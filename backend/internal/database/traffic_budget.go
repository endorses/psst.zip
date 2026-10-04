package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"math"
	"time"
)

const TrafficLeaseBytes int64 = 64 << 10

var ErrTrafficAccounting = errors.New("traffic accounting unavailable")
var ErrTrafficPolicyChanged = errors.New("traffic policy changed; retry explicitly")

type TrafficExhausted struct{ RetryAt time.Time }

func (e *TrafficExhausted) Error() string { return "traffic budget exhausted" }

type TrafficPolicy struct {
	EnforcementEnabled        bool   `json:"enforcement_enabled"`
	ServerBudgetBytes         int64  `json:"server_budget_bytes"`
	DefaultAccountBudgetBytes int64  `json:"default_account_budget_bytes"`
	Basis                     string `json:"basis"`
	CycleStartDay             int    `json:"cycle_start_day"`
	UploadBytesPerSecond      int64  `json:"upload_bytes_per_second"`
	DownloadBytesPerSecond    int64  `json:"download_bytes_per_second"`
	MaxActiveStreams          int    `json:"max_active_streams"`
	MaxStreamsPerAccount      int    `json:"max_streams_per_account"`
	MaxStreamsPerIP           int    `json:"max_streams_per_ip"`
	MaxStreamsPerTransfer     int    `json:"max_streams_per_transfer"`
	MaxStreamsPerSlot         int    `json:"max_streams_per_slot"`
}

func DefaultTrafficPolicy() TrafficPolicy {
	return TrafficPolicy{false, 100 << 30, 10 << 30, "outbound", 1, 100 << 20, 100 << 20, 64, 4, 4, 4, 4}
}
func (p TrafficPolicy) Validate() error {
	if p.ServerBudgetBytes < 1 || p.ServerBudgetBytes > 9007199254740991 || p.DefaultAccountBudgetBytes < 1 || p.DefaultAccountBudgetBytes > p.ServerBudgetBytes {
		return errors.New("budgets must be positive safe integers; account budget cannot exceed server budget")
	}
	if p.Basis != "outbound" && p.Basis != "combined" || p.CycleStartDay < 1 || p.CycleStartDay > 31 {
		return errors.New("invalid traffic cycle or basis")
	}
	for _, n := range []int64{p.UploadBytesPerSecond, p.DownloadBytesPerSecond} {
		if n < 1 || n > 10<<30 {
			return errors.New("bandwidth must be between 1 and 10737418240 bytes per second")
		}
	}
	for _, n := range []int{p.MaxActiveStreams, p.MaxStreamsPerAccount, p.MaxStreamsPerIP, p.MaxStreamsPerTransfer, p.MaxStreamsPerSlot} {
		if n < 1 || n > 4096 {
			return errors.New("stream limits must be between 1 and 4096")
		}
	}
	return nil
}
func trafficBudgetMigration() string {
	b, _ := json.Marshal(DefaultTrafficPolicy())
	return fmt.Sprintf(`CREATE TABLE traffic_policy(id INTEGER PRIMARY KEY CHECK(id=1),policy TEXT NOT NULL,recording_started_at TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1,concurrency_initialized INTEGER NOT NULL DEFAULT 0 CHECK(concurrency_initialized IN (0,1)));
 INSERT INTO traffic_policy(id,policy,recording_started_at,revision) VALUES(1,'%s',strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ','now'),1);
 CREATE TABLE traffic_account_policy(owner TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,budget INTEGER CHECK(budget IS NULL OR (budget>0 AND budget<=9007199254740991)),revision INTEGER NOT NULL DEFAULT 1);
 CREATE TABLE traffic_owner_days(owner TEXT NOT NULL,date TEXT NOT NULL,observed_up INTEGER NOT NULL DEFAULT 0 CHECK(typeof(observed_up)='integer' AND observed_up>=0),observed_down INTEGER NOT NULL DEFAULT 0 CHECK(typeof(observed_down)='integer' AND observed_down>=0),conservative_up INTEGER NOT NULL DEFAULT 0 CHECK(typeof(conservative_up)='integer' AND conservative_up>=0),conservative_down INTEGER NOT NULL DEFAULT 0 CHECK(typeof(conservative_down)='integer' AND conservative_down>=0),PRIMARY KEY(owner,date));
 CREATE INDEX traffic_owner_days_date ON traffic_owner_days(date);
 INSERT INTO traffic_owner_days(owner,date,observed_up,observed_down) SELECT '',date,uploaded_bytes,downloaded_bytes FROM traffic_days;
 CREATE TABLE traffic_leases(id TEXT PRIMARY KEY,owner TEXT NOT NULL,date TEXT NOT NULL,direction TEXT NOT NULL CHECK(direction IN ('up','down')),bytes INTEGER NOT NULL CHECK(bytes BETWEEN 1 AND 65536));
 CREATE INDEX traffic_leases_owner ON traffic_leases(owner,date);`, b)
}

type trafficQuerier interface{ QueryRow(string, ...any) *sql.Row }

func readTrafficPolicy(q trafficQuerier) (TrafficPolicy, string, error) {
	var p TrafficPolicy
	var raw, epoch string
	err := q.QueryRow(`SELECT policy,recording_started_at FROM traffic_policy WHERE id=1`).Scan(&raw, &epoch)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &p)
	}
	if err == nil {
		err = p.Validate()
	}
	return p, epoch, err
}
func (q *Queries) TrafficPolicy() (TrafficPolicy, error) {
	p, _, err := readTrafficPolicy(q.db)
	return p, err
}
func (q *Queries) SetTrafficPolicy(p TrafficPolicy, actors ...*AdminActor) error {
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := q.beginAdminMutation(actors)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	auditChanged := false
	if optionalAdminActor(actors) != nil {
		previous, _, readErr := readTrafficPolicy(tx)
		if readErr != nil {
			return readErr
		}
		auditChanged = previous != p
	}

	result, err := tx.Exec(`UPDATE traffic_policy SET policy=?,revision=revision+1,concurrency_initialized=1 WHERE id=1`, string(b))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrTrafficAccounting
	}
	if auditChanged {
		if err = q.auditAdminMutation(tx, actors, "settings.traffic_policy_changed", "server", "", false); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (q *Queries) SetAccountTrafficBudget(owner string, budget *int64, actors ...*AdminActor) error {
	tx, err := q.beginAdminMutation(actors)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	auditChanged := false
	if optionalAdminActor(actors) != nil {
		var previous sql.NullInt64
		if err = tx.QueryRow(`SELECT budget FROM traffic_account_policy WHERE owner=?`, owner).Scan(&previous); err != nil && err != sql.ErrNoRows {
			return err
		}
		auditChanged = previous.Valid != (budget != nil) || (budget != nil && previous.Int64 != *budget)
	}

	if _, err = tx.Exec(`UPDATE traffic_policy SET revision=revision WHERE id=1`); err != nil {
		return err
	}
	p, _, err := readTrafficPolicy(tx)
	if err != nil {
		return err
	}
	var found string
	if err = tx.QueryRow(`SELECT id FROM users WHERE id=?`, owner).Scan(&found); err != nil {
		return err
	}
	if budget != nil && (*budget < 1 || *budget > p.ServerBudgetBytes) {
		return errors.New("account budget must be positive and no greater than server budget")
	}
	_, err = tx.Exec(`INSERT INTO traffic_account_policy(owner,budget) VALUES(?,?) ON CONFLICT(owner) DO UPDATE SET budget=excluded.budget,revision=revision+1`, owner, budget)
	if err != nil {
		return err
	}
	if auditChanged {
		if err = q.auditAdminMutation(tx, actors, "settings.account_traffic_changed", "user", owner, false); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type TrafficCycle struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func TrafficBudgetCycle(now time.Time, day int) TrafficCycle {
	now = now.UTC()
	start := func(y int, m time.Month) time.Time {
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
		return time.Date(y, m, min(day, last), 0, 0, 0, 0, time.UTC)
	}
	a := start(now.Year(), now.Month())
	if now.Before(a) {
		a = start(now.Year(), now.Month()-1)
	}
	return TrafficCycle{a, start(a.Year(), a.Month()+1)}
}

type TrafficBudgetUsage struct {
	ObservedUploadedBytes       int64 `json:"observed_uploaded_bytes"`
	ObservedDownloadedBytes     int64 `json:"observed_downloaded_bytes"`
	ReservedUploadedBytes       int64 `json:"reserved_uploaded_bytes"`
	ReservedDownloadedBytes     int64 `json:"reserved_downloaded_bytes"`
	ConservativeUploadedBytes   int64 `json:"conservative_uploaded_bytes"`
	ConservativeDownloadedBytes int64 `json:"conservative_downloaded_bytes"`
	ChargedBytes                int64 `json:"charged_bytes"`
	BudgetBytes                 int64 `json:"budget_bytes"`
	RemainingBytes              int64 `json:"remaining_bytes"`
}
type TrafficBudgetSnapshot struct {
	Policy               TrafficPolicy      `json:"policy"`
	Usage                TrafficBudgetUsage `json:"usage"`
	RecordingStartedAt   string             `json:"recording_started_at"`
	Cycle                TrafficCycle       `json:"cycle"`
	LeaseBytes           int64              `json:"lease_bytes"`
	State                string             `json:"state"`
	AccountBudgetBytes   *int64             `json:"account_budget_bytes"`
	EffectiveBudgetBytes int64              `json:"effective_budget_bytes"`
}

func trafficUsage(q trafficQuerier, owner string, global bool, p TrafficPolicy, cycle TrafficCycle) (TrafficBudgetUsage, *int64, error) {
	u := TrafficBudgetUsage{BudgetBytes: p.ServerBudgetBytes}
	floor, err := trafficRetainedFrom(q)
	if err != nil {
		return u, nil, err
	}
	if cycle.Start.UTC().Format("2006-01-02") < floor {
		return u, nil, ErrTrafficAccounting
	}
	var override *int64
	if !global {
		u.BudgetBytes = p.DefaultAccountBudgetBytes
		var b sql.NullInt64
		err := q.QueryRow(`SELECT budget FROM traffic_account_policy WHERE owner=?`, owner).Scan(&b)
		if err == nil && b.Valid {
			override = &b.Int64
			u.BudgetBytes = min(b.Int64, p.ServerBudgetBytes)
		} else if err != nil && err != sql.ErrNoRows {
			return u, nil, err
		}
	}
	first, last := cycle.Start.Format("2006-01-02"), cycle.End.Format("2006-01-02")
	filter := ""
	args := []any{first, last}
	if !global {
		filter = " AND owner=?"
		args = append(args, owner)
	}
	err = q.QueryRow(`SELECT COALESCE(SUM(observed_up),0),COALESCE(SUM(observed_down),0),COALESCE(SUM(conservative_up),0),COALESCE(SUM(conservative_down),0) FROM traffic_owner_days WHERE date>=? AND date<?`+filter, args...).Scan(&u.ObservedUploadedBytes, &u.ObservedDownloadedBytes, &u.ConservativeUploadedBytes, &u.ConservativeDownloadedBytes)
	if err != nil {
		return u, override, err
	}
	err = q.QueryRow(`SELECT COALESCE(SUM(CASE WHEN direction='up' THEN bytes ELSE 0 END),0),COALESCE(SUM(CASE WHEN direction='down' THEN bytes ELSE 0 END),0) FROM traffic_leases WHERE date>=? AND date<?`+filter, args...).Scan(&u.ReservedUploadedBytes, &u.ReservedDownloadedBytes)
	if err != nil {
		return u, override, err
	}
	fields := []int64{u.ObservedDownloadedBytes, u.ConservativeDownloadedBytes, u.ReservedDownloadedBytes}
	if p.Basis == "combined" {
		fields = append(fields, u.ObservedUploadedBytes, u.ConservativeUploadedBytes, u.ReservedUploadedBytes)
	}
	for _, n := range fields {
		if n < 0 || u.ChargedBytes > math.MaxInt64-n {
			return u, override, ErrTrafficAccounting
		}
		u.ChargedBytes += n
	}
	u.RemainingBytes = max(0, u.BudgetBytes-u.ChargedBytes)
	return u, override, nil
}
func (q *Queries) TrafficBudgetSnapshot(owner string, now time.Time) (TrafficBudgetSnapshot, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return TrafficBudgetSnapshot{}, err
	}
	defer tx.Rollback()
	p, epoch, err := readTrafficPolicy(tx)
	if err != nil {
		return TrafficBudgetSnapshot{}, err
	}
	c := TrafficBudgetCycle(now, p.CycleStartDay)
	u, override, err := trafficUsage(tx, owner, owner == "", p, c)
	if err != nil {
		return TrafficBudgetSnapshot{}, err
	}
	state := "ready"
	if p.EnforcementEnabled && u.RemainingBytes == 0 {
		state = "exhausted"
	}
	if owner != "" && p.EnforcementEnabled {
		global, _, e := trafficUsage(tx, "", true, p, c)
		if e != nil {
			return TrafficBudgetSnapshot{}, e
		}
		if global.RemainingBytes == 0 {
			state = "exhausted"
		}
	}
	return TrafficBudgetSnapshot{p, u, epoch, c, TrafficLeaseBytes, state, override, u.BudgetBytes}, nil
}

type TrafficLease struct {
	ID    string
	Bytes int64
}

func (q *Queries) ReserveTraffic(owner string, upload bool, want int64, now time.Time, expectedVersion ...string) (TrafficLease, error) {
	if owner == "" {
		owner = "legacy"
	}
	want = min(want, TrafficLeaseBytes)
	if want < 1 {
		return TrafficLease{}, ErrTrafficAccounting
	}
	tx, err := q.db.Begin()
	if err != nil {
		return TrafficLease{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE traffic_policy SET revision=revision WHERE id=1`); err != nil {
		return TrafficLease{}, err
	}
	p, _, err := readTrafficPolicy(tx)
	if err != nil {
		return TrafficLease{}, err
	}
	if len(expectedVersion) > 0 {
		actual, err := trafficPolicyVersion(tx, owner)
		if err != nil {
			return TrafficLease{}, err
		}
		if actual != expectedVersion[0] {
			return TrafficLease{}, ErrTrafficPolicyChanged
		}
	}
	cycle := TrafficBudgetCycle(now, p.CycleStartDay)
	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM traffic_leases`).Scan(&count); err != nil {
		return TrafficLease{}, err
	}
	if count >= 4096 {
		return TrafficLease{}, ErrTrafficAccounting
	}
	// Read and validate both ledgers even with enforcement disabled. A broken
	// counter is an accounting failure, never permission to continue unrecorded.
	chargedDirection := !upload || p.Basis == "combined"
	for _, global := range []bool{true, false} {
		u, _, e := trafficUsage(tx, owner, global, p, cycle)
		if e != nil {
			return TrafficLease{}, e
		}
		if p.EnforcementEnabled && chargedDirection {
			want = min(want, u.RemainingBytes)
		}
	}
	if want < 1 {
		return TrafficLease{}, &TrafficExhausted{cycle.End}
	}
	direction := "down"
	if upload {
		direction = "up"
	}
	lease := TrafficLease{uuid.NewString(), want}
	_, err = tx.Exec(`INSERT INTO traffic_leases(id,owner,date,direction,bytes) VALUES(?,?,?,?,?)`, lease.ID, owner, now.UTC().Format("2006-01-02"), direction, want)
	if err != nil {
		return TrafficLease{}, err
	}
	if err = tx.Commit(); err != nil {
		return TrafficLease{}, err
	}
	return lease, nil
}
func (q *Queries) SettleTraffic(id string, actual int64) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE traffic_policy SET revision=revision WHERE id=1`); err != nil {
		return err
	}
	var owner, date, direction string
	var reserved int64
	if err = tx.QueryRow(`SELECT owner,date,direction,bytes FROM traffic_leases WHERE id=?`, id).Scan(&owner, &date, &direction, &reserved); err != nil {
		return err
	}
	if actual < 0 || actual > reserved {
		return ErrTrafficAccounting
	}
	up, down := int64(0), int64(0)
	if direction == "up" {
		up = actual
	} else {
		down = actual
	}
	floor, err := trafficRetainedFrom(tx)
	if err != nil {
		return err
	}
	if date < floor {
		err = archiveBudgetTraffic(tx, up, down, 0, 0)
	} else {
		_, err = tx.Exec(`INSERT INTO traffic_owner_days(owner,date,observed_up,observed_down) VALUES(?,?,?,?) ON CONFLICT(owner,date) DO UPDATE SET observed_up=observed_up+excluded.observed_up,observed_down=observed_down+excluded.observed_down`, owner, date, up, down)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM traffic_leases WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RecoverTrafficLeases must run once during process startup, before accepting IO.
// Interrupted leases are charged fully; they are never reported as observed bytes.
func (q *Queries) RecoverTrafficLeases() error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE traffic_policy SET revision=revision WHERE id=1`); err != nil {
		return err
	}
	floor, err := trafficRetainedFrom(tx)
	if err != nil {
		return err
	}
	var up, down int64
	if err = tx.QueryRow(`SELECT COALESCE(SUM(CASE WHEN direction='up' THEN bytes ELSE 0 END),0),COALESCE(SUM(CASE WHEN direction='down' THEN bytes ELSE 0 END),0) FROM traffic_leases WHERE date<?`, floor).Scan(&up, &down); err != nil {
		return err
	}
	if err = archiveBudgetTraffic(tx, 0, 0, up, down); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO traffic_owner_days(owner,date,conservative_up,conservative_down) SELECT owner,date,SUM(CASE WHEN direction='up' THEN bytes ELSE 0 END),SUM(CASE WHEN direction='down' THEN bytes ELSE 0 END) FROM traffic_leases WHERE date>=? GROUP BY owner,date ON CONFLICT(owner,date) DO UPDATE SET conservative_up=conservative_up+excluded.conservative_up,conservative_down=conservative_down+excluded.conservative_down`, floor)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM traffic_leases`); err != nil {
		return err
	}
	return tx.Commit()
}

func trafficPolicyVersion(q trafficQuerier, owner string) (string, error) {
	var version string
	err := q.QueryRow(`SELECT printf('%d:%d',p.revision,COALESCE(a.revision,0)) FROM traffic_policy p LEFT JOIN traffic_account_policy a ON a.owner=? WHERE p.id=1`, owner).Scan(&version)
	return version, err
}

// Policy and account generation are read from one snapshot. A renewal compares
// these inside its serialized allocation transaction, closing cancellation races.
func (q *Queries) TrafficPolicyVersion(owner string) (TrafficPolicy, string, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return TrafficPolicy{}, "", err
	}
	defer tx.Rollback()
	p, _, err := readTrafficPolicy(tx)
	if err != nil {
		return p, "", err
	}
	version, err := trafficPolicyVersion(tx, owner)
	return p, version, err
}

// InitializeTrafficConcurrency preserves environment operator intent exactly
// once on migration/startup. Later persisted administrator changes are authoritative.
func (q *Queries) InitializeTrafficConcurrency(total, account, ip, transfer, slot int) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE traffic_policy SET revision=revision WHERE id=1`); err != nil {
		return err
	}
	var initialized bool
	if err = tx.QueryRow(`SELECT concurrency_initialized FROM traffic_policy WHERE id=1`).Scan(&initialized); err != nil {
		return err
	}
	if initialized {
		return nil
	}
	p, _, err := readTrafficPolicy(tx)
	if err != nil {
		return err
	}
	p.MaxActiveStreams = total
	p.MaxStreamsPerAccount = account
	p.MaxStreamsPerIP = ip
	p.MaxStreamsPerTransfer = transfer
	p.MaxStreamsPerSlot = slot
	if err = p.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE traffic_policy SET policy=?,concurrency_initialized=1,revision=revision+1 WHERE id=1`, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
