package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/endorses/psst.zip/backend/internal/store"
	"golang.org/x/sys/unix"
)

const OrphanDirectoryLimit = 64
const OrphanCandidateLimit = 256
const OrphanPageSize = 16
const OrphanGracePeriod = time.Hour

func orphanMigration() string {
	return `
CREATE TABLE orphan_progress (
 id INTEGER PRIMARY KEY CHECK(id=1), generation INTEGER NOT NULL DEFAULT 1,
 cursor TEXT NOT NULL DEFAULT '{}', root_done INTEGER NOT NULL DEFAULT 0,
 tick INTEGER NOT NULL DEFAULT 0, completed_at INTEGER,
 saturated INTEGER NOT NULL DEFAULT 0, unstable INTEGER NOT NULL DEFAULT 0,
 pass_saturated INTEGER NOT NULL DEFAULT 0, pass_unstable INTEGER NOT NULL DEFAULT 0,
 scan_error INTEGER NOT NULL DEFAULT 0, pass_failed INTEGER NOT NULL DEFAULT 0
);
INSERT INTO orphan_progress(id) VALUES(1);
CREATE TABLE orphan_directories (
 name TEXT PRIMARY KEY, entry TEXT NOT NULL, cursor TEXT NOT NULL DEFAULT '{}',
 revision INTEGER NOT NULL DEFAULT 0, scan_id INTEGER NOT NULL, touched INTEGER NOT NULL, first_seen INTEGER NOT NULL
);
CREATE INDEX orphan_directory_work ON orphan_directories(touched,name);
CREATE TABLE orphan_candidates (
 directory TEXT NOT NULL, name TEXT NOT NULL, entry TEXT NOT NULL, fingerprint TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 0, first_seen INTEGER NOT NULL, observed_at INTEGER NOT NULL, seen_scan INTEGER NOT NULL, parent_generation INTEGER NOT NULL,
 touched INTEGER NOT NULL, category TEXT NOT NULL CHECK(category IN ('pending','busy','failed','unsupported')),
 PRIMARY KEY(directory,name)
);
CREATE INDEX orphan_candidate_work ON orphan_candidates(touched,directory,name);
`
}

type OrphanStatus struct {
	State               string     `json:"state"`
	ScanPending         bool       `json:"scan_pending"`
	LastScanCompletedAt *time.Time `json:"last_scan_completed_at,omitempty"`
	PendingDirectories  int64      `json:"pending_directories"`
	PendingCandidates   int64      `json:"pending_candidates"`
	BusyCount           int64      `json:"busy_count"`
	FailedCount         int64      `json:"failed_count"`
	UnsupportedCount    int64      `json:"unsupported_count"`
	Saturated           bool       `json:"saturated"`
	Unstable            bool       `json:"unstable"`
	OldestPendingAt     *time.Time `json:"oldest_pending_at,omitempty"`
	ScanErrorCode       string     `json:"scan_error_code,omitempty"`
}

func (q *Queries) ResetOrphanScan(ctx context.Context) error {
	// Keep partial directory cursors and candidates; invalidate all in-flight page
	// observations and completed coverage. A new root pass revalidates directories.
	_, err := q.db.ExecContext(ctx, `UPDATE orphan_progress SET generation=generation+1,cursor='{}',root_done=0,completed_at=NULL,pass_saturated=0,pass_unstable=0,pass_failed=0 WHERE id=1`)
	if err != nil {
		_ = q.MarkOrphanFailure(ctx)
	}
	return err
}
func (q *Queries) OrphanScanStatus(ctx context.Context) (OrphanStatus, error) {
	var s OrphanStatus
	var completed, oldest sql.NullInt64
	var scanError, rootDone bool
	var cursor string
	err := q.db.QueryRowContext(ctx, `SELECT completed_at,saturated,unstable,scan_error,root_done,cursor,
 (SELECT COUNT(*) FROM orphan_directories),(SELECT COUNT(*) FROM orphan_candidates),
 (SELECT COUNT(*) FROM orphan_candidates WHERE category='busy'),
 (SELECT COUNT(*) FROM orphan_candidates WHERE category='failed'),
 (SELECT COUNT(*) FROM orphan_candidates WHERE category='unsupported'),
 (SELECT MIN(first_seen) FROM (SELECT first_seen FROM orphan_candidates UNION ALL SELECT first_seen FROM orphan_directories))
 FROM orphan_progress WHERE id=1`).Scan(&completed, &s.Saturated, &s.Unstable, &scanError, &rootDone, &cursor, &s.PendingDirectories, &s.PendingCandidates, &s.BusyCount, &s.FailedCount, &s.UnsupportedCount, &oldest)
	if err != nil {
		return s, err
	}
	q.orphan.Lock()
	localFailure := q.orphan.failures != q.orphan.acknowledged
	q.orphan.Unlock()
	if scanError || localFailure {
		s.ScanErrorCode = "scan_failed"
	}
	if completed.Valid {
		t := time.Unix(completed.Int64, 0).UTC()
		s.LastScanCompletedAt = &t
	}
	if oldest.Valid {
		t := time.Unix(oldest.Int64, 0).UTC()
		s.OldestPendingAt = &t
	}
	s.ScanPending = !completed.Valid || s.PendingDirectories > 0 || s.PendingCandidates > 0 || s.Saturated || s.Unstable || s.ScanErrorCode != ""
	s.State = "checked"
	if s.ScanPending {
		s.State = "pending"
	}
	if s.FailedCount > 0 || s.UnsupportedCount > 0 || s.ScanErrorCode != "" {
		s.State = "degraded"
	}
	return s, nil
}
func (q *Queries) MarkOrphanFailure(ctx context.Context) error {
	q.orphan.Lock()
	q.orphan.failures++
	q.orphan.Unlock()
	_, err := q.db.ExecContext(ctx, `UPDATE orphan_progress SET scan_error=1,pass_failed=1 WHERE id=1`)
	return err
}

type OrphanDirectory struct {
	Budget       int
	ScanID       int64
	Generation   int64
	Name         string
	Revision     int64
	Cursor       store.InventoryCursor
	Entry        store.InventoryEntry
	before       string
	failureEpoch uint64
}

func (q *Queries) NextOrphanDirectory(ctx context.Context, root bool) (OrphanDirectory, error) {
	var d OrphanDirectory
	d.Budget = OrphanPageSize
	var rawEntry string
	var done bool
	if root {
		var queued int
		if err := q.db.QueryRowContext(ctx, `SELECT generation,cursor,root_done,(SELECT COUNT(*) FROM orphan_directories) FROM orphan_progress WHERE id=1`).Scan(&d.Generation, &d.before, &done, &queued); err != nil {
			return d, err
		}
		if done || queued >= OrphanDirectoryLimit {
			return d, sql.ErrNoRows
		}
		d.Budget = min(OrphanPageSize, OrphanDirectoryLimit-queued)
		d.ScanID = -d.Generation
	} else {
		err := q.db.QueryRowContext(ctx, `SELECT p.generation,d.name,d.revision,d.cursor,d.entry,d.scan_id FROM orphan_directories d CROSS JOIN orphan_progress p WHERE p.id=1 ORDER BY d.touched,d.name LIMIT 1`).Scan(&d.Generation, &d.Name, &d.Revision, &d.before, &rawEntry, &d.ScanID)
		if err != nil {
			return d, err
		}
		if err = json.Unmarshal([]byte(rawEntry), &d.Entry); err != nil {
			return d, err
		}
	}
	if err := json.Unmarshal([]byte(d.before), &d.Cursor); err != nil {
		return d, err
	}
	q.orphan.Lock()
	if !q.orphan.passActive || q.orphan.passGeneration != d.Generation {
		q.orphan.passActive = true
		q.orphan.passGeneration = d.Generation
		q.orphan.passEpoch = q.orphan.failures
	}
	d.failureEpoch = q.orphan.passEpoch
	q.orphan.Unlock()
	return d, nil
}

// Only directory identity, not timestamps changed by sibling writes, belongs in
// the ancestor fingerprint. The entry itself uses its entire stable identity.
func orphanFingerprint(e store.InventoryEntry) string {
	e.Root = store.InventoryIdentity{Device: e.Root.Device, Inode: e.Root.Inode, Mode: e.Root.Mode}
	e.Parent = store.InventoryIdentity{Device: e.Parent.Device, Inode: e.Parent.Inode, Mode: e.Parent.Mode}
	raw, _ := json.Marshal(e)
	return string(raw)
}
func orphanReferenced(ctx context.Context, tx *sql.Tx, e store.InventoryEntry) (bool, error) {
	var referenced bool
	var err error
	if e.Directory == "" {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transfers WHERE id=?)`, e.Name).Scan(&referenced)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM files WHERE transfer_id=? AND id=?)`, e.Directory, e.Name).Scan(&referenced)
	}
	return referenced, err
}
func orphanSaturated(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE orphan_progress SET saturated=1,pass_saturated=1 WHERE id=1`)
	return err
}
func observeOrphan(ctx context.Context, tx *sql.Tx, e store.InventoryEntry, now int64, tick int64, scanID int64, generation int64) error {
	referenced, err := orphanReferenced(ctx, tx, e)
	if err != nil {
		return err
	}
	if referenced && !e.Unsupported {
		_, err = tx.ExecContext(ctx, `DELETE FROM orphan_candidates WHERE directory=? AND name=?`, e.Directory, e.Name)
		return err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	fingerprint := orphanFingerprint(e)
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orphan_candidates WHERE directory=? AND name=?)`, e.Directory, e.Name).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM orphan_candidates`).Scan(&count); err != nil {
			return err
		}
		if count >= OrphanCandidateLimit {
			// Retry failures remain explicit in incomplete coverage, but cannot occupy
			// every queue slot forever and hide later healthy entries.
			if _, err = tx.ExecContext(ctx, `DELETE FROM orphan_candidates WHERE (directory,name) IN (SELECT directory,name FROM orphan_candidates WHERE category!='pending' ORDER BY touched,directory,name LIMIT 1)`); err != nil {
				return err
			}
			if err = orphanSaturated(ctx, tx); err != nil {
				return err
			}
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM orphan_candidates`).Scan(&count); err != nil {
				return err
			}
			if count >= OrphanCandidateLimit {
				return nil
			}
		}
	}
	category := "pending"
	if e.Unsupported {
		category = "unsupported"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO orphan_candidates(directory,name,entry,fingerprint,first_seen,observed_at,touched,category,seen_scan,parent_generation) VALUES(?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(directory,name) DO UPDATE SET entry=excluded.entry,fingerprint=excluded.fingerprint,
 first_seen=CASE WHEN fingerprint=excluded.fingerprint THEN first_seen ELSE excluded.first_seen END,
 observed_at=excluded.observed_at,revision=revision+1,seen_scan=excluded.seen_scan,parent_generation=excluded.parent_generation,
 category=CASE WHEN fingerprint=excluded.fingerprint THEN category ELSE excluded.category END`, e.Directory, e.Name, string(raw), fingerprint, now, now, tick, category, scanID, generation)
	return err
}

// CommitOrphanPage atomically records every observation and the enumeration
// cursor under a generation/revision guard. Failed observations never advance it.
func (q *Queries) CommitOrphanPage(ctx context.Context, d OrphanDirectory, p store.InventoryPage, now time.Time) error {
	if len(p.Entries) > OrphanPageSize {
		return errors.New("orphan page exceeds bound")
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var tick int64
	err = tx.QueryRowContext(ctx, `UPDATE orphan_progress SET tick=tick+1 WHERE id=1 AND generation=? RETURNING tick`, d.Generation).Scan(&tick)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(p.Next)
	var result sql.Result
	if d.Name == "" {
		result, err = tx.ExecContext(ctx, `UPDATE orphan_progress SET cursor=?,root_done=? WHERE id=1 AND cursor=? AND root_done=0`, string(raw), p.Done, d.before)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE orphan_directories SET cursor=?,revision=revision+1,touched=? WHERE name=? AND revision=? AND cursor=?`, string(raw), tick, d.Name, d.Revision, d.before)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if p.Unstable || p.Next.Unstable {
		if _, err = tx.ExecContext(ctx, `UPDATE orphan_progress SET unstable=1,pass_unstable=1 WHERE id=1`); err != nil {
			return err
		}
	}
	for _, e := range p.Entries {
		if d.Name == "" {
			if _, err = tx.ExecContext(ctx, `UPDATE orphan_candidates SET parent_generation=? WHERE directory=?`, d.Generation, e.Name); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE orphan_candidates SET seen_scan=? WHERE directory='' AND name=?`, -d.Generation, e.Name); err != nil {
				return err
			}
		}
		if d.Name == "" && !e.Unsupported && e.File.Mode&unix.S_IFMT == unix.S_IFDIR {
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM orphan_directories`).Scan(&count); err != nil {
				return err
			}
			if count >= OrphanDirectoryLimit {
				if err = orphanSaturated(ctx, tx); err != nil {
					return err
				}
				continue
			}
			entry, _ := json.Marshal(e)
			// Existing partial work survives root rediscovery; changed identity is
			// rejected by the worker and rediscovered on the following pass.
			_, err = tx.ExecContext(ctx, `INSERT INTO orphan_directories(name,entry,touched,first_seen,scan_id) VALUES(?,?,?,?,?) ON CONFLICT(name) DO NOTHING`, e.Name, string(entry), tick, now.Unix(), tick)
		} else {
			err = observeOrphan(ctx, tx, e, now.Unix(), tick, d.ScanID, d.Generation)
		}
		if err != nil {
			return err
		}
	}
	// Unsupported entries have no deletion work. Clear stale reports only after
	// stable complete enumeration proves they are no longer in that directory.
	if p.Done && !p.Unstable && !p.Next.Unstable {
		if _, err = tx.ExecContext(ctx, `DELETE FROM orphan_candidates WHERE category='unsupported' AND directory=? AND seen_scan!=?`, d.Name, d.ScanID); err != nil {
			return err
		}
		if d.Name == "" {
			if _, err = tx.ExecContext(ctx, `DELETE FROM orphan_candidates WHERE category='unsupported' AND directory!='' AND parent_generation!=?`, d.Generation); err != nil {
				return err
			}
		}
	}
	// Hot directories are retried on the next pass after four unstable pages;
	// their unbounded growth must not hold all directory slots indefinitely.
	deferred := (p.Unstable || p.Next.Unstable) && d.Revision >= 3
	if d.Name != "" && (p.Done || deferred) {
		if p.Done {
			if err = observeOrphan(ctx, tx, d.Entry, now.Unix(), tick, -d.Generation, d.Generation); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM orphan_directories WHERE name=?`, d.Name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Failed child jobs retire for this pass so a permanently missing, replaced, or
// unreadable directory cannot starve others. The next root pass rediscovers it.
func (q *Queries) FailOrphanDirectory(ctx context.Context, d OrphanDirectory) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE orphan_progress SET unstable=1,pass_unstable=1,scan_error=1,pass_failed=1 WHERE id=1 AND generation=?`, d.Generation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if d.Name != "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM orphan_directories WHERE name=? AND revision=?`, d.Name, d.Revision)
	} else {
		// Continue past a failed root generation only when inventory cannot safely
		// advance: no child coverage can be certified on this pass.
		_, err = tx.ExecContext(ctx, `UPDATE orphan_progress SET root_done=1 WHERE id=1 AND cursor=?`, d.before)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

type OrphanCandidate struct {
	Entry       store.InventoryEntry
	Revision    int64
	FirstSeenAt time.Time
	ObservedAt  time.Time
	Category    string
}

func (q *Queries) NextOrphanCandidates(ctx context.Context) ([]OrphanCandidate, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT entry,revision,first_seen,observed_at,category FROM orphan_candidates ORDER BY touched,directory,name LIMIT 8`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []OrphanCandidate
	for rows.Next() {
		var c OrphanCandidate
		var raw string
		var first, observed int64
		if err = rows.Scan(&raw, &c.Revision, &first, &observed, &c.Category); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &c.Entry); err != nil {
			return nil, err
		}
		c.FirstSeenAt = time.Unix(first, 0)
		c.ObservedAt = time.Unix(observed, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}
func (q *Queries) TouchOrphanCandidate(ctx context.Context, c OrphanCandidate, category string) error {
	if category != "pending" && category != "busy" && category != "failed" && category != "unsupported" {
		return errors.New("invalid orphan category")
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var tick int64
	if err = tx.QueryRowContext(ctx, `UPDATE orphan_progress SET tick=tick+1 WHERE id=1 RETURNING tick`).Scan(&tick); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE orphan_candidates SET touched=?,category=? WHERE directory=? AND name=? AND revision=?`, tick, category, c.Entry.Directory, c.Entry.Name, c.Revision)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// RetireChangedOrphan discards an observation whose identity no longer matches.
// It does not certify deletion: a fresh inventory pass must cover the replacement
// and a new candidate must receive its own grace period before any mutation.
func (q *Queries) RetireChangedOrphan(ctx context.Context, c OrphanCandidate) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE orphan_progress SET unstable=1,pass_unstable=1 WHERE id=1`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM orphan_candidates WHERE directory=? AND name=? AND revision=?`, c.Entry.Directory, c.Entry.Name, c.Revision); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveOrphanCandidate holds SQLite's writer lock across the canonical
// membership recheck and filesystem callback, serializing new allocations. The
// caller must hold the transfer lock and have checked active readers first.
func (q *Queries) RemoveOrphanCandidate(ctx context.Context, c OrphanCandidate, now time.Time, remove func() (bool, error)) (bool, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE orphan_candidates SET revision=revision WHERE directory=? AND name=? AND revision=? AND first_seen<=? AND category!='unsupported'`, c.Entry.Directory, c.Entry.Name, c.Revision, now.Add(-OrphanGracePeriod).Unix())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	referenced, err := orphanReferenced(ctx, tx, c.Entry)
	if err != nil {
		return false, err
	}
	if !referenced {
		ok, removeErr := remove()
		if removeErr != nil {
			return false, removeErr
		}
		if !ok {
			return false, nil
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM orphan_candidates WHERE directory=? AND name=? AND revision=?`, c.Entry.Directory, c.Entry.Name, c.Revision); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// FinishOrphanPass only acknowledges coverage if every directory was traversed
// without skipped work and every candidate was resolved. Queues are bounded, so
// saturation means more discovery is needed even when the current queues empty.
func (q *Queries) FinishOrphanPass(ctx context.Context, now time.Time) error {
	q.orphan.Lock()
	epoch := q.orphan.passEpoch
	generation := q.orphan.passGeneration
	q.orphan.Unlock()
	return q.finishOrphanPass(ctx, now, generation, epoch)
}

func (q *Queries) finishOrphanPass(ctx context.Context, now time.Time, generation int64, epoch uint64) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var clean bool
	err = tx.QueryRowContext(ctx, `UPDATE orphan_progress SET tick=tick+1 WHERE id=1 AND generation=? AND root_done=1 AND NOT EXISTS(SELECT 1 FROM orphan_directories) RETURNING (pass_saturated=0 AND pass_unstable=0 AND pass_failed=0 AND NOT EXISTS(SELECT 1 FROM orphan_candidates))`, generation).Scan(&clean)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE orphan_progress SET completed_at=CASE WHEN ? THEN ? ELSE completed_at END,
 saturated=CASE WHEN ? THEN 0 ELSE (saturated OR pass_saturated) END,
 unstable=CASE WHEN ? THEN 0 ELSE (unstable OR pass_unstable) END,
 scan_error=CASE WHEN ? THEN 0 ELSE scan_error END,
 generation=generation+1,cursor='{}',root_done=0,pass_saturated=0,pass_unstable=0,pass_failed=0 WHERE id=1`, clean, now.Unix(), clean, clean, clean)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	q.orphan.Lock()
	defer q.orphan.Unlock()
	if q.orphan.passGeneration == generation {
		if clean && epoch > q.orphan.acknowledged {
			q.orphan.acknowledged = epoch
		}
		q.orphan.passActive = false
	}
	return nil
}
