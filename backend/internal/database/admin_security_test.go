package database

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/hotp"
)

const administratorTestSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func adminSecurityFixture(t *testing.T) (*Queries, User, string) {
	t.Helper()
	q, path := resourceFixture(t)
	u := User{ID: "administrator", Username: "Administrator", Role: "admin", PasswordHash: []byte("test-password-hash")}
	if err := q.CreateUser(u, false); err != nil {
		t.Fatal(err)
	}
	return q, u, path
}
func adminTestSession(t *testing.T, q *Queries, u User, id string, now time.Time, code, recovery string) Session {
	t.Helper()
	state, err := q.AdminSecurity(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := Session{ID: id, UserID: u.ID, DeviceName: "Browser", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err = q.CreateAdminSession(s, []byte(id+"-hash"), u.PasswordHash, state.Revision, code, recovery, now); err != nil {
		t.Fatal(err)
	}
	return s
}
func adminTestCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := hotp.GenerateCode(secret, uint64(at.Unix()/30))
	if err != nil {
		t.Fatal(err)
	}
	return code
}
func enrollAdminTestFactor(t *testing.T, q *Queries, u User, now time.Time) []string {
	t.Helper()
	session := adminTestSession(t, q, u, "enrollment-session", now, "", "")
	state, _ := q.AdminSecurity(u.ID)
	if _, err := q.BeginAdminEnrollment(u.ID, session.ID, u.PasswordHash, state.Revision, administratorTestSecret, now); err != nil {
		t.Fatal(err)
	}
	codes, err := q.ConfirmAdminEnrollment(u.ID, session.ID, u.PasswordHash, state.Revision, adminTestCode(t, administratorTestSecret, now), now)
	if err != nil {
		t.Fatal(err)
	}
	return codes
}
func TestAdminTOTPVectorWindowAndCounterReplay(t *testing.T) {
	at := time.Unix(59, 0)
	counter, ok := adminTOTPCounter(administratorTestSecret, "287082", at, -1)
	if !ok || counter != 1 {
		t.Fatal(counter, ok)
	}
	if _, ok = adminTOTPCounter(administratorTestSecret, "287082", at, 1); ok {
		t.Fatal("replayed counter")
	}
	if _, ok = adminTOTPCounter(administratorTestSecret, "287082", time.Unix(90, 0), -1); ok {
		t.Fatal("accepted beyond skew")
	}
	if _, ok = adminTOTPCounter(administratorTestSecret, "287082", time.Unix(60, 0), -1); !ok {
		t.Fatal("one-step skew rejected")
	}
	for _, code := range []string{"287082 ", "28708", "0287082", "abcdef"} {
		if _, ok = adminTOTPCounter(administratorTestSecret, code, at, -1); ok {
			t.Fatal("malformed code accepted")
		}
	}
}
func TestAdminEnrollmentBoundedSessionAndRevocation(t *testing.T) {
	q, u, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	first := adminTestSession(t, q, u, "one", now, "", "")
	second := adminTestSession(t, q, u, "two", now, "", "")
	state, _ := q.AdminSecurity(u.ID)
	if _, err := q.BeginAdminEnrollment(u.ID, first.ID, u.PasswordHash, state.Revision, administratorTestSecret, now); err != nil {
		t.Fatal(err)
	}
	if _, err := q.BeginAdminEnrollment(u.ID, second.ID, u.PasswordHash, state.Revision, administratorTestSecret, now); !errors.Is(err, ErrAdminEnrollmentPending) {
		t.Fatal(err)
	}
	if err := q.CancelAdminEnrollment(u.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ConfirmAdminEnrollment(u.ID, second.ID, u.PasswordHash, state.Revision, adminTestCode(t, administratorTestSecret, now), now); !errors.Is(err, ErrAdminEnrollmentExpired) {
		t.Fatal(err)
	}
	codes, err := q.ConfirmAdminEnrollment(u.ID, first.ID, u.PasswordHash, state.Revision, adminTestCode(t, administratorTestSecret, now), now)
	if err != nil || len(codes) != 10 {
		t.Fatal(err, len(codes))
	}
	seen := map[string]bool{}
	for _, code := range codes {
		if len(code) != 22 || seen[code] {
			t.Fatal("invalid recovery entropy/uniqueness")
		}
		seen[code] = true
		var plaintext int
		if err = q.db.QueryRow(`SELECT count(*) FROM admin_recovery_codes WHERE CAST(hash AS TEXT)=?`, code).Scan(&plaintext); err != nil || plaintext != 0 {
			t.Fatal("recovery plaintext persisted", err)
		}
	}
	for _, table := range []string{"sessions", "admin_pending_factors"} {
		var n int
		if err = q.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	state, _ = q.AdminSecurity(u.ID)
	if state.Secret != administratorTestSecret || state.LastCounter != now.Unix()/30 {
		t.Fatal("factor state incorrect")
	}
	if err = q.CreateSession(Session{ID: "bypass", UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, []byte("bypass"), u.PasswordHash); err == nil {
		t.Fatal("legacy session insertion bypassed enabled factor")
	}
}
func TestAdminFactorConcurrentReplayAndRecoveryUse(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(fmt.Sprint(recovery), func(t *testing.T) {
			q, u, path := adminSecurityFixture(t)
			now := time.Now().UTC()
			codes := enrollAdminTestFactor(t, q, u, now)
			db, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer closeFixture(t, db)
			other := NewQueries(db)
			state, _ := q.AdminSecurity(u.ID)
			proofAt := now.Add(30 * time.Second)
			proof := adminTestCode(t, administratorTestSecret, proofAt)
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					writer := q
					if i == 1 {
						writer = other
					}
					code, backup := proof, ""
					if recovery {
						code = ""
						backup = codes[0]
					}
					id := fmt.Sprint("session", i)
					results <- writer.CreateAdminSession(Session{ID: id, UserID: u.ID, CreatedAt: proofAt, ExpiresAt: proofAt.Add(time.Hour)}, []byte(id), u.PasswordHash, state.Revision, code, backup, proofAt)
				}(i)
			}
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				} else if !errors.Is(err, ErrAdminFactorInvalid) {
					t.Fatal(err)
				}
			}
			if success != 1 {
				t.Fatalf("proof accepted %d times", success)
			}
		})
	}
}
func TestAdminFactorFailureCooldownPersistsAndExpires(t *testing.T) {
	q, u, path := adminSecurityFixture(t)
	now := time.Now().UTC()
	enrollAdminTestFactor(t, q, u, now)
	state, _ := q.AdminSecurity(u.ID)
	for i := 1; i <= 5; i++ {
		err := q.RecordAdminAuthenticationFailure(u.ID, u.PasswordHash, state.Revision, now)
		var locked *AdminAuthenticationLocked
		if i < 5 && !errors.Is(err, ErrAdminFactorInvalid) || i == 5 && !errors.As(err, &locked) {
			t.Fatalf("attempt%d: %v", i, err)
		}
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	other := NewQueries(db)
	stored, _ := other.AdminSecurity(u.ID)
	if stored.Failures != 5 || stored.LockedUntil != now.Add(5*time.Minute).Unix() {
		t.Fatal(stored.Failures, stored.LockedUntil)
	}
	err = other.RecordAdminAuthenticationFailure(u.ID, u.PasswordHash, state.Revision, now.Add(time.Minute))
	var locked *AdminAuthenticationLocked
	if !errors.As(err, &locked) || locked.RetryAt.Unix() != stored.LockedUntil {
		t.Fatal("lock extended or lost", err)
	}
	later := now.Add(5*time.Minute + time.Second)
	if err = other.RecordAdminAuthenticationFailure(u.ID, u.PasswordHash, state.Revision, later); !errors.Is(err, ErrAdminFactorInvalid) {
		t.Fatal(err)
	}
	stored, _ = other.AdminSecurity(u.ID)
	if stored.Failures != 1 || stored.LockedUntil != 0 {
		t.Fatal(stored.Failures, stored.LockedUntil)
	}
}
func TestAdminPendingExpiryAttemptsAndFreshness(t *testing.T) {
	q, u, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	session := adminTestSession(t, q, u, "pending", now, "", "")
	state, _ := q.AdminSecurity(u.ID)
	if _, err := q.BeginAdminEnrollment(u.ID, session.ID, u.PasswordHash, state.Revision, administratorTestSecret, now); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		_, _ = q.ConfirmAdminEnrollment(u.ID, session.ID, u.PasswordHash, state.Revision, "invalid", now)
	}
	var n int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM admin_pending_factors`).Scan(&n); err != nil || n != 0 {
		t.Fatal("pending attempt cap", n, err)
	}
	later := now.Add(6 * time.Minute)
	if _, err := q.ReauthenticateAdmin(u.ID, session.ID, u.PasswordHash, state.Revision, "", "", later); err != nil {
		t.Fatal(err)
	}
	if _, err := q.BeginAdminEnrollment(u.ID, session.ID, u.PasswordHash, state.Revision, administratorTestSecret, later); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE sessions SET recent_until=? WHERE id=?`, later.Add(time.Hour).Unix(), session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ConfirmAdminEnrollment(u.ID, session.ID, u.PasswordHash, state.Revision, "123456", later.Add(AdminEnrollmentDuration)); !errors.Is(err, ErrAdminEnrollmentExpired) {
		t.Fatal(err)
	}
	if err := q.RequireRecentAdmin(u.ID, session.ID, later.Add(2*time.Hour)); !errors.Is(err, ErrAdminAuthenticationChanged) {
		t.Fatal(err)
	}
}
func TestAdminFactorChangesRejectStaleProofAndPreservePasswordResetFactor(t *testing.T) {
	q, u, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	codes := enrollAdminTestFactor(t, q, u, now)
	session := adminTestSession(t, q, u, "fresh", now, "", codes[0])
	before, _ := q.AdminSecurity(u.ID)
	renewed, err := q.ChangeAdminFactor(u.ID, session.ID, u.PasswordHash, before.Revision, false, now)
	if err != nil || len(renewed) != 10 {
		t.Fatal(err)
	}
	if _, err = q.ReauthenticateAdmin(u.ID, session.ID, u.PasswordHash, before.Revision, "", codes[1], now); !errors.Is(err, ErrAdminAuthenticationChanged) {
		t.Fatal("stale reauth succeeded", err)
	}
	stale := Session{ID: "stale-login", UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err = q.CreateAdminSession(stale, []byte("stale"), u.PasswordHash, before.Revision, "", codes[1], now); !errors.Is(err, ErrAdminAuthenticationChanged) {
		t.Fatal("stale login succeeded", err)
	}
	after, _ := q.AdminSecurity(u.ID)
	if after.Secret != before.Secret || after.LastCounter != before.LastCounter {
		t.Fatal("regeneration reset TOTP state")
	}
	if err = q.UpdateUser(u.ID, nil, []byte("new-password")); err != nil {
		t.Fatal(err)
	}
	reset, _ := q.AdminSecurity(u.ID)
	if reset.Secret != before.Secret || reset.Revision <= after.Revision {
		t.Fatal("password reset removed factor or revision")
	}
	if err = q.CreateAdminSession(stale, []byte("stale"), u.PasswordHash, reset.Revision, "", renewed[0], now); !errors.Is(err, ErrAdminAuthenticationChanged) {
		t.Fatal("old password snapshot accepted", err)
	}
}
func TestAdminSensitiveAuthMutationsRejectRevokedActor(t *testing.T) {
	q, u, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	session := adminTestSession(t, q, u, "actor", now, "", "")
	actor := &AdminActor{u.ID, session.ID}
	if err := q.ResetAdminFactor(u.Username); err != nil {
		t.Fatal(err)
	}
	target := User{ID: "target", Username: "target", Role: "user", PasswordHash: []byte("password")}
	if err := q.CreateUser(target, false, actor); !errors.Is(err, ErrAdminAuthenticationChanged) {
		t.Fatal("revoked actor createduser", err)
	}
	if err := q.CreateUser(target, false); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateUser(target.ID, nil, []byte("changed"), actor); !errors.Is(err, ErrAdminAuthenticationChanged) {
		t.Fatal("revoked actor resetuser", err)
	}
	if err := q.DeleteSession("anything", u.ID, actor); !errors.Is(err, ErrAdminAuthenticationChanged) {
		t.Fatal("revoked actor mutatedsessions", err)
	}
	if err := q.ChangeAdminPassword(u.ID, session.ID, u.PasswordHash, []byte("changed")); !errors.Is(err, ErrAdminAuthenticationChanged) {
		t.Fatal("revoked session changedpassword", err)
	}
	current, _ := q.UserByID(u.ID)
	if !bytes.Equal(current.PasswordHash, u.PasswordHash) {
		t.Fatal("password changed")
	}
}
func TestAdminSecurityMigrationLeavesOldSessionsNotRecent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, migration := range migrations {
		if strings.Contains(migration, "ADD COLUMN recent_until") {
			break
		}
		if _, err = old.Exec(migration); err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			if _, err = old.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = old.Exec(`INSERT INTO users(id,username,role,password_hash) VALUES('admin','admin','admin',?)`, []byte("hash")); err != nil {
		t.Fatal(err)
	}
	if _, err = old.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES('old','admin',?,'Browser',?,?)`, []byte("token"), time.Now().UTC(), time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
	closeFixture(t, old)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	q := NewQueries(db)
	metadata, err := q.AdminSecurityMetadata("admin", "old", time.Now())
	if err != nil || metadata.Enabled || metadata.RecentUntil != nil {
		t.Fatal(metadata, err)
	}
	if err = q.RequireRecentAdmin("admin", "old", time.Now()); !errors.Is(err, ErrAdminRecentRequired) {
		t.Fatal(err)
	}
}
func TestDatabaseCredentialFilesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.db")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatal(suffix, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s permissions %o", suffix, info.Mode().Perm())
		}
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path+"-wal", 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path+"-shm", 0644); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, other)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, _ := os.Stat(path + suffix)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("existing%s permissions %o", suffix, info.Mode().Perm())
		}
	}
}

func TestDatabaseCredentialPathMetacharactersAreLiteral(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file:credentials?mode=memory#fragment.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = NewQueries(db).CreateUser(User{ID: "admin", Username: "admin", Role: "admin", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	closeFixture(t, db)
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, reopened)
	if n, err := NewQueries(reopened).UserCount(); err != nil || n != 1 {
		t.Fatal("opened different database", n, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) && entry.Name() != filepath.Base(path)+"-wal" && entry.Name() != filepath.Base(path)+"-shm" {
			t.Fatal("unexpected database file", entry.Name())
		}
	}
}
