package database

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func auditEvent(kind, origin string) SecurityEvent {
	return SecurityEvent{Kind: kind, Origin: origin, TargetType: "server", Outcome: "succeeded"}
}

func TestSecurityAuditBucketsProtectAdministration(t *testing.T) {
	q, _ := resourceFixture(t)
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, group := range []struct {
		kind, origin string
		total        int
	}{{"settings.file_size_changed", "administrator", 8005}, {"transfer.revoked", "capability", 1105}, {"authentication.login_rejected", "system", 1105}} {
		for i := 0; i < group.total; i++ {
			if err = q.AppendSecurityEvent(tx, auditEvent(group.kind, group.origin)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for bucket, want := range map[string]int{"administration": 8000, "lifecycle": 1000, "authentication": 1000} {
		var actual, retained int
		if err = q.db.QueryRow(`SELECT COUNT(*) FROM security_events WHERE bucket=?`, bucket).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if err = q.db.QueryRow(`SELECT retained FROM security_audit_buckets WHERE bucket=?`, bucket).Scan(&retained); err != nil {
			t.Fatal(err)
		}
		if actual != want || retained != want {
			t.Fatalf("%s rows=%d counter=%d want=%d", bucket, actual, retained, want)
		}
	}
}

func TestSecurityAuditPrivacyValidationAndCursor(t *testing.T) {
	q, _ := resourceFixture(t)
	for _, change := range []func(*SecurityEvent){func(e *SecurityEvent) { e.Kind = "secret-token" }, func(e *SecurityEvent) { e.ActorID = "cookie=value" }, func(e *SecurityEvent) { e.TargetID = "https://host/u#key" }, func(e *SecurityEvent) { e.Origin = "User-Agent" }, func(e *SecurityEvent) { e.TargetType = "filename" }, func(e *SecurityEvent) { e.Outcome = "raw error" }, func(e *SecurityEvent) { e.Count = -1 }, func(e *SecurityEvent) { e.Count = 1000000001 }} {
		e := auditEvent("transfers.paused", "local")
		change(&e)
		if err := q.RecordSecurityEvent(e); err == nil {
			t.Fatalf("accepted %+v", e)
		}
	}
	for i := 0; i < 7; i++ {
		e := auditEvent("transfers.paused", "local")
		e.ID = 987
		e.OccurredAt = "secret-ignored-timestamp"
		if err := q.RecordSecurityEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	before := int64(0)
	seen := map[int64]bool{}
	for {
		p, err := q.SecurityEvents(before, 3, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Events) > 3 {
			t.Fatal("unbounded response")
		}
		for _, e := range p.Events {
			if seen[e.ID] || e.ID == 987 || strings.Contains(e.OccurredAt, "secret") {
				t.Fatalf("invalid event %+v", e)
			}
			seen[e.ID] = true
		}
		if p.NextBefore == nil {
			break
		}
		before = *p.NextBefore
	}
	if len(seen) != 7 {
		t.Fatal(seen)
	}
	for _, input := range []struct {
		before int64
		limit  int
	}{{-1, 3}, {0, 0}, {0, 101}} {
		if _, err := q.SecurityEvents(input.before, input.limit, time.Now()); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	p, _ := q.SecurityEvents(0, 100, time.Now())
	raw, _ := json.Marshal(p)
	for _, secret := range []string{"cookie=", "https://", "secret-token", "secret-ignored", "User-Agent", "filename"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("audit leaked rejected input")
		}
	}
}

func TestSecurityAuditAgePruningBoundedAndRefills(t *testing.T) {
	q, _ := resourceFixture(t)
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		if err = q.AppendSecurityEvent(tx, auditEvent("transfers.paused", "local")); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -91).UTC().Format(securityAuditTimeLayout)
	if _, err = q.db.Exec(`UPDATE security_events SET occurred_at=?`, old); err != nil {
		t.Fatal(err)
	}
	p, err := q.SecurityEvents(0, 100, time.Now())
	if err != nil || len(p.Events) != 0 {
		t.Fatalf("expired rows returned %+v %v", p, err)
	}
	n, err := q.PruneSecurityEvents(time.Now())
	if err != nil || n != 256 {
		t.Fatal(n, err)
	}
	if err = q.RecordSecurityEvent(auditEvent("transfers.resumed", "local")); err != nil {
		t.Fatal(err)
	}
	n, err = q.PruneSecurityEvents(time.Now())
	if err != nil || n != 88 {
		t.Fatal(n, err)
	}
	p, err = q.SecurityEvents(0, 100, time.Now())
	if err != nil || len(p.Events) != 1 || p.Events[0].Kind != "transfers.resumed" {
		t.Fatal(p, err)
	}
	var retained int
	if err = q.db.QueryRow(`SELECT retained FROM security_audit_buckets WHERE bucket='administration'`).Scan(&retained); err != nil || retained != 1 {
		t.Fatal(retained, err)
	}
}

func TestSecurityAuditStrictMutationRollbackAndRecovery(t *testing.T) {
	q, u, _ := adminSecurityFixture(t)
	session := adminTestSession(t, q, u, "audit-session", time.Now(), "", "")
	actor := &AdminActor{UserID: u.ID, SessionID: session.ID}
	if err := q.SetMaxFileSize(1234, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`CREATE TRIGGER audit_unavailable BEFORE INSERT ON security_events BEGIN SELECT RAISE(ABORT,'storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := q.SetMaxFileSize(1234, actor); err != nil {
		t.Fatal("unchanged setting attempted a new event", err)
	}
	if err := q.SetMaxFileSize(9876, actor); err == nil {
		t.Fatal("mutation succeeded without audit")
	}
	size, err := q.MaxFileSize()
	if err != nil || size != 1234 {
		t.Fatal(size, err)
	}
	if err = q.SetTransfersPaused(true, actor); err != nil {
		t.Fatal("emergency pause blocked", err)
	}
	if err = q.SetTransfersPaused(false, actor); err == nil {
		t.Fatal("remote resume bypassed audit")
	}
	if err = q.SetTransfersPausedLocal(false); err != nil {
		t.Fatal("local recovery blocked", err)
	}
	if !q.SecurityAuditDegraded() {
		t.Fatal("audit loss invisible")
	}
	if _, err = q.db.Exec(`DROP TRIGGER audit_unavailable`); err != nil {
		t.Fatal(err)
	}
	if err = q.SetTransfersPausedLocal(true); err != nil {
		t.Fatal(err)
	}
	p, err := q.SecurityEvents(0, 100, time.Now())
	if err != nil || !p.Degraded {
		t.Fatal(p, err)
	}
	if p.Events[0].Origin != "local" || p.Events[0].ActorID != "" {
		t.Fatal("local actor misattributed")
	}
}

func TestSecurityAuditRevocationOnlyActualExplicitTransitions(t *testing.T) {
	q, _ := resourceFixture(t)
	for _, id := range []string{"explicit", "internal", "failure"} {
		if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	e := SecurityEvent{Origin: "capability"}
	if err := q.RevokeTransferAudited("explicit", e); err != nil {
		t.Fatal(err)
	}
	if err := q.RevokeTransferAudited("explicit", e); err != nil {
		t.Fatal(err)
	}
	if err := q.RevokeTransfer("internal"); err != nil {
		t.Fatal(err)
	}
	p, err := q.SecurityEvents(0, 100, time.Now())
	if err != nil || len(p.Events) != 1 || p.Events[0].TargetID != "explicit" {
		t.Fatal(p, err)
	}
	if _, err = q.db.Exec(`DROP TABLE security_events`); err != nil {
		t.Fatal(err)
	}
	if err = q.RevokeTransferAudited("failure", e); err != nil {
		t.Fatal("audit blocked revocation", err)
	}
	transfer, err := q.GetTransfer("failure")
	if err != nil || transfer.Status != "revoked" || !q.SecurityAuditDegraded() {
		t.Fatal(transfer, err)
	}
}

func TestSecurityAuditSummaryBatchIsAtomicAndCancelable(t *testing.T) {
	q, _ := resourceFixture(t)
	good := auditEvent("authentication.login_rejected", "system")
	good.TargetType = "authentication"
	good.Outcome = "rejected"
	good.Count = 500
	bad := good
	bad.Kind = "authentication.unknown"
	if err := q.RecordSecurityEvents(context.Background(), []SecurityEvent{good, bad}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	p, _ := q.SecurityEvents(0, 100, time.Now())
	if len(p.Events) != 0 {
		t.Fatal("partial batch committed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := q.RecordSecurityEvents(ctx, []SecurityEvent{good}); err == nil {
		t.Fatal("canceled batch accepted")
	}
	if err := q.RecordSecurityEvents(context.Background(), []SecurityEvent{good}); err != nil {
		t.Fatal(err)
	}
	p, _ = q.SecurityEvents(0, 100, time.Now())
	if len(p.Events) != 1 || p.Events[0].Count != 500 {
		t.Fatal(p)
	}
}
