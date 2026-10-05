package database

import (
	"database/sql"
	"errors"
	"time"
)

const CleanupDiscoveryBatch = 64
const CleanupWorkBatch = 16

var ErrCleanupNotEligible = errors.New("resource is not eligible for cleanup")

// This metadata describes queued cleanup, not a full physical-storage inventory.
type ResourceCleanupStatus struct {
	State         string     `json:"state"`
	Reason        string     `json:"reason,omitempty"`
	PendingSince  *time.Time `json:"pending_since,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	NextRetryAt   *time.Time `json:"next_retry_at,omitempty"`
	AttemptCount  int64      `json:"attempt_count"`
	FailureCode   string     `json:"failure_code,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
}
type CleanupOverview struct {
	PendingCount     int64      `json:"pending_count"`
	FailedCount      int64      `json:"failed_count"`
	BusyCount        int64      `json:"busy_count"`
	OldestPendingAt  *time.Time `json:"oldest_pending_at,omitempty"`
	LastDiscoveryAt  *time.Time `json:"last_discovery_at,omitempty"`
	DiscoveryPending bool       `json:"discovery_pending"`
}
type CleanupTask struct{ Kind, ID, Mode string }

func cleanupStateMigration() string {
	return `CREATE TABLE cleanup_tasks (
 kind TEXT NOT NULL CHECK(kind IN ('transfer','slot')),
 resource_id TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('full','payload')),
 reason TEXT NOT NULL CHECK(reason IN ('revoked','expired','unfinished_expired','download_limit')),
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','busy','waiting_children','failed')),
 pending_since INTEGER NOT NULL,
 last_attempt_at INTEGER NOT NULL DEFAULT 0,
 next_retry_at INTEGER NOT NULL DEFAULT (unixepoch()),
 attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 1000000000),
 failure_code TEXT NOT NULL DEFAULT '' CHECK(failure_code IN ('','storage_delete_failed','metadata_delete_failed')),
 last_failure_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(kind,resource_id));
 CREATE INDEX cleanup_tasks_due ON cleanup_tasks(kind,next_retry_at,resource_id);
 CREATE INDEX cleanup_tasks_oldest ON cleanup_tasks(pending_since);
 CREATE INDEX files_transfer_payload ON files(transfer_id,payload_deleted);
 CREATE TABLE cleanup_progress (
 id INTEGER PRIMARY KEY CHECK(id=1),
 pending_count INTEGER NOT NULL DEFAULT 0 CHECK(pending_count>=0),
 failed_count INTEGER NOT NULL DEFAULT 0 CHECK(failed_count>=0),
 busy_count INTEGER NOT NULL DEFAULT 0 CHECK(busy_count>=0),
 transfer_cursor TEXT NOT NULL DEFAULT '',slot_cursor TEXT NOT NULL DEFAULT '',
 transfer_discovered_at INTEGER NOT NULL DEFAULT 0,slot_discovered_at INTEGER NOT NULL DEFAULT 0);
 INSERT INTO cleanup_progress(id) VALUES(1);
 CREATE TRIGGER cleanup_insert AFTER INSERT ON cleanup_tasks BEGIN
 UPDATE cleanup_progress SET pending_count=pending_count+1,failed_count=failed_count+(NEW.state='failed'),busy_count=busy_count+(NEW.state IN ('busy','waiting_children')) WHERE id=1; END;
 CREATE TRIGGER cleanup_delete AFTER DELETE ON cleanup_tasks BEGIN
 UPDATE cleanup_progress SET pending_count=pending_count-1,failed_count=failed_count-(OLD.state='failed'),busy_count=busy_count-(OLD.state IN ('busy','waiting_children')) WHERE id=1; END;
 CREATE TRIGGER cleanup_update AFTER UPDATE OF state ON cleanup_tasks BEGIN
 UPDATE cleanup_progress SET failed_count=failed_count+(NEW.state='failed')-(OLD.state='failed'),busy_count=busy_count+(NEW.state IN ('busy','waiting_children'))-(OLD.state IN ('busy','waiting_children')) WHERE id=1; END;
 CREATE TRIGGER transfer_cleanup_deleted AFTER DELETE ON transfers BEGIN DELETE FROM cleanup_tasks WHERE kind='transfer' AND resource_id=OLD.id; END;
 CREATE TRIGGER slot_cleanup_deleted AFTER DELETE ON slots BEGIN DELETE FROM cleanup_tasks WHERE kind='slot' AND resource_id=OLD.id; END;
 CREATE TRIGGER transfer_cleanup_revoked AFTER UPDATE OF status ON transfers WHEN NEW.status='revoked' AND OLD.status!='revoked' BEGIN
 INSERT INTO cleanup_tasks(kind,resource_id,mode,reason,pending_since) VALUES('transfer',NEW.id,'full','revoked',unixepoch())
 ON CONFLICT(kind,resource_id) DO UPDATE SET mode='full',reason=CASE WHEN cleanup_tasks.mode='full' THEN cleanup_tasks.reason ELSE 'revoked' END,state='pending',next_retry_at=unixepoch(); END;
 CREATE TRIGGER slot_cleanup_revoked AFTER UPDATE OF status ON slots WHEN NEW.status='revoked' AND OLD.status!='revoked' BEGIN
 INSERT INTO cleanup_tasks(kind,resource_id,mode,reason,pending_since) VALUES('slot',NEW.id,'full','revoked',unixepoch())
 ON CONFLICT(kind,resource_id) DO UPDATE SET mode='full',reason=CASE WHEN cleanup_tasks.mode='full' THEN cleanup_tasks.reason ELSE 'revoked' END,state='pending',next_retry_at=unixepoch(); END;`
}
func cleanupTime(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	at := time.Unix(value, 0).UTC()
	return &at
}
func validCleanupKind(kind string) bool { return kind == "transfer" || kind == "slot" }
func (q *Queries) ResourceCleanup(kind, id string) (ResourceCleanupStatus, error) {
	out := ResourceCleanupStatus{State: "none"}
	if !validCleanupKind(kind) {
		return out, ErrCleanupNotEligible
	}
	var pending, attempt, retry, failed int64
	err := q.db.QueryRow(`SELECT state,reason,pending_since,last_attempt_at,next_retry_at,attempt_count,failure_code,last_failure_at FROM cleanup_tasks WHERE kind=? AND resource_id=?`, kind, id).Scan(&out.State, &out.Reason, &pending, &attempt, &retry, &out.AttemptCount, &out.FailureCode, &failed)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.PendingSince, out.LastAttemptAt, out.NextRetryAt, out.LastFailureAt = cleanupTime(pending), cleanupTime(attempt), cleanupTime(retry), cleanupTime(failed)
	return out, nil
}
func (q *Queries) CleanupOverview() (CleanupOverview, error) {
	var out CleanupOverview
	var transferCursor, slotCursor string
	var transferAt, slotAt int64
	var oldest sql.NullInt64
	err := q.db.QueryRow(`SELECT pending_count,failed_count,busy_count,transfer_cursor,slot_cursor,transfer_discovered_at,slot_discovered_at,(SELECT pending_since FROM cleanup_tasks ORDER BY pending_since LIMIT 1) FROM cleanup_progress WHERE id=1`).Scan(&out.PendingCount, &out.FailedCount, &out.BusyCount, &transferCursor, &slotCursor, &transferAt, &slotAt, &oldest)
	if err != nil {
		return out, err
	}
	out.OldestPendingAt = cleanupTime(oldest.Int64)
	out.LastDiscoveryAt = cleanupTime(min(transferAt, slotAt))
	out.DiscoveryPending = transferAt == 0 || slotAt == 0 || transferCursor != "" || slotCursor != ""
	return out, nil
}
func enqueueCleanup(tx *sql.Tx, kind, id, mode, reason string, now time.Time, reset bool) error {
	query := `INSERT INTO cleanup_tasks(kind,resource_id,mode,reason,pending_since,next_retry_at) VALUES(?,?,?,?,?,?) ON CONFLICT(kind,resource_id) DO UPDATE SET mode='full',reason=excluded.reason,state='pending',next_retry_at=excluded.next_retry_at WHERE cleanup_tasks.mode='payload' AND excluded.mode='full'`
	if reset {
		query = `INSERT INTO cleanup_tasks(kind,resource_id,mode,reason,pending_since,next_retry_at) VALUES(?,?,?,?,?,?) ON CONFLICT(kind,resource_id) DO UPDATE SET mode=CASE WHEN cleanup_tasks.mode='full' THEN 'full' ELSE excluded.mode END,reason=CASE WHEN cleanup_tasks.mode='full' THEN cleanup_tasks.reason ELSE excluded.reason END,state='pending',next_retry_at=0`
	}
	_, err := tx.Exec(query, kind, id, mode, reason, now.Unix(), now.Unix())
	return err
}
func eligibleTransferCleanup(status string, expires time.Time, pending sql.NullTime, maximum, count int, payload bool, now time.Time) (string, string) {
	switch {
	case status == "revoked":
		return "full", "revoked"
	case !now.Before(expires):
		return "full", "expired"
	case status == "pending" && pending.Valid && !now.Before(pending.Time):
		return "full", "unfinished_expired"
	case status == "complete" && maximum > 0 && count >= maximum && payload:
		return "payload", "download_limit"
	default:
		return "", ""
	}
}

// DiscoverCleanup inspects a bounded primary-key page of each resource type.
// Go parses historical timestamps correctly, including legacy timezone offsets.
// Cursors persist across restart; unvisited expiry backlog is explicitly unknown.
func (q *Queries) DiscoverCleanup(now time.Time) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE cleanup_progress SET id=id WHERE id=1`); err != nil {
		return err
	}
	var transferCursor, slotCursor string
	if err = tx.QueryRow(`SELECT transfer_cursor,slot_cursor FROM cleanup_progress WHERE id=1`).Scan(&transferCursor, &slotCursor); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT t.id,t.status,t.expires_at,t.pending_expires_at,t.max_downloads,t.download_count,EXISTS(SELECT 1 FROM files f WHERE f.transfer_id=t.id AND f.payload_deleted=0),EXISTS(SELECT 1 FROM slot_transfers st JOIN slots s ON s.id=st.slot_id WHERE st.transfer_id=t.id AND s.status='revoked') FROM transfers t WHERE t.id>? ORDER BY t.id LIMIT ?`, transferCursor, CleanupDiscoveryBatch)
	if err != nil {
		return err
	}
	type discovered struct {
		id, mode, reason string
		revoke           bool
	}
	transfers := []discovered{}
	count := 0
	for rows.Next() {
		var id, status string
		var expires time.Time
		var pending sql.NullTime
		var maximum, downloads int
		var payload, parentRevoked bool
		if err = rows.Scan(&id, &status, &expires, &pending, &maximum, &downloads, &payload, &parentRevoked); err != nil {
			_ = rows.Close()
			return err
		}
		count++
		transferCursor = id
		revoke := parentRevoked && status != "revoked"
		if revoke {
			status = "revoked"
		}
		mode, reason := eligibleTransferCleanup(status, expires, pending, maximum, downloads, payload, now)
		if mode != "" {
			transfers = append(transfers, discovered{id, mode, reason, revoke})
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, item := range transfers {
		if item.revoke {
			if _, err = tx.Exec(`UPDATE transfers SET status='revoked' WHERE id=? AND status!='revoked'`, item.id); err != nil {
				return err
			}
		}
		if err = enqueueCleanup(tx, "transfer", item.id, item.mode, item.reason, now, false); err != nil {
			return err
		}
	}
	if count < CleanupDiscoveryBatch {
		transferCursor = ""
		if _, err = tx.Exec(`UPDATE cleanup_progress SET transfer_discovered_at=? WHERE id=1`, now.Unix()); err != nil {
			return err
		}
	}
	rows, err = tx.Query(`SELECT id,status,expires_at FROM slots WHERE id>? ORDER BY id LIMIT ?`, slotCursor, CleanupDiscoveryBatch)
	if err != nil {
		return err
	}
	slots := []discovered{}
	count = 0
	for rows.Next() {
		var id, status string
		var expires time.Time
		if err = rows.Scan(&id, &status, &expires); err != nil {
			_ = rows.Close()
			return err
		}
		count++
		slotCursor = id
		reason := ""
		if status == "revoked" {
			reason = "revoked"
		} else if !now.Before(expires) {
			reason = "expired"
		}
		if reason != "" {
			slots = append(slots, discovered{id, "full", reason, false})
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, item := range slots {
		if err = enqueueCleanup(tx, "slot", item.id, item.mode, item.reason, now, false); err != nil {
			return err
		}
	}
	if count < CleanupDiscoveryBatch {
		slotCursor = ""
		if _, err = tx.Exec(`UPDATE cleanup_progress SET slot_discovered_at=? WHERE id=1`, now.Unix()); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE cleanup_progress SET transfer_cursor=?,slot_cursor=? WHERE id=1`, transferCursor, slotCursor); err != nil {
		return err
	}
	return tx.Commit()
}
func (q *Queries) DueCleanup(kind string, now time.Time) ([]CleanupTask, error) {
	if !validCleanupKind(kind) {
		return nil, ErrCleanupNotEligible
	}
	rows, err := q.db.Query(`SELECT resource_id,mode FROM cleanup_tasks WHERE kind=? AND next_retry_at<=? ORDER BY next_retry_at,resource_id LIMIT ?`, kind, now.Unix(), CleanupWorkBatch)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tasks := []CleanupTask{}
	for rows.Next() {
		task := CleanupTask{Kind: kind}
		if err = rows.Scan(&task.ID, &task.Mode); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}
func (q *Queries) RecordCleanupAttempt(kind, id, state, failure string, now time.Time) error {
	if !validCleanupKind(kind) {
		return ErrCleanupNotEligible
	}
	switch state {
	case "pending", "busy", "waiting_children":
		if failure != "" {
			return ErrCleanupNotEligible
		}
	case "failed":
		if failure != "storage_delete_failed" && failure != "metadata_delete_failed" {
			return ErrCleanupNotEligible
		}
	default:
		return ErrCleanupNotEligible
	}
	delay := int64(1)
	if state == "failed" {
		delay = 5
	}
	_, err := q.db.Exec(`UPDATE cleanup_tasks SET state=?,last_attempt_at=?,next_retry_at=?+CASE WHEN ?='failed' THEN MIN(300,?*(1+MIN(attempt_count,59))) ELSE ? END,failure_code=CASE WHEN ?='' THEN failure_code ELSE ? END,last_failure_at=CASE WHEN ?='' THEN last_failure_at ELSE ? END WHERE kind=? AND resource_id=?`, state, now.Unix(), now.Unix(), state, delay, delay, failure, failure, failure, now.Unix(), kind, id)
	return err
}

// RequestResourceCleanup is an explicit retry. It never makes an active link
// eligible, and administrator authority is checked in the scheduling transaction.
func (q *Queries) RequestResourceCleanup(kind, id string, actors ...*AdminActor) error {
	if !validCleanupKind(kind) {
		return ErrCleanupNotEligible
	}
	tx, err := q.beginAdminMutation(actors)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE cleanup_progress SET id=id WHERE id=1`); err != nil {
		return err
	}
	now := time.Now()
	mode, reason := "", ""
	if kind == "transfer" {
		var status string
		var expires time.Time
		var pending sql.NullTime
		var maximum, count int
		var payload bool
		err = tx.QueryRow(`SELECT t.status,t.expires_at,t.pending_expires_at,t.max_downloads,t.download_count,EXISTS(SELECT 1 FROM files f WHERE f.transfer_id=t.id AND f.payload_deleted=0) FROM transfers t WHERE t.id=?`, id).Scan(&status, &expires, &pending, &maximum, &count, &payload)
		if err != nil {
			return err
		}
		mode, reason = eligibleTransferCleanup(status, expires, pending, maximum, count, payload, now)
	} else {
		var status string
		var expires time.Time
		if err = tx.QueryRow(`SELECT status,expires_at FROM slots WHERE id=?`, id).Scan(&status, &expires); err != nil {
			return err
		}
		if status == "revoked" {
			mode, reason = "full", "revoked"
		} else if !now.Before(expires) {
			mode, reason = "full", "expired"
		}
	}
	if mode == "" {
		return ErrCleanupNotEligible
	}
	if err = enqueueCleanup(tx, kind, id, mode, reason, now, true); err != nil {
		return err
	}
	return tx.Commit()
}

// ReleaseCleanedPayloads keeps file/manifest metadata and cumulative allowances.
// The caller must verify physical deletion and absence of readers under its lock.
func (q *Queries) ReleaseCleanedPayloads(id string) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE files SET payload_deleted=1 WHERE transfer_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM cleanup_tasks WHERE kind='transfer' AND resource_id=? AND mode='payload'`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (q *Queries) StartCleanupAttempt(kind, id string, now time.Time) error {
	if !validCleanupKind(kind) {
		return ErrCleanupNotEligible
	}
	_, err := q.db.Exec(`UPDATE cleanup_tasks SET last_attempt_at=?,next_retry_at=?,attempt_count=MIN(1000000000,attempt_count+1) WHERE kind=? AND resource_id=?`, now.Unix(), now.Add(5*time.Second).Unix(), kind, id)
	return err
}
func (q *Queries) SlotCleanupChildren(id string) ([]string, error) {
	rows, err := q.db.Query(`SELECT transfer_id FROM slot_transfers WHERE slot_id=? ORDER BY transfer_id LIMIT ?`, id, CleanupWorkBatch)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var child string
		if err = rows.Scan(&child); err != nil {
			return nil, err
		}
		ids = append(ids, child)
	}
	return ids, rows.Err()
}
