package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const SecurityAuditRetentionDays = 90
const SecurityAuditMaxEvents = 10000
const securityAuditPruneBatch = 256
const securityAuditTimeLayout = "2006-01-02T15:04:05.000000000Z"

// Only enumerated actions and opaque internal identifiers belong here. There is
// intentionally no free-form message, request metadata or arbitrary JSON field.
type SecurityEvent struct {
	ID         int64  `json:"id"`
	OccurredAt string `json:"occurred_at"`
	Kind       string `json:"kind"`
	Origin     string `json:"origin"`
	ActorID    string `json:"actor_id,omitempty"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id,omitempty"`
	Outcome    string `json:"outcome"`
	Count      int64  `json:"count"`
}

var securityEventKinds = []string{
	"settings.file_size_changed", "settings.resource_policy_changed", "settings.traffic_policy_changed", "settings.traffic_chart_changed", "settings.account_traffic_changed",
	"transfers.paused", "transfers.resumed", "account.created", "account.enabled", "account.disabled", "account.shutdown", "account.password_changed", "account.password_reset",
	"session.revoked", "pairing.created", "pairing.redeemed", "pairing.revoked", "administrator.signed_in", "administrator.reauthenticated", "administrator.factor_enabled", "administrator.factor_disabled", "administrator.factor_reset", "administrator.recovery_rotated", "administrator.recovery_used",
	"transfer.revoked", "slot.revoked", "authentication.login_rejected", "authentication.pairing_rejected", "authentication.factor_rejected", "authentication.throttled",
}

func securityAuditMigration() string {
	return `CREATE TABLE security_audit_buckets(bucket TEXT PRIMARY KEY,retained INTEGER NOT NULL DEFAULT 0 CHECK(retained>=0),maximum INTEGER NOT NULL);
 INSERT INTO security_audit_buckets(bucket,maximum) VALUES('administration',8000),('lifecycle',1000),('authentication',1000);
 ` + securityAuditEventsTable("security_events", securityEventKinds) + securityAuditIndexesAndTriggers(false)
}

// Historical migrations retain their original allowlist. Later schema versions
// rebuild this bounded table using the explicit expanded set of event kinds.
func securityAuditEventsTable(name string, kinds []string) string {
	return ` CREATE TABLE ` + name + `(
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 occurred_at TEXT NOT NULL,
 bucket TEXT NOT NULL REFERENCES security_audit_buckets(bucket),
 kind TEXT NOT NULL CHECK(kind IN ('` + strings.Join(kinds, "','") + `')),
 origin TEXT NOT NULL CHECK(origin IN ('administrator','account','capability','local','system')),
 actor_id TEXT NOT NULL CHECK(length(actor_id)<=64 AND actor_id NOT GLOB '*[^a-zA-Z0-9_-]*'),
 target_type TEXT NOT NULL CHECK(target_type IN ('server','user','session','pairing','transfer','slot','authentication')),
 target_id TEXT NOT NULL CHECK(length(target_id)<=64 AND target_id NOT GLOB '*[^a-zA-Z0-9_-]*'),
 outcome TEXT NOT NULL CHECK(outcome IN ('succeeded','rejected')),
 event_count INTEGER NOT NULL CHECK(typeof(event_count)='integer' AND event_count BETWEEN 1 AND 1000000000));
`
}
func securityAuditIndexesAndTriggers(targetIndex bool) string {
	sql := ` CREATE INDEX security_events_bucket ON security_events(bucket,id);
 CREATE INDEX security_events_age ON security_events(occurred_at,id);
 CREATE TRIGGER security_events_insert AFTER INSERT ON security_events BEGIN
 UPDATE security_audit_buckets SET retained=retained+1 WHERE bucket=NEW.bucket;
 DELETE FROM security_events WHERE id IN (SELECT id FROM security_events WHERE bucket=NEW.bucket ORDER BY id LIMIT (SELECT MAX(0,retained-maximum) FROM security_audit_buckets WHERE bucket=NEW.bucket));
 END;
 CREATE TRIGGER security_events_delete AFTER DELETE ON security_events BEGIN
 UPDATE security_audit_buckets SET retained=retained-1 WHERE bucket=OLD.bucket;
 END;`
	if targetIndex {
		sql += `CREATE INDEX security_events_target ON security_events(target_type,target_id,id DESC);`
	}
	return sql
}

func validAuditID(s string) bool {
	if len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func securityEventBucket(e SecurityEvent) string {
	if strings.HasPrefix(e.Kind, "authentication.") {
		return "authentication"
	}
	if e.Origin == "administrator" || e.Origin == "local" || strings.HasPrefix(e.Kind, "administrator.") || strings.HasPrefix(e.Kind, "settings.") || strings.HasPrefix(e.Kind, "transfers.") {
		return "administration"
	}
	switch e.Kind {
	case "account.created", "account.enabled", "account.disabled", "account.shutdown", "account.password_reset":
		return "administration"
	}
	return "lifecycle"
}

// AppendSecurityEvent is used inside the same writer transaction as the action.
// Input timestamps/IDs are deliberately ignored; writers cannot forge history.
func (q *Queries) AppendSecurityEvent(tx *sql.Tx, e SecurityEvent) error {
	if !validAuditID(e.ActorID) || !validAuditID(e.TargetID) {
		q.MarkSecurityAuditDegraded()
		return errors.New("invalid security event identifier")
	}
	if e.Count == 0 {
		e.Count = 1
	}
	_, err := tx.Exec(`INSERT INTO security_events(occurred_at,bucket,kind,origin,actor_id,target_type,target_id,outcome,event_count) VALUES(?,?,?,?,?,?,?,?,?)`, time.Now().UTC().Format(securityAuditTimeLayout), securityEventBucket(e), e.Kind, e.Origin, e.ActorID, e.TargetType, e.TargetID, e.Outcome, e.Count)
	if err != nil {
		q.MarkSecurityAuditDegraded()
		return err
	}
	_, err = tx.Exec(`DELETE FROM security_events WHERE id IN (SELECT id FROM security_events WHERE occurred_at<? ORDER BY occurred_at,id LIMIT ?)`, securityAuditFloor(time.Now()), securityAuditPruneBatch)
	if err != nil {
		q.MarkSecurityAuditDegraded()
	}
	return err
}

// Recovery must not depend on audit storage. A savepoint isolates statement-level
// audit failures; inability to roll back/release it still aborts the transaction.
func (q *Queries) AppendRecoverySecurityEvent(tx *sql.Tx, e SecurityEvent) error {
	if _, err := tx.Exec(`SAVEPOINT security_audit_recovery`); err != nil {
		return err
	}
	if err := q.AppendSecurityEvent(tx, e); err != nil {
		q.MarkSecurityAuditDegraded()
		if _, rollbackErr := tx.Exec(`ROLLBACK TO security_audit_recovery`); rollbackErr != nil {
			return rollbackErr
		}
	}
	_, err := tx.Exec(`RELEASE security_audit_recovery`)
	return err
}

func (q *Queries) RecordSecurityEvent(e SecurityEvent) error {
	tx, err := q.db.Begin()
	if err != nil {
		q.MarkSecurityAuditDegraded()
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AppendSecurityEvent(tx, e); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		q.MarkSecurityAuditDegraded()
	}
	return err
}

// RecordSecurityEvents atomically persists the fixed set of aggregate counters.
func (q *Queries) RecordSecurityEvents(ctx context.Context, events []SecurityEvent) error {
	if len(events) > 4 {
		return errors.New("security aggregate batch too large")
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		q.MarkSecurityAuditDegraded()
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, e := range events {
		if !strings.HasPrefix(e.Kind, "authentication.") {
			return errors.New("invalid security aggregate kind")
		}
		if err = q.AppendSecurityEvent(tx, e); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		q.MarkSecurityAuditDegraded()
	}
	return err
}
func (q *Queries) SecurityAuditDegraded() bool { return q.auditDegraded.Load() }
func (q *Queries) MarkSecurityAuditDegraded()  { q.auditDegraded.Store(true) }
func securityAuditFloor(now time.Time) string {
	return now.UTC().AddDate(0, 0, -SecurityAuditRetentionDays).Format(securityAuditTimeLayout)
}

type SecurityEventPage struct {
	Events        []SecurityEvent `json:"events"`
	NextBefore    *int64          `json:"next_before"`
	RetentionDays int             `json:"retention_days"`
	MaxEvents     int             `json:"max_events"`
	Degraded      bool            `json:"degraded"`
}

func (q *Queries) SecurityEvents(before int64, limit int, now time.Time) (SecurityEventPage, error) {
	p := SecurityEventPage{Events: []SecurityEvent{}, RetentionDays: SecurityAuditRetentionDays, MaxEvents: SecurityAuditMaxEvents, Degraded: q.SecurityAuditDegraded()}
	if before < 0 || limit < 1 || limit > 100 {
		return p, ErrInvalidPage
	}
	query := `SELECT id,occurred_at,kind,origin,actor_id,target_type,target_id,outcome,event_count FROM security_events WHERE occurred_at>=?`
	args := []any{securityAuditFloor(now)}
	if before > 0 {
		query += ` AND id<?`
		args = append(args, before)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := q.db.Query(query, args...)
	if err != nil {
		q.MarkSecurityAuditDegraded()
		return p, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e SecurityEvent
		if err = rows.Scan(&e.ID, &e.OccurredAt, &e.Kind, &e.Origin, &e.ActorID, &e.TargetType, &e.TargetID, &e.Outcome, &e.Count); err != nil {
			q.MarkSecurityAuditDegraded()
			return p, err
		}
		if len(p.Events) == limit {
			cursor := p.Events[len(p.Events)-1].ID
			p.NextBefore = &cursor
			break
		}
		p.Events = append(p.Events, e)
	}
	err = rows.Err()
	if err != nil {
		q.MarkSecurityAuditDegraded()
	}
	return p, err
}
func (q *Queries) PruneSecurityEvents(now time.Time) (int64, error) {
	r, err := q.db.Exec(`DELETE FROM security_events WHERE id IN (SELECT id FROM security_events WHERE occurred_at<? ORDER BY occurred_at,id LIMIT ?)`, securityAuditFloor(now), securityAuditPruneBatch)
	if err != nil {
		q.MarkSecurityAuditDegraded()
		return 0, err
	}
	return r.RowsAffected()
}

func (q *Queries) auditAdminMutation(tx *sql.Tx, actors []*AdminActor, kind, targetType, targetID string, recovery bool) error {
	a := optionalAdminActor(actors)
	if a == nil {
		return nil
	}
	e := SecurityEvent{Kind: kind, Origin: "administrator", ActorID: a.UserID, TargetType: targetType, TargetID: targetID, Outcome: "succeeded"}
	if recovery {
		return q.AppendRecoverySecurityEvent(tx, e)
	}
	if err := q.AppendSecurityEvent(tx, e); err != nil {
		return fmt.Errorf("security audit unavailable: %w", err)
	}
	return nil
}
