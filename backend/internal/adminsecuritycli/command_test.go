package adminsecuritycli

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestResetRequiresExplicitAccountAndConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	for _, args := range [][]string{nil, {"--username", "admin"}, {"--username", "admin", "--force"}, {"--username", " admin", "--confirm"}, {"--username", "admin", "--confirm", "extra"}} {
		if err := Run(path, args, &bytes.Buffer{}); err == nil {
			t.Fatal("accepted incomplete or ambiguous arguments")
		}
	}
	if err := Run(path, []string{"--username", "admin", "--confirm"}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted missing database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created a database during recovery")
	}
}

func TestResetRejectsWrongDatabaseWithoutMutation(t *testing.T) {
	for _, kind := range []string{"empty", "unrelated"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wrong.db")
			if kind == "empty" {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`CREATE TABLE unrelated(value TEXT); INSERT INTO unrelated VALUES('preserve')`); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := Run(path, []string{"--username", "admin", "--confirm"}, &out); err == nil || out.Len() > 0 {
				t.Fatal("accepted unsupported database or printed success")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("recovery modified unsupported database")
			}
		})
	}
}

func TestResetIsScopedToAdministratorAndPreservesPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := database.NewQueries(db)
	for _, user := range []database.User{
		{ID: "admin", Username: "admin", Role: "admin", PasswordHash: []byte("original")},
		{ID: "other", Username: "other", Role: "admin", PasswordHash: []byte("other-password")},
		{ID: "regular", Username: "regular", Role: "user", PasswordHash: []byte("regular-password")},
	} {
		if err := q.CreateUser(user, false); err != nil {
			t.Fatal(err)
		}
		session := database.Session{ID: user.ID + "-session", UserID: user.ID, DeviceName: "fixture", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
		if err := q.CreateSession(session, []byte(user.ID+"-token"), user.PasswordHash); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"unknown", "regular", "Admin"} {
		if err := Run(path, []string{"--username", name, "--confirm"}, &bytes.Buffer{}); err == nil {
			t.Fatal("reset non-administrator")
		}
	}
	if err := Run(path, []string{"--username", "admin", "--confirm"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.SessionByHash([]byte("admin-token")); err == nil {
		t.Fatal("administrator session survived reset")
	}
	for _, id := range []string{"other", "regular"} {
		if _, _, err := q.SessionByHash([]byte(id + "-token")); err != nil {
			t.Fatal("reset affected another account", err)
		}
	}
	admin, err := q.UserByID("admin")
	if err != nil || !bytes.Equal(admin.PasswordHash, []byte("original")) || admin.Disabled || admin.Role != "admin" {
		t.Fatal("reset changed password, role or disabled state")
	}
}

func TestEnrolledDisabledAdministratorResetClearsOnlyCredentialMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := database.NewQueries(db)
	u := database.User{ID: "admin", Username: "admin", Role: "admin", PasswordHash: []byte("original")}
	if err := q.CreateUser(u, false); err != nil {
		t.Fatal(err)
	}
	session := database.Session{ID: "session", UserID: u.ID, DeviceName: "fixture", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := q.CreateSession(session, []byte("token"), u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	// Fixture includes an old pairing grant that predates the administrator-only
	// boundary. Local recovery must also remove historical credential material.
	if _, err := db.Exec(`INSERT INTO pairings(code_hash,user_id,session_id,expires_at,id) VALUES(?,?,?,?,?)`, []byte("old-grant"), u.ID, session.ID, time.Now().Add(time.Hour), "pairing"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE admin_security SET secret='JBSWY3DPEHPK3PXP',last_counter=123,failures=5,locked_until=? WHERE user_id=?`, time.Now().Add(time.Hour).Unix(), u.ID); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("fixture-recovery-code"))
	if _, err := db.Exec(`INSERT INTO admin_recovery_codes(user_id,hash) VALUES(?,?)`, u.ID, hash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO admin_pending_factors(user_id,session_id,secret,expires_at,revision) VALUES(?,?,?,?,?)`, u.ID, session.ID, "pending-secret", time.Now().Add(time.Hour).Unix(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET disabled=1 WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.SetTransfersPaused(true); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(path, []string{"--username", "admin", "--confirm"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"JBSWY3DPEHPK3PXP", "fixture-recovery-code", "pending-secret", "original"} {
		if bytes.Contains(out.Bytes(), []byte(secret)) {
			t.Fatal("command output contains credential material")
		}
	}
	security, err := q.AdminSecurity(u.ID)
	if err != nil || security.Secret != "" || security.LastCounter != -1 || security.Revision != 2 || security.Failures != 0 || security.LockedUntil != 0 {
		t.Fatal("credential reset or revision change missing", err)
	}
	for _, table := range []string{"sessions", "pairings", "admin_recovery_codes", "admin_pending_factors"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE user_id=?`, u.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained credential state", table)
		}
	}
	admin, err := q.UserByID(u.ID)
	if err != nil || !admin.Disabled || admin.Role != "admin" || !bytes.Equal(admin.PasswordHash, u.PasswordHash) {
		t.Fatal("reset altered disabled state, role or password")
	}
	incident, err := q.IncidentState()
	if err != nil || !incident.PublicTransfersPaused {
		t.Fatal("reset changed transfer pause policy")
	}
}

func TestResetWithoutAuditStorageRemainsAvailableAndReportsGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := database.NewQueries(db)
	user := database.User{ID: "operator", Username: "operator", Role: "admin", PasswordHash: []byte("preserved-password")}
	if err := q.CreateUser(user, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE admin_security SET secret='fixture-secret' WHERE user_id='operator'; DROP TABLE security_events`); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(path, []string{"--username", "operator", "--confirm"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("may not have been recorded")) {
		t.Fatal("audit gap hidden", out.String())
	}
	if bytes.Contains(out.Bytes(), []byte("fixture-secret")) || bytes.Contains(out.Bytes(), user.PasswordHash) {
		t.Fatal("recovery leaked credential")
	}
	state, err := q.AdminSecurity(user.ID)
	if err != nil || state.Secret != "" {
		t.Fatal("recovery did not complete", err)
	}
}
