package database

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func denyAuditWrites(t *testing.T, q *Queries) {
	t.Helper()
	if _, err := q.db.Exec(`CREATE TRIGGER fail_auth_audit BEFORE INSERT ON security_events BEGIN SELECT RAISE(FAIL,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticationAuditPreservesAdminRecoveryButRollsBackChanges(t *testing.T) {
	q, u, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	codes := enrollAdminTestFactor(t, q, u, now)
	denyAuditWrites(t, q)
	// Recovery sign-in consumes a code and creates the session even if its audit
	// insert fails. Operator recovery cannot depend on audit storage availability.
	session := adminTestSession(t, q, u, "recovery-session", now, "", codes[0])
	if !q.SecurityAuditDegraded() {
		t.Fatal("audit failure not surfaced")
	}
	var remaining int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM admin_recovery_codes WHERE user_id=?`, u.ID).Scan(&remaining); err != nil || remaining != 9 {
		t.Fatal(remaining, err)
	}
	actor := &AdminActor{UserID: u.ID, SessionID: session.ID}
	created := User{ID: "new-account", Username: "New-account", Role: "user", PasswordHash: []byte("new-password")}
	if err := q.CreateUser(created, false, actor); err == nil {
		t.Fatal("unaudited account grant committed")
	}
	if _, err := q.UserByID(created.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("grant not rolled back", err)
	}
	if err := q.ChangeAdminPassword(u.ID, session.ID, u.PasswordHash, []byte("replacement-hash")); err == nil {
		t.Fatal("unaudited password change committed")
	}
	unchanged, err := q.UserByID(u.ID)
	if err != nil || !bytes.Equal(unchanged.PasswordHash, u.PasswordHash) {
		t.Fatal("password not rolled back", err)
	}
	if _, _, err := q.SessionByHash([]byte(session.ID + "-hash")); err != nil {
		t.Fatal("password rollback lost session", err)
	}
	state, _ := q.AdminSecurity(u.ID)
	if _, err := q.ChangeAdminFactor(u.ID, session.ID, u.PasswordHash, state.Revision, false, now); err == nil {
		t.Fatal("unaudited recovery rotation committed")
	}
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM admin_recovery_codes WHERE user_id=?`, u.ID).Scan(&remaining); err != nil || remaining != 9 {
		t.Fatal("recovery codes not rolled back", remaining, err)
	}
	if err := q.ResetAdminFactor(u.Username); err != nil {
		t.Fatal("audit failure blocked local reset", err)
	}
	state, _ = q.AdminSecurity(u.ID)
	if state.Secret != "" {
		t.Fatal("reset did not remove factor")
	}
	if _, _, err := q.SessionByHash([]byte(session.ID + "-hash")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reset retained session", err)
	}
}

func TestAuthenticationAuditDenialAndLogoutRemainAvailable(t *testing.T) {
	q, admin, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	session := adminTestSession(t, q, admin, "admin-session", now, "", "")
	u := User{ID: "regular", Username: "Regular", Role: "user", PasswordHash: []byte("regular-password")}
	if err := q.CreateUser(u, false); err != nil {
		t.Fatal(err)
	}
	guest := Session{ID: "regular-session", UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := q.CreateSession(guest, []byte("session-token"), u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	denyAuditWrites(t, q)
	disabled := true
	if err := q.UpdateUser(u.ID, &disabled, nil, &AdminActor{UserID: admin.ID, SessionID: session.ID}); err != nil {
		t.Fatal("audit failure blocked denial", err)
	}
	stored, err := q.UserByID(u.ID)
	if err != nil || !stored.Disabled {
		t.Fatal(stored, err)
	}
	if err := q.DeleteAccountSession(session.ID, admin.ID); err != nil {
		t.Fatal("audit failure blocked logout", err)
	}
	if _, _, err := q.SessionByHash([]byte(session.ID + "-hash")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("logout failed", err)
	}
	if !q.SecurityAuditDegraded() {
		t.Fatal("audit gap hidden")
	}
}

func TestAuthenticationAuditOnlyRecordsRealSessionRevocationAndNoSecrets(t *testing.T) {
	q, u, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	session := adminTestSession(t, q, u, "actual-session", now, "", "")
	if err := q.DeleteAccountSession("nonexistent-session", u.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteAccountSession(session.ID, "other-user"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM security_events WHERE kind='session.revoked'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := q.DeleteAccountSession(session.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteAccountSession(session.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM security_events WHERE kind='session.revoked'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	page, err := q.SecurityEvents(0, 100, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range page.Events {
		serialized := event.Kind + event.Origin + event.ActorID + event.TargetID + event.TargetType
		for _, secret := range []string{u.Username, string(u.PasswordHash), "actual-session-hash", administratorTestSecret} {
			if strings.Contains(serialized, secret) {
				t.Fatal("audit contains secret or account name", event)
			}
		}
	}
}

func TestAuthenticationAuditEnrollmentRollbackAndPreAuditRecovery(t *testing.T) {
	q, u, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	session := adminTestSession(t, q, u, "setup-session", now, "", "")
	state, _ := q.AdminSecurity(u.ID)
	if _, err := q.BeginAdminEnrollment(u.ID, session.ID, u.PasswordHash, state.Revision, administratorTestSecret, now); err != nil {
		t.Fatal(err)
	}
	denyAuditWrites(t, q)
	if _, err := q.ConfirmAdminEnrollment(u.ID, session.ID, u.PasswordHash, state.Revision, adminTestCode(t, administratorTestSecret, now), now); err == nil {
		t.Fatal("unaudited enrollment committed")
	}
	unchanged, _ := q.AdminSecurity(u.ID)
	if unchanged.Secret != "" || unchanged.Revision != state.Revision {
		t.Fatal("enrollment not rolled back")
	}
	if _, _, err := q.SessionByHash([]byte(session.ID + "-hash")); err != nil {
		t.Fatal("enrollment failure revoked session", err)
	}
	if _, err := q.db.Exec(`DROP TABLE security_events`); err != nil {
		t.Fatal(err)
	}
	if err := q.ResetAdminFactor(u.Username); err != nil {
		t.Fatal("pre-audit recovery blocked", err)
	}
}

func TestAuthenticationAuditRecordsOnlyAccountStateTransitions(t *testing.T) {
	q, admin, _ := adminSecurityFixture(t)
	now := time.Now().UTC()
	session := adminTestSession(t, q, admin, "control-session", now, "", "")
	user := User{ID: "target", Username: "Target", Role: "user", PasswordHash: []byte("password")}
	if err := q.CreateUser(user, false); err != nil {
		t.Fatal(err)
	}
	for _, disabled := range []bool{false, true, true, false, false} {
		if err := q.UpdateUser(user.ID, &disabled, nil, &AdminActor{UserID: admin.ID, SessionID: session.ID}); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"account.disabled", "account.enabled"} {
		var count int
		if err := q.db.QueryRow(`SELECT COUNT(*) FROM security_events WHERE kind=? AND target_id=?`, kind, user.ID).Scan(&count); err != nil || count != 1 {
			t.Fatal(kind, count, err)
		}
	}
}

func TestAuthenticationAuditPairingReplacementRecordsAtomicRevocation(t *testing.T) {
	q, _ := resourceFixture(t)
	user := User{ID: "owner", Username: "Owner", Role: "user", PasswordHash: []byte("password")}
	if err := q.CreateUser(user, false); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := Session{ID: "parent", UserID: user.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := q.CreateSession(session, []byte("session-hash"), user.PasswordHash); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTrackedPairing("old", []byte("old-hash"), user.ID, session.ID, now.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	// Duplicate replacement ID fails after cancellation; both cancellation and
	// its audit record must roll back with the unsuccessful new grant.
	if err := q.CreateTrackedPairing("old", []byte("replacement-hash"), user.ID, session.ID, now.Add(time.Minute), "old"); err == nil {
		t.Fatal("duplicate grant accepted")
	}
	status, err := q.PairingStatus("old", user.ID, session.ID)
	if err != nil || status.Status != "pending" {
		t.Fatal("failed replacement canceled grant", status, err)
	}
	var count int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM security_events WHERE kind='pairing.revoked'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed replacement audit survived", count, err)
	}
	if err := q.CreateTrackedPairing("new", []byte("new-hash"), user.ID, session.ID, now.Add(time.Minute), "old"); err != nil {
		t.Fatal(err)
	}
	status, err = q.PairingStatus("old", user.ID, session.ID)
	if err != nil || status.Status != "canceled" {
		t.Fatal(status, err)
	}
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM security_events WHERE kind='pairing.revoked' AND target_id='old' AND actor_id='owner' AND origin='account'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("replacement revocation missing", count, err)
	}
	// Replacing an already-canceled flow does not report another revocation.
	if err := q.CreateTrackedPairing("another", []byte("another-hash"), user.ID, session.ID, now.Add(time.Minute), "old"); err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM security_events WHERE kind='pairing.revoked'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate revocation", count, err)
	}
}
