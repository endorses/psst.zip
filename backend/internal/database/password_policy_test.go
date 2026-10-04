package database

import (
	"bytes"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func passwordPolicyDB(t *testing.T) (*sql.DB, *Queries) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "password-policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, NewQueries(db)
}

func passwordPolicySession(t *testing.T, q *Queries, user User, id string) {
	t.Helper()
	session := Session{ID: id, UserID: user.ID, DeviceName: "Browser", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := q.CreateSession(session, []byte(id+"-secret"), user.PasswordHash); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordPolicyMigrationPreservesExistingAccountsAndCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for version, migration := range migrations {
		if strings.Contains(migration, "ADD COLUMN must_change_password") {
			break
		}
		if _, err := legacy.Exec(migration); err != nil {
			t.Fatal(err)
		}
		if version > 0 {
			if _, err := legacy.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, role := range []string{"user", "admin"} {
		if _, err := legacy.Exec(`INSERT INTO users(id,username,role,password_hash) VALUES(?,?,?,?)`, role, role, role, []byte(role+"-password")); err != nil {
			t.Fatal(err)
		}
	}
	q := NewQueries(legacy)
	if _, err := legacy.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES(?,?,?,?,?,?)`, "existing-session", "user", []byte("existing-session-secret"), "Browser", time.Now().UTC(), time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO pairings(id,code_hash,user_id,session_id,expires_at) VALUES(?,?,?,?,?)`, "existing-grant", []byte("grant-secret"), "user", "existing-session", time.Now().Add(time.Minute).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		upgraded, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		q = NewQueries(upgraded)
		for _, role := range []string{"user", "admin"} {
			user, err := q.UserByID(role)
			if err != nil || user.MustChangePassword || !bytes.Equal(user.PasswordHash, []byte(role+"-password")) {
				t.Fatalf("migration altered %s: %+v %v", role, user, err)
			}
		}
		if _, _, err := q.SessionByHash([]byte("existing-session-secret")); err != nil {
			t.Fatalf("migration invalidated session: %v", err)
		}
		pairing, err := q.PairingStatus("existing-grant", "user", "existing-session")
		if err != nil || pairing.Status != "pending" {
			t.Fatalf("migration invalidated grant: %+v %v", pairing, err)
		}
		if err := upgraded.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPasswordPolicyResetAndSelfChangeAreAtomic(t *testing.T) {
	db, q := passwordPolicyDB(t)
	user := User{ID: "regular", Username: "regular", Role: "user", PasswordHash: []byte("original-hash")}
	if err := q.CreateUser(user, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"browser", "phone"} {
		passwordPolicySession(t, q, user, id)
	}
	if err := q.CreateTrackedPairing("grant", []byte("grant-code"), user.ID, "browser", time.Now().Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	resetHash := []byte("reset-hash")
	if err := q.UpdateUser(user.ID, nil, resetHash); err != nil {
		t.Fatal(err)
	}
	reset, err := q.UserByID(user.ID)
	if err != nil || !reset.MustChangePassword || !bytes.Equal(reset.PasswordHash, resetHash) {
		t.Fatalf("reset did not flag account: %+v %v", reset, err)
	}
	for _, table := range []string{"sessions", "pairings"} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id=?`, user.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("reset retained %s: %d %v", table, count, err)
		}
	}
	// A request authenticated before the reset must not overwrite that newer
	// credential, clear its flag, or revoke a newly issued restricted session.
	passwordPolicySession(t, q, *reset, "restricted")
	if err := q.ChangePassword(user.ID, user.PasswordHash, []byte("stale-self-change")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale change accepted: %v", err)
	}
	after, err := q.UserByID(user.ID)
	if err != nil || !after.MustChangePassword || !bytes.Equal(after.PasswordHash, resetHash) {
		t.Fatalf("stale change altered reset: %+v %v", after, err)
	}
	if _, _, err := q.SessionByHash([]byte("restricted-secret")); err != nil {
		t.Fatalf("failed stale change revoked new session: %v", err)
	}
	if err := q.CreateTrackedPairing("forbidden", []byte("forbidden-code"), user.ID, "restricted", time.Now().Add(time.Minute), ""); err == nil {
		t.Fatal("restricted account minted a pairing directly")
	}
	if err := q.ChangePassword(user.ID, resetHash, []byte("personal-hash")); err != nil {
		t.Fatal(err)
	}
	after, err = q.UserByID(user.ID)
	if err != nil || after.MustChangePassword || !bytes.Equal(after.PasswordHash, []byte("personal-hash")) {
		t.Fatalf("self-change failed to clear restriction: %+v %v", after, err)
	}
	if _, _, err := q.SessionByHash([]byte("restricted-secret")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("self-change retained session: %v", err)
	}
	passwordPolicySession(t, q, *after, "fresh")
	if err := q.CreateTrackedPairing("allowed", []byte("allowed-code"), user.ID, "fresh", time.Now().Add(time.Minute), ""); err != nil {
		t.Fatalf("self-change did not restore pairing: %v", err)
	}
}

func TestPasswordPolicyConcurrentResetCannotBeOverwrittenByStaleChange(t *testing.T) {
	_, q := passwordPolicyDB(t)
	user := User{ID: "regular", Username: "regular", Role: "user", PasswordHash: []byte("original-hash")}
	if err := q.CreateUser(user, false); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		results <- q.ChangePassword(user.ID, user.PasswordHash, []byte("personal-hash"))
	}()
	go func() { defer wg.Done(); <-start; results <- q.UpdateUser(user.ID, nil, []byte("reset-hash")) }()
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
	}
	after, err := q.UserByID(user.ID)
	if err != nil || !after.MustChangePassword || !bytes.Equal(after.PasswordHash, []byte("reset-hash")) {
		t.Fatalf("stale password change overrode administrator reset: %+v %v", after, err)
	}
}

func TestPasswordPolicyAdministratorExemptButCannotPair(t *testing.T) {
	_, q := passwordPolicyDB(t)
	admin := User{ID: "admin", Username: "admin", Role: "admin", PasswordHash: []byte("original-hash"), MustChangePassword: true}
	if err := q.CreateUser(admin, false); err != nil {
		t.Fatal(err)
	}
	found, err := q.UserByID(admin.ID)
	if err != nil || found.MustChangePassword {
		t.Fatalf("admin initial password restricted: %+v %v", found, err)
	}
	passwordPolicySession(t, q, *found, "admin-session")
	if err := q.CreateTrackedPairing("forbidden", []byte("forbidden-code"), admin.ID, "admin-session", time.Now().Add(time.Minute), ""); err == nil {
		t.Fatal("administrator minted pairing directly")
	}
	if err := q.UpdateUser(admin.ID, nil, []byte("reset-hash")); err != nil {
		t.Fatal(err)
	}
	found, err = q.UserByID(admin.ID)
	if err != nil || found.MustChangePassword {
		t.Fatalf("admin reset required forced password change: %+v %v", found, err)
	}
	if err := q.ChangePassword(admin.ID, []byte("reset-hash"), []byte("personal-hash")); err != nil {
		t.Fatal(err)
	}
	found, err = q.UserByID(admin.ID)
	if err != nil || found.MustChangePassword {
		t.Fatalf("admin self-change restricted: %+v %v", found, err)
	}
}
