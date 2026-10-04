package database

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func budgetPolicy(t *testing.T, q *Queries, n int64) TrafficPolicy {
	t.Helper()
	p := DefaultTrafficPolicy()
	p.EnforcementEnabled = true
	p.ServerBudgetBytes = n
	p.DefaultAccountBudgetBytes = n
	p.Basis = "combined"
	if err := q.SetTrafficPolicy(p); err != nil {
		t.Fatal(err)
	}
	return p
}
func budgetSnapshot(t *testing.T, q *Queries, owner string, now time.Time) TrafficBudgetSnapshot {
	t.Helper()
	s, err := q.TrafficBudgetSnapshot(owner, now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestTrafficLeasesConcurrentAcrossConnections(t *testing.T) {
	q, path := resourceFixture(t)
	budgetPolicy(t, q, 3*TrafficLeaseBytes)
	otherDB, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer otherDB.Close()
	other := NewQueries(otherDB)
	now := time.Now()
	var wg sync.WaitGroup
	leases := make(chan TrafficLease, 24)
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			writer := q
			if i%2 == 0 {
				writer = other
			}
			lease, err := writer.ReserveTraffic("owner", true, TrafficLeaseBytes, now)
			if err != nil {
				failures <- err
			} else {
				leases <- lease
			}
		}(i)
	}
	wg.Wait()
	close(leases)
	close(failures)
	if len(leases) != 3 {
		t.Fatalf("leases=%d", len(leases))
	}
	for err := range failures {
		var exhausted *TrafficExhausted
		if !errors.As(err, &exhausted) {
			t.Fatal(err)
		}
	}
	s := budgetSnapshot(t, q, "", now)
	if s.Usage.ReservedUploadedBytes != 3*TrafficLeaseBytes || s.Usage.RemainingBytes != 0 || s.State != "exhausted" {
		t.Fatal(s)
	}
	for lease := range leases {
		if err = q.SettleTraffic(lease.ID, 7); err != nil {
			t.Fatal(err)
		}
	}
	s = budgetSnapshot(t, q, "owner", now)
	if s.Usage.ObservedUploadedBytes != 21 || s.Usage.ReservedUploadedBytes != 0 || s.Usage.ChargedBytes != 21 {
		t.Fatal(s)
	}
}
func TestTrafficCrashReservationsConservativeAndRestart(t *testing.T) {
	q, path := resourceFixture(t)
	budgetPolicy(t, q, 100)
	now := time.Now()
	lease, err := q.ReserveTraffic("owner", false, 80, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SettleTraffic(lease.ID, 81); err == nil {
		t.Fatal("settled beyond reservation")
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	r := NewQueries(reopened)
	if err = r.RecoverTrafficLeases(); err != nil {
		t.Fatal(err)
	}
	if err = r.RecoverTrafficLeases(); err != nil {
		t.Fatal(err)
	}
	s := budgetSnapshot(t, r, "owner", now)
	if s.Usage.ConservativeDownloadedBytes != 80 || s.Usage.ObservedDownloadedBytes != 0 || s.Usage.ReservedDownloadedBytes != 0 || s.Usage.RemainingBytes != 20 {
		t.Fatal(s)
	}
	next, err := r.ReserveTraffic("owner", false, 100, now)
	if err != nil || next.Bytes != 20 {
		t.Fatalf("%+v %v", next, err)
	}
}
func TestTrafficOverridesBasisChangesAndNoRefund(t *testing.T) {
	q, _ := resourceFixture(t)
	p := budgetPolicy(t, q, 100)
	now := time.Now()
	if err := q.CreateUser(User{ID: "owner", Username: "owner", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	n := int64(30)
	if err := q.SetAccountTrafficBudget("owner", &n); err != nil {
		t.Fatal(err)
	}
	lease, err := q.ReserveTraffic("owner", true, 80, now)
	if err != nil || lease.Bytes != 30 {
		t.Fatalf("%+v %v", lease, err)
	}
	if err = q.SettleTraffic(lease.ID, 30); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReserveTraffic("owner", true, 1, now); err == nil {
		t.Fatal("override bypass")
	}
	p.Basis = "outbound"
	if err = q.SetTrafficPolicy(p); err != nil {
		t.Fatal(err)
	}
	lease, err = q.ReserveTraffic("owner", true, 80, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SettleTraffic(lease.ID, 10); err != nil {
		t.Fatal(err)
	}
	p.Basis = "combined"
	p.EnforcementEnabled = false
	if err = q.SetTrafficPolicy(p); err != nil {
		t.Fatal(err)
	}
	s := budgetSnapshot(t, q, "owner", now)
	if s.Usage.ChargedBytes != 40 || s.State != "ready" {
		t.Fatal(s)
	}
	if err = q.SetAccountTrafficBudget("owner", nil); err != nil {
		t.Fatal(err)
	}
	s = budgetSnapshot(t, q, "owner", now)
	if s.Usage.ChargedBytes != 40 || s.AccountBudgetBytes != nil || s.EffectiveBudgetBytes != 100 {
		t.Fatal(s)
	}
	p.EnforcementEnabled = true
	p.ServerBudgetBytes = 20
	p.DefaultAccountBudgetBytes = 20
	if err = q.SetTrafficPolicy(p); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReserveTraffic("other", false, 1, now); err == nil {
		t.Fatal("lowering reset usage")
	}
}
func TestTrafficCycleRolloverAndPolicyRecomputation(t *testing.T) {
	q, _ := resourceFixture(t)
	p := budgetPolicy(t, q, 100)
	p.CycleStartDay = 31
	if err := q.SetTrafficPolicy(p); err != nil {
		t.Fatal(err)
	}
	before := time.Date(2028, 2, 28, 23, 59, 59, 0, time.UTC)
	after := before.Add(time.Second)
	lease, err := q.ReserveTraffic("owner", false, 100, before)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SettleTraffic(lease.ID, 100); err != nil {
		t.Fatal(err)
	}
	s := budgetSnapshot(t, q, "owner", after)
	if s.Cycle.Start.Day() != 29 || s.Cycle.End.Day() != 31 || s.Usage.ChargedBytes != 0 {
		t.Fatal(s)
	}
	p.CycleStartDay = 1
	if err = q.SetTrafficPolicy(p); err != nil {
		t.Fatal(err)
	}
	s = budgetSnapshot(t, q, "owner", after)
	if s.Usage.ChargedBytes != 100 {
		t.Fatal("cycle change discarded daily history", s)
	}
}
func TestTrafficMigrationSeedsGlobalWithoutInventingOwners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, sqlText := range migrations {
		if strings.Contains(sqlText, "CREATE TABLE traffic_policy") {
			break
		}
		if _, err = db.Exec(sqlText); err != nil {
			t.Fatal(i, err)
		}
		if i > 0 {
			if _, err = db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := time.Now()
	if err = func() error {
		_, e := db.Exec(`INSERT INTO traffic_days(date,uploaded_bytes,downloaded_bytes) VALUES(?,11,22)`, now.UTC().Format("2006-01-02"))
		return e
	}(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	q := NewQueries(migrated)
	global := budgetSnapshot(t, q, "", now)
	account := budgetSnapshot(t, q, "owner", now)
	if global.Usage.ObservedUploadedBytes != 11 || global.Usage.ObservedDownloadedBytes != 22 || account.Usage.ChargedBytes != 0 || account.RecordingStartedAt == "" {
		t.Fatal(global, account)
	}
}
func TestTrafficPolicyFiniteAndIndependentDefaults(t *testing.T) {
	q, _ := resourceFixture(t)
	p, err := q.TrafficPolicy()
	if err != nil || p.EnforcementEnabled || p.ServerBudgetBytes != 100<<30 || p.DefaultAccountBudgetBytes != 10<<30 {
		t.Fatal(p, err)
	}
	for _, edit := range []func(*TrafficPolicy){func(p *TrafficPolicy) { p.ServerBudgetBytes = 0 }, func(p *TrafficPolicy) { p.DownloadBytesPerSecond = 0 }, func(p *TrafficPolicy) { p.MaxActiveStreams = 0 }, func(p *TrafficPolicy) { p.CycleStartDay = 32 }, func(p *TrafficPolicy) { p.Basis = "inbound" }} {
		bad := p
		edit(&bad)
		if q.SetTrafficPolicy(bad) == nil {
			t.Fatal("accepted invalid policy", bad)
		}
	}
	if err = q.SetTrafficSettings(TrafficSettings{&[]int64{1}[0], 31, "combined"}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := q.TrafficPolicy()
	if err != nil || unchanged != p {
		t.Fatal(unchanged, err)
	}
}

func TestTrafficUsagePositiveWrapFailsClosed(t *testing.T) {
	q, _ := resourceFixture(t)
	budgetPolicy(t, q, 100)
	now := time.Now()
	large := int64(1<<62) + 100
	_, err := q.db.Exec(`INSERT INTO traffic_owner_days(owner,date,observed_up,observed_down,conservative_up,conservative_down) VALUES(?,?,?,?,?,?)`, "owner", now.Format("2006-01-02"), large, large, large, large)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.TrafficBudgetSnapshot("owner", now); err == nil {
		t.Fatal("positive wrapped aggregate accepted")
	}
	if _, err = q.ReserveTraffic("owner", false, 1, now); err == nil {
		t.Fatal("overflow admitted payload")
	}
}
func TestTrafficSettlementFailureKeepsReservation(t *testing.T) {
	q, _ := resourceFixture(t)
	budgetPolicy(t, q, 100)
	now := time.Now()
	lease, err := q.ReserveTraffic("owner", true, 100, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.db.Exec(`CREATE TRIGGER reject_budget_settlement BEFORE INSERT ON traffic_owner_days BEGIN SELECT RAISE(ABORT,'test settlement failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = q.SettleTraffic(lease.ID, 3); err == nil {
		t.Fatal("fault not propagated")
	}
	s := budgetSnapshot(t, q, "owner", now)
	if s.Usage.ReservedUploadedBytes != 100 || s.Usage.ObservedUploadedBytes != 0 {
		t.Fatal(s)
	}
	if _, err = q.db.Exec(`DROP TRIGGER reject_budget_settlement`); err != nil {
		t.Fatal(err)
	}
	if err = q.RecoverTrafficLeases(); err != nil {
		t.Fatal(err)
	}
	s = budgetSnapshot(t, q, "owner", now)
	if s.Usage.ConservativeUploadedBytes != 100 || s.Usage.ObservedUploadedBytes != 0 {
		t.Fatal(s)
	}
}

func TestTrafficPolicyGenerationStopsRenewalBeforeCancellation(t *testing.T) {
	q, _ := resourceFixture(t)
	p := budgetPolicy(t, q, 1000)
	now := time.Now()
	for _, owner := range []string{"alice", "bob"} {
		if err := q.CreateUser(User{ID: owner, Username: owner, Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
			t.Fatal(err)
		}
	}
	_, aliceVersion, err := q.TrafficPolicyVersion("alice")
	if err != nil {
		t.Fatal(err)
	}
	_, bobVersion, err := q.TrafficPolicyVersion("bob")
	if err != nil {
		t.Fatal(err)
	}
	active, err := q.ReserveTraffic("alice", false, 100, now, aliceVersion)
	if err != nil {
		t.Fatal(err)
	}
	override := int64(500)
	if err = q.SetAccountTrafficBudget("alice", &override); err != nil {
		t.Fatal(err)
	}
	// No process cancellation has run: the old in-flight lease can settle, but
	// its next lease must fail atomically under the persisted owner generation.
	if err = q.SettleTraffic(active.ID, 90); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReserveTraffic("alice", false, 100, now, aliceVersion); !errors.Is(err, ErrTrafficPolicyChanged) {
		t.Fatal("stale owner renewed", err)
	}
	bob, err := q.ReserveTraffic("bob", false, 100, now, bobVersion)
	if err != nil {
		t.Fatal("unrelated owner canceled", err)
	}
	if err = q.SettleTraffic(bob.ID, 0); err != nil {
		t.Fatal(err)
	}
	_, overrideVersion, err := q.TrafficPolicyVersion("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SetAccountTrafficBudget("alice", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReserveTraffic("alice", false, 1, now, overrideVersion); !errors.Is(err, ErrTrafficPolicyChanged) {
		t.Fatal("inheritance reset generation", err)
	}
	p.DownloadBytesPerSecond = 1
	if err = q.SetTrafficPolicy(p); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReserveTraffic("bob", false, 1, now, bobVersion); !errors.Is(err, ErrTrafficPolicyChanged) {
		t.Fatal("stale global bandwidth renewed", err)
	}
	_, latest, err := q.TrafficPolicyVersion("bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReserveTraffic("bob", false, 1, now, latest); err != nil {
		t.Fatal("explicit new policy failed", err)
	}
}

func TestTrafficConcurrencyEnvironmentSeedsOnlyOnce(t *testing.T) {
	q, path := resourceFixture(t)
	if err := q.InitializeTrafficConcurrency(19, 7, 8, 9, 10); err != nil {
		t.Fatal(err)
	}
	p, err := q.TrafficPolicy()
	if err != nil || p.MaxActiveStreams != 19 || p.MaxStreamsPerAccount != 7 || p.MaxStreamsPerIP != 8 || p.MaxStreamsPerTransfer != 9 || p.MaxStreamsPerSlot != 10 {
		t.Fatal(p, err)
	}
	if err = q.InitializeTrafficConcurrency(2, 1, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	same, err := q.TrafficPolicy()
	if err != nil || same != p {
		t.Fatal("environment changed persistedpolicy", same, err)
	}
	p.MaxActiveStreams = 32
	p.MaxStreamsPerAccount = 16
	if err = q.SetTrafficPolicy(p); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	other := NewQueries(reopened)
	if err = other.InitializeTrafficConcurrency(2, 1, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	got, err := other.TrafficPolicy()
	if err != nil || got != p {
		t.Fatal("restart overrode administrator", got, err)
	}
}
