package database

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func authMetadataUser(t *testing.T) (*Queries, User) {
	t.Helper()
	q, _ := resourceFixture(t)
	u := User{ID: "metadata-user", Username: "metadata-user", Role: "user", PasswordHash: []byte("hash")}
	if err := q.CreateUser(u, false); err != nil {
		t.Fatal(err)
	}
	return q, u
}
func metadataSession(u User, id string, at time.Time) Session {
	return Session{ID: id, UserID: u.ID, DeviceName: "Test", CreatedAt: at, ExpiresAt: at.Add(30 * 24 * time.Hour)}
}
func seedMetadataSessions(t *testing.T, q *Queries, u User, count int, at time.Time) {
	t.Helper()
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := 0; i < count; i++ {
		s := metadataSession(u, fmt.Sprintf("session-%04d", i), at.Add(time.Duration(i)*time.Millisecond))
		if _, err = tx.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES(?,?,?,?,?,?)`, s.ID, u.ID, []byte(s.ID), s.DeviceName, s.CreatedAt.UTC(), s.ExpiresAt.UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func countAuthRows(t *testing.T, q *Queries, table string) int {
	t.Helper()
	var n int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAuthMetadataAccountCapacityConcurrent(t *testing.T) {
	q, u := authMetadataUser(t)
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < MaxAuthUsers-1; i++ {
		id := fmt.Sprint("user-", i)
		if _, err = tx.Exec(`INSERT INTO users(id,username,role,password_hash,disabled) VALUES(?,?,'user',?,1)`, id, id, u.PasswordHash); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := u
			v.ID = fmt.Sprint("candidate-", i)
			v.Username = v.ID
			results <- q.CreateUser(v, false)
		}(i)
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrAccountCapacity) {
			t.Fatal(err)
		}
	}
	if ok != 1 || countAuthRows(t, q, "users") != MaxAuthUsers {
		t.Fatal("account cap raced", ok)
	}
	if err = q.CreateUser(User{ID: "bootstrap", Username: "bootstrap", Role: "admin", PasswordHash: u.PasswordHash}, true); err != nil {
		t.Fatal("existing bootstrap no longer no-op", err)
	}
}
func TestAuthMetadataConcurrentSessionsRotateAndFailedInsertRollsBack(t *testing.T) {
	q, u := authMetadataUser(t)
	at := time.Now().Add(-time.Minute)
	seedMetadataSessions(t, q, u, MaxAuthSessionsPerUser, at)
	results := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint("new-", i)
			results <- q.CreateSession(metadataSession(u, id, time.Now()), []byte(id), u.PasswordHash)
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	list, err := q.AuthenticationSessions(u.ID, "")
	if err != nil || list.TotalActive != MaxAuthSessionsPerUser || list.Limited || len(list.Sessions) != MaxAuthSessionsPerUser {
		t.Fatal(list, err)
	}
	before := countAuthRows(t, q, "sessions")
	if err = q.CreateSession(metadataSession(u, "new-0", time.Now()), []byte("collision"), u.PasswordHash); err == nil {
		t.Fatal("duplicate accepted")
	}
	if got := countAuthRows(t, q, "sessions"); got != before {
		t.Fatal("failed insert retired session", got, before)
	}
	if err = q.CreateSession(metadataSession(u, "wrong-proof", time.Now()), []byte("wrong-proof"), []byte("wrong")); err == nil {
		t.Fatal("wrong proof accepted")
	}
	for i := 0; i < 16; i++ {
		s, _, err := q.SessionByHash([]byte(fmt.Sprint("new-", i)))
		if err != nil || !time.Now().Before(s.ExpiresAt) {
			t.Fatal("new session lost", i, err)
		}
	}
}
func TestAuthMetadataAdministratorRecoveryRotationAtomic(t *testing.T) {
	q, u, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	codes := enrollAdminTestFactor(t, q, u, now)
	seedMetadataSessions(t, q, u, MaxAuthSessionsPerUser, now.Add(-time.Hour))
	state, err := q.AdminSecurity(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	collision := metadataSession(u, "session-0031", now)
	if err = q.CreateAdminSession(collision, []byte("collision"), u.PasswordHash, state.Revision, "", codes[0], now); err == nil {
		t.Fatal("collision accepted")
	}
	if countAuthRows(t, q, "admin_recovery_codes") != 10 || countAuthRows(t, q, "sessions") != 32 {
		t.Fatal("rollback lost session/recovery")
	}
	valid := metadataSession(u, "recovered", now)
	if err = q.CreateAdminSession(valid, []byte("recovered"), u.PasswordHash, state.Revision, "", codes[0], now); err != nil {
		t.Fatal(err)
	}
	list, err := q.AuthenticationSessions(u.ID, "recovered")
	if err != nil || list.TotalActive != 32 || countAuthRows(t, q, "admin_recovery_codes") != 9 {
		t.Fatal(list, err)
	}
}
func TestAuthMetadataPairingCapsReplacementAndParentPreserved(t *testing.T) {
	q, u := authMetadataUser(t)
	now := time.Now()
	seedMetadataSessions(t, q, u, 32, now.Add(-time.Hour))
	parent := "session-0000"
	for i := 0; i < 7; i++ {
		id := fmt.Sprint("pair-", i)
		if err := q.CreateTrackedPairing(id, []byte(id), u.ID, parent, now.Add(5*time.Minute), ""); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint("concurrent-", i)
			results <- q.CreateTrackedPairing(id, []byte(id), u.ID, parent, now.Add(5*time.Minute), "")
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrPairingCapacity) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	if err := q.CreateTrackedPairing("replacement", []byte("replacement"), u.ID, parent, now.Add(5*time.Minute), "pair-0"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RedeemPairing([]byte("replacement"), []byte("phone"), metadataSession(u, "phone", now)); err != nil {
		t.Fatal(err)
	}
	s, _, err := q.SessionByHash([]byte(parent))
	if err != nil || !time.Now().Before(s.ExpiresAt) {
		t.Fatal("pairing retired issuer", err)
	}
	status, err := q.PairingStatus("replacement", u.ID, parent)
	if err != nil || status.Status != "connected" {
		t.Fatal(status, err)
	}
	// Terminal rows count toward retained capacity, not just pending grants.
	for i := countAuthRows(t, q, "pairings"); i < 64; i++ {
		id := fmt.Sprint("terminal-", i)
		if _, err = q.db.Exec(`INSERT INTO pairings(id,code_hash,user_id,session_id,expires_at,status) VALUES(?,?,?,?,?,'canceled')`, id, []byte(id), u.ID, parent, now.Add(time.Minute).UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if err = q.CreateTrackedPairing("full", []byte("full"), u.ID, parent, now.Add(time.Minute), "pair-1"); !errors.Is(err, ErrPairingCapacity) {
		t.Fatal(err)
	}
	status, err = q.PairingStatus("pair-1", u.ID, parent)
	if err != nil || status.Status != "pending" {
		t.Fatal("failed replacement canceled old grant", status, err)
	}
}
func TestAuthMetadataLegacyPruningBoundedAndListingCurrent(t *testing.T) {
	q, u := authMetadataUser(t)
	now := time.Now()
	seedMetadataSessions(t, q, u, 1000, now.Add(-time.Hour))
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		id := fmt.Sprint("legacy-", i)
		if _, err = tx.Exec(`INSERT INTO pairings(id,code_hash,user_id,session_id,expires_at) VALUES(?,?,?,?,?)`, id, []byte(id), u.ID, "session-0000", now.Add(time.Hour).UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = q.CreateSession(metadataSession(u, "new", now), []byte("new"), u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	if got := countAuthRows(t, q, "pairings"); got != 2000-MaxAuthPairingsPerUser {
		t.Fatal("login cascaded large pairing history", got)
	}
	list, err := q.AuthenticationSessions(u.ID, "session-0500")
	if err != nil || !list.Limited || list.TotalActive != 33 || list.TotalActiveExact || len(list.Sessions) != 32 {
		t.Fatal(list, err)
	}
	found := false
	for _, s := range list.Sessions {
		found = found || s.ID == "session-0500"
	}
	if !found {
		t.Fatal("current session hidden")
	}
	beforeSessions, beforePairs := countAuthRows(t, q, "sessions"), countAuthRows(t, q, "pairings")
	if err = q.PruneAuthentication(); err != nil {
		t.Fatal(err)
	}
	if removed := beforePairs - countAuthRows(t, q, "pairings"); removed > authPruneBatch {
		t.Fatal("unbounded parent cascade", removed)
	}
	if removed := beforeSessions - countAuthRows(t, q, "sessions"); removed > authPruneBatch {
		t.Fatal("unbounded sessions", removed)
	}
	for i := 0; i < 50; i++ {
		if err = q.PruneAuthentication(); err != nil {
			t.Fatal(err)
		}
	}
	list, err = q.AuthenticationSessions(u.ID, "")
	if err != nil || list.TotalActive != 32 || list.Limited || countAuthRows(t, q, "pairings") != 0 {
		t.Fatal("legacy did not converge", list, err)
	}
	if countAuthRows(t, q, "users") != 1 {
		t.Fatal("cleanup deleted account")
	}
}
func TestAuthMetadataCleanupUsesExpiryIndexes(t *testing.T) {
	q, _ := authMetadataUser(t)
	for _, sample := range []struct {
		query, index string
		args         []any
	}{
		{`SELECT id FROM pairings WHERE substr(expires_at,1,19)<? ORDER BY substr(expires_at,1,19),id LIMIT ?`, "auth_pairing_expiry", []any{authTimePrefix(time.Now()), 256}},
		{`SELECT id FROM sessions WHERE user_id=? AND expires_at>? ORDER BY expires_at DESC,id DESC LIMIT ?`, "auth_session_user_expiry", []any{"metadata-user", time.Now().UTC(), 33}},
		{`SELECT id FROM sessions WHERE user_id=? AND id!=? AND expires_at>? AND id NOT IN (SELECT id FROM sessions WHERE user_id=? AND id!=? AND expires_at>? ORDER BY expires_at DESC,id DESC LIMIT ?) ORDER BY expires_at,id LIMIT ?`, "auth_session_user_expiry", []any{"metadata-user", "", time.Now().UTC(), "metadata-user", "", time.Now().UTC(), 31, 128}},
		{`SELECT p.id FROM (SELECT id FROM sessions WHERE substr(expires_at,1,19)<? ORDER BY substr(expires_at,1,19),id LIMIT ?) s JOIN pairings p ON p.session_id=s.id LIMIT ?`, "auth_session_expiry", []any{authTimePrefix(time.Now()), 256, 256}},
	} {
		rows, err := q.db.Query("EXPLAIN QUERY PLAN "+sample.query, sample.args...)
		if err != nil {
			t.Fatal(err)
		}
		details := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details += detail + "\n"
		}
		closeFixture(t, rows)
		if !strings.Contains(details, sample.index) {
			t.Fatal("missing expiry index", details)
		}
		if strings.Contains(details, "USE TEMP B-TREE") {
			t.Fatal("unbounded sort", details)
		}
		if strings.Contains(details, "SCAN p") {
			t.Fatal("unbounded pairings scan", details)
		}
	}
}

func TestAuthMetadataExpiredPairingTreesCannotBlockSessionRotation(t *testing.T) {
	q, u := authMetadataUser(t)
	now := time.Now().UTC()
	seedMetadataSessions(t, q, u, 32, now.Add(-time.Hour))
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := 0; i < 256; i++ {
		id := fmt.Sprintf("expired-%04d", i)
		if _, err = tx.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES(?,?,?,?,?,?)`, id, u.ID, []byte(id), "Expired browser", now.Add(-48*time.Hour), now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 4; j++ {
			pair := fmt.Sprintf("tree-%04d-%d", i, j)
			if _, err = tx.Exec(`INSERT INTO pairings(id,code_hash,user_id,session_id,expires_at) VALUES(?,?,?,?,?)`, pair, []byte(pair), u.ID, id, now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = q.CreateSession(metadataSession(u, "new-owner-login", now), []byte("new-owner-login"), u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	list, err := q.AuthenticationSessions(u.ID, "new-owner-login")
	if err != nil || list.TotalActive != 32 || !list.TotalActiveExact {
		t.Fatal("expired rows blocked capacity rotation", list, err)
	}
	old, _, err := q.SessionByHash([]byte("session-0000"))
	if err == nil && now.Before(old.ExpiresAt) {
		t.Fatal("earliest active session retained")
	}
	if got := countAuthRows(t, q, "pairings"); got != 1024-2*MaxAuthPairingsPerUser {
		t.Fatal("login cleanup exceeded fixed child budget", got)
	}
	// A maintenance sweep must also pass the remaining expired trees to retire
	// active upgrade overages; ordinary account cleanup cannot starve it.
	for i := 0; i < 8; i++ {
		s := metadataSession(u, fmt.Sprint("extra-", i), now.Add(time.Duration(i)*time.Millisecond))
		if _, err = q.db.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES(?,?,?,?,?,?)`, s.ID, u.ID, []byte(s.ID), "Browser", s.CreatedAt, s.ExpiresAt); err != nil {
			t.Fatal(err)
		}
	}
	if err = q.PruneAuthentication(); err != nil {
		t.Fatal(err)
	}
	list, err = q.AuthenticationSessions(u.ID, "")
	if err != nil || list.TotalActive != 32 || !list.TotalActiveExact {
		t.Fatal("maintenance stalled behind expired parents", list, err)
	}
}
