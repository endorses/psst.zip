package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAbuseContactMailboxValidation(t *testing.T) {
	for _, valid := range []string{"", "abuse@example.com", "Abuse.Team+reports_42%tag@example-domain.test", strings.Repeat("a", 64) + "@" + strings.Repeat("d", 63) + "." + strings.Repeat("e", 63) + "." + strings.Repeat("f", 61)} {
		if !ValidAbuseContactEmail(valid) {
			t.Fatalf("valid address rejected (%d bytes)", len(valid))
		}
	}
	for _, invalid := range []string{" abuse@example.com", "abuse@example.com ", "abuse\r\nBcc:victim@example.com", "abuse\x00@example.com", "mailto:abuse@example.com", "a@example.com?subject=test", "a@example.com#key", "Display <a@example.com>", "a@example.com,b@example.com", "a@@example.com", "@example.com", "a@localhost", ".a@example.com", "a.@example.com", "a..b@example.com", "ü@example.com", "a@éxample.com", "a@example..com", "a@-example.com", "a@example-.com", "a@.example.com", "a@example.com.", "a:b@example.com", "a&b@example.com", strings.Repeat("a", 65) + "@example.com", "a@" + strings.Repeat("a", 64) + ".com", strings.Repeat("a", 64) + "@" + strings.Repeat("d", 63) + "." + strings.Repeat("e", 63) + "." + strings.Repeat("f", 62)} {
		if ValidAbuseContactEmail(invalid) {
			t.Fatalf("invalid address accepted (%d bytes)", len(invalid))
		}
	}
}
func TestAbuseContactPersistenceAuditAndAtomicFailure(t *testing.T) {
	q, u, path := adminSecurityFixture(t)
	session := adminTestSession(t, q, u, "contact-admin", time.Now(), "", "")
	actor := &AdminActor{UserID: u.ID, SessionID: session.ID}
	value, err := q.AbuseContactEmail()
	if err != nil || value != "" {
		t.Fatal(value, err)
	}
	if err = q.SetAbuseContactEmail("abuse@example.com", actor); err != nil {
		t.Fatal(err)
	}
	if err = q.SetAbuseContactEmail("abuse@example.com", actor); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err = NewQueries(db).AbuseContactEmail()
	db.Close()
	if err != nil || value != "abuse@example.com" {
		t.Fatal(value, err)
	}
	page, err := q.SecurityEvents(0, 100, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	for _, e := range page.Events {
		if e.Kind == AbuseContactChangedEvent {
			events++
		}
	}
	if events != 1 {
		t.Fatal("duplicate/no audit", events)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "abuse@example.com") {
		t.Fatal("address leaked into audit")
	}
	if _, err = q.db.Exec(`CREATE TRIGGER fail_contact_audit BEFORE INSERT ON security_events WHEN NEW.kind='settings.abuse_contact_changed' BEGIN SELECT RAISE(ABORT,'unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err = q.SetAbuseContactEmail("", actor); err == nil {
		t.Fatal("contact change bypassed audit")
	}
	value, _ = q.AbuseContactEmail()
	if value != "abuse@example.com" {
		t.Fatal("failed audit changed public contact")
	}
	if _, err = q.db.Exec(`DROP TRIGGER fail_contact_audit`); err != nil {
		t.Fatal(err)
	}
	if _, err = q.db.Exec(`UPDATE sessions SET recent_until=0 WHERE id=?`, session.ID); err != nil {
		t.Fatal(err)
	}
	if err = q.SetAbuseContactEmail("", actor); !errors.Is(err, ErrAdminRecentRequired) {
		t.Fatal("stale administrator mutated contact", err)
	}
	if _, err = q.db.Exec(`UPDATE sessions SET recent_until=? WHERE id=?`, time.Now().Add(time.Minute).Unix(), session.ID); err != nil {
		t.Fatal(err)
	}
	if err = q.SetAbuseContactEmail("", actor); err != nil {
		t.Fatal(err)
	}
	value, _ = q.AbuseContactEmail()
	if value != "" {
		t.Fatal("disable not persisted")
	}
}

func TestAbuseContactUpgradePreservesBoundedAuditAndSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	old, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for version, migration := range migrations {
		if strings.Contains(migration, "CREATE TABLE abuse_contact") {
			break
		}
		if _, err = old.Exec(migration); err != nil {
			t.Fatal(err)
		}
		if version > 0 {
			if _, err = old.Exec(`INSERT INTO schema_migrations(version)VALUES(?)`, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := NewQueries(old)
	for _, e := range []SecurityEvent{auditEvent("transfers.paused", "local"), auditEvent("transfer.revoked", "capability"), auditEvent("authentication.login_rejected", "system")} {
		if err = q.RecordSecurityEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	if err = q.RecordSecurityEvent(auditEvent(AbuseContactChangedEvent, "administrator")); err == nil {
		t.Fatal("old audit constraint already accepts new kind; upgrade test invalid")
	}
	original, err := q.SecurityEvents(0, 100, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.Exec(`UPDATE sqlite_sequence SET seq=999 WHERE name='security_events'`); err != nil {
		t.Fatal(err)
	}
	old.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q = NewQueries(db)
	after, err := q.SecurityEvents(0, 100, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(original.Events)
	afterJSON, _ := json.Marshal(after.Events)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("upgrade changed retained audit events")
	}
	for _, bucket := range []string{"administration", "lifecycle", "authentication"} {
		var n int
		if err = db.QueryRow(`SELECT retained FROM security_audit_buckets WHERE bucket=?`, bucket).Scan(&n); err != nil || n != 1 {
			t.Fatal(bucket, n, err)
		}
	}
	var indexes int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name IN ('security_events_age','security_events_bucket','security_events_target')`).Scan(&indexes); err != nil || indexes != 3 {
		t.Fatal("lost audit index", indexes, err)
	}
	if err = q.RecordSecurityEvent(auditEvent(AbuseContactChangedEvent, "administrator")); err != nil {
		t.Fatal(err)
	}
	after, _ = q.SecurityEvents(0, 100, time.Now())
	if after.Events[0].ID != 1000 {
		t.Fatal("audit cursor high-water mark lost", after.Events[0].ID)
	}
	if _, err = db.Exec(`UPDATE security_audit_buckets SET maximum=2 WHERE bucket='administration'`); err != nil {
		t.Fatal(err)
	}
	if err = q.RecordSecurityEvent(auditEvent(AbuseContactChangedEvent, "administrator")); err != nil {
		t.Fatal(err)
	}
	var rows, counter int
	if err = db.QueryRow(`SELECT COUNT(*) FROM security_events WHERE bucket='administration'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT retained FROM security_audit_buckets WHERE bucket='administration'`).Scan(&counter); err != nil || rows != 2 || counter != 2 {
		t.Fatal("upgrade lost partition pruning", rows, counter, err)
	}
}
