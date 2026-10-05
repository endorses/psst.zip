package database

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

const ReconciliationBatchSize = 64

// Failed writes cannot rely on SQLite to record their generation. Keep a
// process-local epoch; a pass may acknowledge only failures preceding its start.
type reconciliationTracker struct {
	sync.Mutex
	failures       uint64
	acknowledged   uint64
	passEpoch      uint64
	passGeneration int64
	passActive     bool
}

func reconciliationMigration() string {
	return `CREATE TABLE file_reconciliation_progress (
 id INTEGER PRIMARY KEY CHECK(id=1), generation INTEGER NOT NULL DEFAULT 0,
 cursor TEXT NOT NULL DEFAULT '', last_scan_completed_at INTEGER,
 scan_error INTEGER NOT NULL DEFAULT 0,pass_failed INTEGER NOT NULL DEFAULT 0,
 busy_count INTEGER NOT NULL DEFAULT 0 CHECK(busy_count>=0),
 unavailable_count INTEGER NOT NULL DEFAULT 0 CHECK(unavailable_count>=0),
 failed_count INTEGER NOT NULL DEFAULT 0 CHECK(failed_count>=0)
 );
 INSERT INTO file_reconciliation_progress(id) VALUES(1);
 CREATE TABLE file_reconciliation_issues (
 file_id TEXT PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
 category TEXT NOT NULL CHECK(category IN ('busy','unavailable','failed')),
 code TEXT NOT NULL CHECK(code IN ('resource_busy','payload_missing','payload_size_mismatch','metadata_invalid','inspect_failed','truncate_failed','offset_update_failed')),
 first_seen_at INTEGER NOT NULL,last_seen_at INTEGER NOT NULL
 );
 CREATE TRIGGER file_reconciliation_issue_insert AFTER INSERT ON file_reconciliation_issues BEGIN
 UPDATE file_reconciliation_progress SET busy_count=busy_count+(NEW.category='busy'),unavailable_count=unavailable_count+(NEW.category='unavailable'),failed_count=failed_count+(NEW.category='failed') WHERE id=1; END;
 CREATE TRIGGER file_reconciliation_issue_delete AFTER DELETE ON file_reconciliation_issues BEGIN
 UPDATE file_reconciliation_progress SET busy_count=busy_count-(OLD.category='busy'),unavailable_count=unavailable_count-(OLD.category='unavailable'),failed_count=failed_count-(OLD.category='failed') WHERE id=1; END;
 CREATE TRIGGER file_reconciliation_issue_update AFTER UPDATE OF category ON file_reconciliation_issues BEGIN
 UPDATE file_reconciliation_progress SET busy_count=busy_count-(OLD.category='busy')+(NEW.category='busy'),unavailable_count=unavailable_count-(OLD.category='unavailable')+(NEW.category='unavailable'),failed_count=failed_count-(OLD.category='failed')+(NEW.category='failed') WHERE id=1; END;
 CREATE INDEX files_transfer_id_order ON files(transfer_id,id);`
}

type ReconciliationStatus struct {
	ScanErrorCode       string     `json:"scan_error_code,omitempty"`
	State               string     `json:"state"`
	IssueCount          int64      `json:"issue_count"`
	BusyCount           int64      `json:"busy_count"`
	UnavailableCount    int64      `json:"unavailable_count"`
	FailedCount         int64      `json:"failed_count"`
	LastScanCompletedAt *time.Time `json:"last_scan_completed_at,omitempty"`
	ScanPending         bool       `json:"scan_pending"`
}

type ReconciliationIssue struct {
	Category    string    `json:"category"`
	Code        string    `json:"code"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

func (q *Queries) ResetReconciliationScan(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, `UPDATE file_reconciliation_progress SET generation=generation+1,cursor='',last_scan_completed_at=NULL,pass_failed=0 WHERE id=1`)
	return err
}
func (q *Queries) ReconciliationStatus(ctx context.Context) (ReconciliationStatus, error) {
	var out ReconciliationStatus
	var cursor string
	var completed sql.NullInt64
	var scanError bool
	err := q.db.QueryRowContext(ctx, `SELECT cursor,last_scan_completed_at,busy_count,unavailable_count,failed_count,scan_error FROM file_reconciliation_progress WHERE id=1`).Scan(&cursor, &completed, &out.BusyCount, &out.UnavailableCount, &out.FailedCount, &scanError)
	if err != nil {
		return out, err
	}
	q.reconciliation.Lock()
	localFailure := q.reconciliation.failures != q.reconciliation.acknowledged
	q.reconciliation.Unlock()
	if scanError || localFailure {
		out.ScanErrorCode = "scan_failed"
	}
	out.IssueCount = out.BusyCount + out.UnavailableCount + out.FailedCount
	if completed.Valid {
		stamp := time.Unix(completed.Int64, 0).UTC()
		out.LastScanCompletedAt = &stamp
	}
	out.ScanPending = !completed.Valid || cursor != "" || out.BusyCount > 0
	out.State = "checked"
	if !completed.Valid || out.BusyCount > 0 {
		out.State = "pending"
	}
	if out.UnavailableCount > 0 || out.FailedCount > 0 || out.ScanErrorCode != "" {
		out.State = "degraded"
	}
	return out, nil
}
func (q *Queries) FileReconciliationIssue(ctx context.Context, id string) (ReconciliationIssue, error) {
	var out ReconciliationIssue
	var first, last int64
	err := q.db.QueryRowContext(ctx, `SELECT category,code,first_seen_at,last_seen_at FROM file_reconciliation_issues WHERE file_id=?`, id).Scan(&out.Category, &out.Code, &first, &last)
	out.FirstSeenAt = time.Unix(first, 0).UTC()
	out.LastSeenAt = time.Unix(last, 0).UTC()
	return out, err
}
func validReconciliationIssue(category, code string) bool {
	switch category {
	case "busy":
		return code == "resource_busy"
	case "unavailable":
		return code == "payload_missing" || code == "payload_size_mismatch" || code == "metadata_invalid"
	case "failed":
		return code == "inspect_failed" || code == "truncate_failed" || code == "offset_update_failed" || code == "metadata_invalid"
	}
	return false
}
func (q *Queries) RecordFileReconciliationIssue(ctx context.Context, id, category, code string) error {
	if !validReconciliationIssue(category, code) {
		return errors.New("invalid reconciliation issue")
	}
	now := time.Now().Unix()
	_, err := q.db.ExecContext(ctx, `INSERT INTO file_reconciliation_issues(file_id,category,code,first_seen_at,last_seen_at)
 SELECT id,?,?,?,? FROM files WHERE id=? AND payload_deleted=0
 ON CONFLICT(file_id) DO UPDATE SET category=excluded.category,code=excluded.code,last_seen_at=excluded.last_seen_at`, category, code, now, now, id)
	return err
}
func (q *Queries) ClearFileReconciliationIssue(ctx context.Context, id string) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM file_reconciliation_issues WHERE file_id=?`, id)
	return err
}

type ReconciliationFile struct {
	File
	TransferStatus string
	CleanupPending bool
}

func (q *Queries) ReconciliationFile(ctx context.Context, id string) (ReconciliationFile, error) {
	var out ReconciliationFile
	err := q.db.QueryRowContext(ctx, `SELECT f.id,f.transfer_id,f.size,f.upload_offset,f.upload_complete,f.payload_deleted,t.status,
 EXISTS(SELECT 1 FROM cleanup_tasks c WHERE c.kind='transfer' AND c.resource_id=t.id AND c.mode='full')
 FROM files f JOIN transfers t ON t.id=f.transfer_id WHERE f.id=?`, id).Scan(&out.ID, &out.TransferID, &out.Size, &out.UploadOffset, &out.UploadComplete, &out.PayloadDeleted, &out.TransferStatus, &out.CleanupPending)
	return out, err
}

// RepairPendingFile checks metadata under the SQLite writer lock before any physical mutation.
// The caller also holds the transfer lock. The callback must only truncate this file.
func (q *Queries) RepairPendingFile(ctx context.Context, expected File, offset int64, complete bool, truncate func() error) (bool, error) {
	if offset < 0 || offset > expected.UploadOffset || offset > expected.Size || (complete && (!expected.UploadComplete || offset != expected.Size)) {
		return false, errors.New("invalid repaired offset")
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE files SET id=id WHERE id=? AND transfer_id=? AND size=? AND upload_offset=? AND upload_complete=? AND payload_deleted=0
 AND EXISTS(SELECT 1 FROM transfers t WHERE t.id=files.transfer_id AND t.status='pending')
 AND NOT EXISTS(SELECT 1 FROM cleanup_tasks c WHERE c.kind='transfer' AND c.resource_id=files.transfer_id AND c.mode='full')`, expected.ID, expected.TransferID, expected.Size, expected.UploadOffset, expected.UploadComplete)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if truncate != nil {
		if err = truncate(); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE files SET upload_offset=?,upload_complete=? WHERE id=?`, offset, complete, expected.ID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM file_reconciliation_issues WHERE file_id=?`, expected.ID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

type ReconciliationBatch struct {
	failureEpoch uint64
	Generation   int64
	Before       string
	After        string
	Complete     bool
	Failed       bool
	Files        []File
}

func (q *Queries) NextReconciliationBatch(ctx context.Context) (ReconciliationBatch, error) {
	var out ReconciliationBatch
	q.reconciliation.Lock()
	startEpoch := q.reconciliation.failures
	q.reconciliation.Unlock()
	tx, err := q.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = tx.QueryRowContext(ctx, `SELECT generation,cursor FROM file_reconciliation_progress WHERE id=1`).Scan(&out.Generation, &out.Before); err != nil {
		return out, err
	}
	q.reconciliation.Lock()
	if !q.reconciliation.passActive || q.reconciliation.passGeneration != out.Generation {
		q.reconciliation.passActive = true
		q.reconciliation.passGeneration = out.Generation
		q.reconciliation.passEpoch = startEpoch
	}
	out.failureEpoch = q.reconciliation.passEpoch
	q.reconciliation.Unlock()
	rows, err := tx.QueryContext(ctx, `SELECT id,transfer_id FROM files WHERE id>? ORDER BY id LIMIT ?`, out.Before, ReconciliationBatchSize)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var f File
		if err = rows.Scan(&f.ID, &f.TransferID); err != nil {
			_ = rows.Close()
			return out, err
		}
		out.Files = append(out.Files, f)
		out.After = f.ID
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, err
	}
	out.Complete = len(out.Files) < ReconciliationBatchSize
	return out, tx.Commit()
}

// MarkReconciliationScanFailure keeps an in-process degraded marker even if SQLite
// cannot persist the failure. A later complete clean traversal clears it.
func (q *Queries) MarkReconciliationScanFailure(ctx context.Context) error {
	q.reconciliation.Lock()
	q.reconciliation.failures++
	q.reconciliation.Unlock()
	_, err := q.db.ExecContext(ctx, `UPDATE file_reconciliation_progress SET scan_error=1,pass_failed=1 WHERE id=1`)
	return err
}
func (q *Queries) AdvanceReconciliationBatch(ctx context.Context, b ReconciliationBatch) error {
	after := b.After
	if b.Complete {
		after = ""
	}
	var scanError bool
	err := q.db.QueryRowContext(ctx, `UPDATE file_reconciliation_progress SET cursor=?,last_scan_completed_at=CASE WHEN ? THEN ? ELSE last_scan_completed_at END,
 scan_error=CASE WHEN ? THEN (pass_failed OR ?) ELSE (scan_error OR ?) END,
 pass_failed=CASE WHEN ? THEN 0 ELSE (pass_failed OR ?) END
 WHERE id=1 AND generation=? AND cursor=? RETURNING scan_error`, after, b.Complete, time.Now().Unix(), b.Complete, b.Failed, b.Failed, b.Complete, b.Failed, b.Generation, b.Before).Scan(&scanError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err == nil && b.Complete {
		q.reconciliation.Lock()
		// A concurrent failure increments failures independently. Acknowledging
		// this old snapshot cannot clear a newer unpersisted failure.
		if !scanError && b.failureEpoch > q.reconciliation.acknowledged {
			q.reconciliation.acknowledged = b.failureEpoch
		}
		if q.reconciliation.passGeneration == b.Generation && q.reconciliation.passEpoch == b.failureEpoch {
			q.reconciliation.passActive = false
		}
		q.reconciliation.Unlock()
	}
	return err
}
func (q *Queries) TransferFileIDs(ctx context.Context, transferID, after string, limit int) ([]string, error) {
	if limit < 1 || limit > ReconciliationBatchSize {
		limit = ReconciliationBatchSize
	}
	rows, err := q.db.QueryContext(ctx, `SELECT id FROM files WHERE transfer_id=? AND id>? ORDER BY id LIMIT ?`, transferID, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
