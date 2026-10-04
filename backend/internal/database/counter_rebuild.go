package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

const CounterRebuildMaxJobs = 64
const CounterRebuildMaxWork = 64

var ErrCounterRebuild = errors.New("counter reconstruction could not complete")

type counterFailureMarker struct{ value byte }

func counterRebuildMigration() string {
	s := `CREATE TABLE counter_rebuild_sources(kind TEXT NOT NULL CHECK(kind IN ('transfer','slot','cleanup','reconciliation')),resource_id TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 0 CHECK(typeof(revision)='integer' AND revision>=0),PRIMARY KEY(kind,resource_id));
 INSERT INTO counter_rebuild_sources(kind,resource_id) VALUES('cleanup',''),('reconciliation','');
 CREATE TABLE counter_rebuild_progress(id INTEGER PRIMARY KEY CHECK(id=1),phase TEXT NOT NULL DEFAULT 'summaries' CHECK(phase IN ('summaries','transfer','slot','stale','waiting','idle')),cursor TEXT NOT NULL DEFAULT '',cursor_kind TEXT NOT NULL DEFAULT '',turn INTEGER NOT NULL DEFAULT 0,last_scan_completed_at INTEGER,scan_error INTEGER NOT NULL DEFAULT 0,rescan_required INTEGER NOT NULL DEFAULT 0);
 INSERT INTO counter_rebuild_progress(id) VALUES(1);
 CREATE TABLE counter_rebuild_jobs(kind TEXT NOT NULL,resource_id TEXT NOT NULL,expected_revision INTEGER NOT NULL DEFAULT -1,phase TEXT NOT NULL DEFAULT 'start',cursor TEXT NOT NULL DEFAULT '',child TEXT NOT NULL DEFAULT '',file_count INTEGER NOT NULL DEFAULT 0,child_count INTEGER NOT NULL DEFAULT 0,reserved_bytes INTEGER NOT NULL DEFAULT 0,occupied_bytes INTEGER NOT NULL DEFAULT 0,manifest_bytes INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','busy','failed')),next_retry_at INTEGER NOT NULL DEFAULT 0,turn INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(kind,resource_id),FOREIGN KEY(kind,resource_id) REFERENCES counter_rebuild_sources(kind,resource_id) ON DELETE CASCADE);
 CREATE TRIGGER counter_rebuild_job_cap BEFORE INSERT ON counter_rebuild_jobs WHEN NOT EXISTS(SELECT 1 FROM counter_rebuild_jobs WHERE kind=NEW.kind AND resource_id=NEW.resource_id) AND (SELECT COUNT(*) FROM counter_rebuild_jobs)>=64 BEGIN SELECT RAISE(ABORT,'counter job limit'); END;
 CREATE INDEX counter_rebuild_due ON counter_rebuild_jobs(next_retry_at,turn,kind,resource_id);
 `
	bump := func(kind, expr string) string {
		return fmt.Sprintf(`INSERT INTO counter_rebuild_sources(kind,resource_id,revision) SELECT '%s',id,1 FROM %ss WHERE id=%s ON CONFLICT(kind,resource_id) DO UPDATE SET revision=revision+1;`, kind, kind, expr)
	}
	leaf := func(row string) string {
		return bump("transfer", row+".transfer_id") + `INSERT INTO counter_rebuild_sources(kind,resource_id,revision) SELECT 'slot',s.id,1 FROM slots s JOIN slot_transfers st ON st.slot_id=s.id WHERE st.transfer_id=` + row + `.transfer_id ON CONFLICT(kind,resource_id) DO UPDATE SET revision=revision+1;`
	}
	for _, kind := range []string{"transfer", "slot"} {
		s += fmt.Sprintf(`CREATE TRIGGER counter_source_%[1]s_insert AFTER INSERT ON %[1]ss BEGIN %[2]s END;
 CREATE TRIGGER counter_source_%[1]s_delete AFTER DELETE ON %[1]ss BEGIN DELETE FROM counter_rebuild_sources WHERE kind='%[1]s' AND resource_id=OLD.id; END;`, kind, bump(kind, "NEW.id"))
	}
	for _, table := range []string{"files", "manifests"} {
		columns := "transfer_id,size,upload_offset,payload_deleted"
		if table == "manifests" {
			columns = "transfer_id,data"
		}
		s += `CREATE TRIGGER counter_source_` + table + `_insert AFTER INSERT ON ` + table + ` BEGIN ` + leaf("NEW") + ` END;`
		s += `CREATE TRIGGER counter_source_` + table + `_delete AFTER DELETE ON ` + table + ` BEGIN ` + leaf("OLD") + ` END;`
		s += `CREATE TRIGGER counter_source_` + table + `_update AFTER UPDATE OF ` + columns + ` ON ` + table + ` BEGIN ` + leaf("OLD") + leaf("NEW") + ` END;`
	}
	// Membership accounting copies child summaries that may still need repair.
	// If slot discovery has started, replay after membership changes so already
	// published slots cannot retain those corrupt deltas. This also covers a
	// child deleted before its repair publishes. Membership churn can require
	// another bounded pass; ordinary idle mutations do not invalidate coverage.
	membershipRescan := `UPDATE counter_rebuild_progress SET rescan_required=1 WHERE id=1 AND phase IN ('slot','stale','waiting');`
	s += `CREATE TRIGGER counter_source_membership_insert AFTER INSERT ON slot_transfers BEGIN ` + bump("slot", "NEW.slot_id") + membershipRescan + ` END;`
	s += `CREATE TRIGGER counter_source_membership_delete AFTER DELETE ON slot_transfers BEGIN ` + bump("slot", "OLD.slot_id") + membershipRescan + ` END;`
	s += `CREATE TRIGGER counter_source_membership_update AFTER UPDATE OF slot_id,transfer_id ON slot_transfers BEGIN ` + bump("slot", "OLD.slot_id") + bump("slot", "NEW.slot_id") + membershipRescan + ` END;`
	for _, summary := range []struct{ kind, table, column string }{{"cleanup", "cleanup_tasks", "state"}, {"reconciliation", "file_reconciliation_issues", "category"}} {
		for _, event := range []string{"INSERT", "DELETE", "UPDATE OF " + summary.column} {
			name := event
			if event != "INSERT" && event != "DELETE" {
				name = "UPDATE"
			}
			when := ""
			if name == "UPDATE" {
				when = fmt.Sprintf(" WHEN NEW.%s IS NOT OLD.%s", summary.column, summary.column)
			}
			s += fmt.Sprintf(`CREATE TRIGGER counter_source_%s_%s AFTER %s ON %s%s BEGIN UPDATE counter_rebuild_sources SET revision=revision+1 WHERE kind='%s' AND resource_id=''; END;`, summary.kind, name, event, summary.table, when, summary.kind)
		}
	}
	// Seek keys are immutable identities. Reject renames so staged cursors cannot
	// miss rows moved behind them; harmless id=id writer guards remain legal.
	for _, key := range []struct{ table, columns, changed string }{
		{"transfers", "id", "NEW.id IS NOT OLD.id"},
		{"slots", "id", "NEW.id IS NOT OLD.id"},
		{"files", "id", "NEW.id IS NOT OLD.id"},
		{"cleanup_tasks", "kind,resource_id", "NEW.kind IS NOT OLD.kind OR NEW.resource_id IS NOT OLD.resource_id"},
		{"file_reconciliation_issues", "file_id", "NEW.file_id IS NOT OLD.file_id"},
	} {
		s += fmt.Sprintf(`CREATE TRIGGER counter_identity_%s BEFORE UPDATE OF %s ON %s WHEN %s BEGIN SELECT RAISE(ABORT,'immutable resource identity'); END;`, key.table, key.columns, key.table, key.changed)
	}
	return s
}

type CounterRebuildStatus struct {
	State               string     `json:"state"`
	ScanPending         bool       `json:"scan_pending"`
	LastScanCompletedAt *time.Time `json:"last_scan_completed_at,omitempty"`
	PendingCount        int64      `json:"pending_count"`
	FailedCount         int64      `json:"failed_count"`
	BusyCount           int64      `json:"busy_count"`
	ScanErrorCode       string     `json:"scan_error_code,omitempty"`
}

func (q *Queries) CounterRebuildStatus(ctx context.Context) (CounterRebuildStatus, error) {
	var out CounterRebuildStatus
	var phase string
	var completed sql.NullInt64
	var failed bool
	err := q.db.QueryRowContext(ctx, `SELECT phase,last_scan_completed_at,scan_error,(SELECT COUNT(*) FROM counter_rebuild_jobs),(SELECT COUNT(*) FROM counter_rebuild_jobs WHERE state='failed'),(SELECT COUNT(*) FROM counter_rebuild_jobs WHERE state='busy') FROM counter_rebuild_progress WHERE id=1`).Scan(&phase, &completed, &failed, &out.PendingCount, &out.FailedCount, &out.BusyCount)
	if err != nil {
		return out, err
	}
	out.ScanPending = phase != "idle" || out.PendingCount > 0 || !completed.Valid || failed || q.counterRebuildFailure.Load() != nil
	out.State = "checked"
	if out.ScanPending {
		out.State = "pending"
	}
	if failed || q.counterRebuildFailure.Load() != nil {
		out.ScanErrorCode = "scan_failed"
	}
	if out.FailedCount > 0 || out.ScanErrorCode != "" {
		out.State = "degraded"
	}
	if completed.Valid {
		stamp := time.Unix(completed.Int64, 0).UTC()
		out.LastScanCompletedAt = &stamp
	}
	return out, nil
}

// Startup invalidates coverage but preserves partial accumulators and discovery
// progress. Only a finished pass starts over; repeated restarts cannot strand a
// large resource at its first page.
func (q *Queries) ResetCounterRebuild(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, `UPDATE counter_rebuild_progress SET phase=CASE WHEN phase='idle' THEN 'summaries' ELSE phase END,cursor=CASE WHEN phase='idle' THEN '' ELSE cursor END,cursor_kind=CASE WHEN phase='idle' THEN '' ELSE cursor_kind END,last_scan_completed_at=NULL WHERE id=1`)
	return err
}

type counterJob struct {
	kind, id, phase, cursor, child                          string
	revision, files, children, reserved, occupied, manifest int64
}

func readCounterJob(ctx context.Context, tx *sql.Tx) (counterJob, error) {
	var j counterJob
	err := tx.QueryRowContext(ctx, `SELECT kind,resource_id,expected_revision,phase,cursor,child,file_count,child_count,reserved_bytes,occupied_bytes,manifest_bytes FROM counter_rebuild_jobs WHERE next_retry_at<=? ORDER BY next_retry_at,turn,kind,resource_id LIMIT 1`, time.Now().Unix()).Scan(&j.kind, &j.id, &j.revision, &j.phase, &j.cursor, &j.child, &j.files, &j.children, &j.reserved, &j.occupied, &j.manifest)
	return j, err
}
func saveCounterJob(ctx context.Context, tx *sql.Tx, j counterJob, state string, delay int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE counter_rebuild_jobs SET expected_revision=?,phase=?,cursor=?,child=?,file_count=?,child_count=?,reserved_bytes=?,occupied_bytes=?,manifest_bytes=?,state=?,next_retry_at=?,turn=(SELECT turn FROM counter_rebuild_progress WHERE id=1) WHERE kind=? AND resource_id=?`, j.revision, j.phase, j.cursor, j.child, j.files, j.children, j.reserved, j.occupied, j.manifest, state, time.Now().Unix()+delay, j.kind, j.id)
	return err
}
func enqueueCounterJob(ctx context.Context, tx *sql.Tx, kind, id string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO counter_rebuild_sources(kind,resource_id) VALUES(?,?) ON CONFLICT(kind,resource_id) DO NOTHING`, kind, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO counter_rebuild_jobs(kind,resource_id,next_retry_at) VALUES(?,?,?) ON CONFLICT(kind,resource_id) DO NOTHING`, kind, id, time.Now().Unix())
	return err
}

// discoverCounterJobs spends at most eight source-row/probe units per call.
// New resources are found in authoritative tables, including missing summaries.
func discoverCounterJobs(ctx context.Context, tx *sql.Tx, budget *int) error {
	var phase, cursor, cursorKind string
	var queued int
	if err := tx.QueryRowContext(ctx, `SELECT phase,cursor,cursor_kind,(SELECT COUNT(*) FROM counter_rebuild_jobs) FROM counter_rebuild_progress WHERE id=1`).Scan(&phase, &cursor, &cursorKind, &queued); err != nil {
		return err
	}
	if phase == "waiting" {
		var retry bool
		if err := tx.QueryRowContext(ctx, `SELECT rescan_required=1 AND NOT EXISTS(SELECT 1 FROM counter_rebuild_jobs WHERE state='pending') FROM counter_rebuild_progress WHERE id=1`).Scan(&retry); err != nil {
			return err
		}
		if retry {
			phase = "summaries"
			cursor = ""
			cursorKind = ""
			if _, err := tx.ExecContext(ctx, `UPDATE counter_rebuild_progress SET phase='summaries',cursor='',cursor_kind='',rescan_required=0 WHERE id=1`); err != nil {
				return err
			}
		}
	}
	if phase == "waiting" || phase == "idle" {
		return nil
	}
	if queued >= CounterRebuildMaxJobs {
		// A bounded queue must not make a permanently failing prefix hide every
		// later resource. Evicted retries are covered by another full source pass;
		// the persistent marker prevents falsely declaring complete coverage.
		result, err := tx.ExecContext(ctx, `DELETE FROM counter_rebuild_jobs WHERE (kind,resource_id) IN (SELECT kind,resource_id FROM counter_rebuild_jobs WHERE state IN ('failed','busy') ORDER BY turn,kind,resource_id LIMIT 1)`)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			queued--
			if _, err = tx.ExecContext(ctx, `UPDATE counter_rebuild_progress SET rescan_required=1,scan_error=1 WHERE id=1`); err != nil {
				return err
			}
		}
	}
	limit := min(8, *budget, CounterRebuildMaxJobs-queued)
	if limit < 1 {
		return nil
	}
	switch phase {
	case "summaries":
		kind := "cleanup"
		if cursor == "cleanup" {
			kind = "reconciliation"
		}
		if err := enqueueCounterJob(ctx, tx, kind, ""); err != nil {
			return err
		}
		*budget--
		if kind == "cleanup" {
			cursor = "cleanup"
		} else {
			phase = "transfer"
			cursor = ""
		}
	case "transfer", "slot":
		rows, err := tx.QueryContext(ctx, `SELECT id FROM `+phase+`s WHERE id>? ORDER BY id LIMIT ?`, cursor, limit)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		*budget -= max(1, len(ids))
		for _, id := range ids {
			if err = enqueueCounterJob(ctx, tx, phase, id); err != nil {
				return err
			}
			cursor = id
		}
		if len(ids) < limit {
			if phase == "transfer" {
				phase = "slot"
			} else {
				phase = "stale"
			}
			cursor = ""
			cursorKind = ""
		}
	case "stale":
		// Each row includes two bounded canonical existence probes.
		limit = min(limit, *budget/3)
		if limit < 1 {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.kind,c.resource_id,CASE WHEN c.kind='transfer' THEN EXISTS(SELECT 1 FROM transfers t WHERE t.id=c.resource_id) ELSE EXISTS(SELECT 1 FROM slots s WHERE s.id=c.resource_id) END FROM admin_resource_totals c WHERE (c.kind,c.resource_id)>(?,?) ORDER BY c.kind,c.resource_id LIMIT ?`, cursorKind, cursor, limit)
		if err != nil {
			return err
		}
		type entry struct {
			kind, id string
			exists   bool
		}
		entries := []entry{}
		for rows.Next() {
			var e entry
			if err = rows.Scan(&e.kind, &e.id, &e.exists); err != nil {
				rows.Close()
				return err
			}
			entries = append(entries, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		*budget -= max(1, 3*len(entries))
		for _, e := range entries {
			if !e.exists {
				if _, err = tx.ExecContext(ctx, `DELETE FROM admin_resource_totals WHERE kind=? AND resource_id=?`, e.kind, e.id); err != nil {
					return err
				}
			}
			cursorKind = e.kind
			cursor = e.id
		}
		if len(entries) < limit {
			phase = "waiting"
			cursor = ""
			cursorKind = ""
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE counter_rebuild_progress SET phase=?,cursor=?,cursor_kind=? WHERE id=1`, phase, cursor, cursorKind)
	return err
}

func addCounter(target *int64, value int64) error {
	if *target < 0 || value < 0 || value > math.MaxInt64-*target {
		return errors.New("invalid counter source value")
	}
	*target += value
	return nil
}
func validCounterAccumulators(j counterJob) bool {
	return j.files >= 0 && j.children >= 0 && j.reserved >= 0 && j.occupied >= 0 && j.manifest >= 0
}
func counterFilePage(ctx context.Context, tx *sql.Tx, j *counterJob, transfer string, budget *int) error {
	limit := *budget
	rows, err := tx.QueryContext(ctx, `SELECT id,size,upload_offset,payload_deleted FROM files WHERE transfer_id=? AND id>? ORDER BY id LIMIT ?`, transfer, j.cursor, limit)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var id string
		var size, offset int64
		var deleted bool
		if err = rows.Scan(&id, &size, &offset, &deleted); err != nil {
			rows.Close()
			return err
		}
		count++
		j.cursor = id
		if size < 0 || offset < 0 || offset > size {
			rows.Close()
			return errors.New("invalid file accounting")
		}
		if err = addCounter(&j.files, 1); err != nil {
			rows.Close()
			return err
		}
		if !deleted {
			if err = addCounter(&j.reserved, size); err != nil {
				rows.Close()
				return err
			}
			if err = addCounter(&j.occupied, offset); err != nil {
				rows.Close()
				return err
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	*budget -= max(1, count)
	if count < limit {
		j.cursor = ""
		if j.kind == "slot" {
			j.phase = "children"
		} else {
			j.phase = "ready"
		}
	}
	return nil
}
func counterManifest(ctx context.Context, tx *sql.Tx, j *counterJob, transfer string, budget *int) error {
	var size int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT length(data) FROM manifests WHERE transfer_id=?),0)`, transfer).Scan(&size); err != nil {
		return err
	}
	*budget--
	if err := addCounter(&j.reserved, size); err != nil {
		return err
	}
	if err := addCounter(&j.occupied, size); err != nil {
		return err
	}
	if err := addCounter(&j.manifest, size); err != nil {
		return err
	}
	j.phase = "files"
	return nil
}
func counterSummaryPage(ctx context.Context, tx *sql.Tx, j *counterJob, budget *int) error {
	limit := *budget
	query := `SELECT file_id,'',category FROM file_reconciliation_issues WHERE file_id>? ORDER BY file_id LIMIT ?`
	args := []any{j.cursor, limit}
	if j.kind == "cleanup" {
		query = `SELECT resource_id,kind,state FROM cleanup_tasks WHERE (kind,resource_id)>(?,?) ORDER BY kind,resource_id LIMIT ?`
		args = []any{j.child, j.cursor, limit}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var id, kind, state string
		if err = rows.Scan(&id, &kind, &state); err != nil {
			rows.Close()
			return err
		}
		count++
		j.cursor = id
		j.child = kind
		if err = addCounter(&j.files, 1); err != nil {
			rows.Close()
			return err
		}
		if state == "failed" {
			if err = addCounter(&j.reserved, 1); err != nil {
				rows.Close()
				return err
			}
		}
		if state == "busy" || state == "waiting_children" {
			if err = addCounter(&j.occupied, 1); err != nil {
				rows.Close()
				return err
			}
		}
		if state == "unavailable" {
			if err = addCounter(&j.manifest, 1); err != nil {
				rows.Close()
				return err
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	*budget -= max(1, count)
	if count < limit {
		j.phase = "ready"
	}
	return nil
}
func publishCounterJob(ctx context.Context, tx *sql.Tx, j counterJob) error {
	// The writer transaction holds the revision check through publication.
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM counter_rebuild_sources WHERE kind=? AND resource_id=?`, j.kind, j.id).Scan(&revision); err != nil {
		return err
	}
	if revision != j.revision {
		return errors.New("source revision changed")
	}
	var err error
	var result sql.Result
	switch j.kind {
	case "transfer", "slot":
		result, err = tx.ExecContext(ctx, `INSERT INTO admin_resource_totals(kind,resource_id,file_count,child_transfer_count,reserved_bytes,occupied_bytes,manifest_bytes) VALUES(?,?,?,?,?,?,?) ON CONFLICT(kind,resource_id) DO UPDATE SET file_count=excluded.file_count,child_transfer_count=excluded.child_transfer_count,reserved_bytes=excluded.reserved_bytes,occupied_bytes=excluded.occupied_bytes,manifest_bytes=excluded.manifest_bytes`, j.kind, j.id, j.files, j.children, j.reserved, j.occupied, j.manifest)
	case "cleanup":
		result, err = tx.ExecContext(ctx, `INSERT INTO cleanup_progress(id,pending_count,failed_count,busy_count) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET pending_count=excluded.pending_count,failed_count=excluded.failed_count,busy_count=excluded.busy_count`, j.files, j.reserved, j.occupied)
	case "reconciliation":
		result, err = tx.ExecContext(ctx, `INSERT INTO file_reconciliation_progress(id,busy_count,unavailable_count,failed_count) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET busy_count=excluded.busy_count,unavailable_count=excluded.unavailable_count,failed_count=excluded.failed_count`, j.occupied, j.manifest, j.reserved)
	}
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("invalid counter publication")
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("counter target missing")
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM counter_rebuild_jobs WHERE kind=? AND resource_id=?`, j.kind, j.id)
	return err
}
func advanceCounterJob(ctx context.Context, tx *sql.Tx, j counterJob, budget *int) error {
	if !validCounterAccumulators(j) {
		return errors.New("invalid staged counters")
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM counter_rebuild_sources WHERE kind=? AND resource_id=?`, j.kind, j.id).Scan(&revision); err != nil {
		return err
	}
	if j.revision >= 0 && revision != j.revision {
		// Requeue at the tail; repeated changes cannot monopolize the worker.
		reset := counterJob{kind: j.kind, id: j.id, revision: -1, phase: "start"}
		return saveCounterJob(ctx, tx, reset, "busy", 1)
	}
	if j.revision < 0 {
		j.revision = revision
		switch j.kind {
		case "transfer":
			j.phase = "manifest"
		case "slot":
			j.phase = "children"
		default:
			j.phase = "summary"
		}
	}
	for *budget > 0 && j.phase != "ready" {
		switch j.phase {
		case "manifest":
			transfer := j.id
			if j.kind == "slot" {
				transfer = j.child
			}
			if err := counterManifest(ctx, tx, &j, transfer, budget); err != nil {
				return err
			}
		case "files":
			transfer := j.id
			if j.kind == "slot" {
				transfer = j.child
			}
			if err := counterFilePage(ctx, tx, &j, transfer, budget); err != nil {
				return err
			}
		case "children":
			var child string
			err := tx.QueryRowContext(ctx, `SELECT transfer_id FROM slot_transfers WHERE slot_id=? AND transfer_id>? ORDER BY transfer_id LIMIT 1`, j.id, j.child).Scan(&child)
			*budget--
			if errors.Is(err, sql.ErrNoRows) {
				j.phase = "ready"
				continue
			}
			if err != nil {
				return err
			}
			j.child = child
			if err = addCounter(&j.children, 1); err != nil {
				return err
			}
			j.phase = "manifest"
			j.cursor = ""
		case "summary":
			if err := counterSummaryPage(ctx, tx, &j, budget); err != nil {
				return err
			}
		default:
			return errors.New("invalid counter job phase")
		}
	}
	if j.phase == "ready" {
		return publishCounterJob(ctx, tx, j)
	}
	return saveCounterJob(ctx, tx, j, "pending", 0)
}

func (q *Queries) recordCounterRebuildFailure(ctx context.Context, j *counterJob) {
	retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	if j != nil {
		if result, err := q.db.ExecContext(retryCtx, `UPDATE counter_rebuild_jobs SET state='failed',next_retry_at=?,turn=(SELECT turn FROM counter_rebuild_progress WHERE id=1)+1 WHERE kind=? AND resource_id=?`, time.Now().Add(5*time.Second).Unix(), j.kind, j.id); err == nil {
			if n, e := result.RowsAffected(); e == nil && n == 1 {
				return
			}
		}
	}
	q.counterRebuildFailureMarker()
	_, _ = q.db.ExecContext(retryCtx, `UPDATE counter_rebuild_progress SET scan_error=1 WHERE id=1`)
}
func (q *Queries) counterRebuildFailureMarker() {
	q.counterRebuildFailure.Store(&counterFailureMarker{1})
}

// RebuildCounterBatch performs at most budget canonical source-row/probe units
// (4–64), plus fixed bookkeeping over a queue capped at 64 jobs. A short
// writer transaction serializes each chunk with live mutations; no resource lock
// or transaction is retained across calls.
func (q *Queries) RebuildCounterBatch(ctx context.Context, budget int) (err error) {
	if budget < 4 || budget > CounterRebuildMaxWork {
		return errors.New("invalid counter reconstruction budget")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Healthy completed passes stay read-only. A failed idle probe requires a
	// new verification pass once storage recovers; retain any staged jobs.
	var idle, scanFailed, hasJobs bool
	marker := q.counterRebuildFailure.Load()
	if err = q.db.QueryRowContext(ctx, `SELECT phase='idle',scan_error,EXISTS(SELECT 1 FROM counter_rebuild_jobs) FROM counter_rebuild_progress WHERE id=1`).Scan(&idle, &scanFailed, &hasJobs); err != nil {
		q.recordCounterRebuildFailure(ctx, nil)
		return ErrCounterRebuild
	}
	if idle && !scanFailed && !hasJobs && marker == nil {
		return nil
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		q.recordCounterRebuildFailure(ctx, nil)
		return ErrCounterRebuild
	}
	defer tx.Rollback()
	var job *counterJob
	fail := func(cause error) error {
		_ = tx.Rollback()
		q.recordCounterRebuildFailure(ctx, job)
		return errors.Join(ErrCounterRebuild, ctx.Err())
	}
	if _, err = tx.ExecContext(ctx, `UPDATE counter_rebuild_progress SET turn=turn+1,phase=CASE WHEN phase='idle' THEN 'summaries' ELSE phase END,cursor=CASE WHEN phase='idle' THEN '' ELSE cursor END,cursor_kind=CASE WHEN phase='idle' THEN '' ELSE cursor_kind END,last_scan_completed_at=CASE WHEN phase='idle' THEN NULL ELSE last_scan_completed_at END WHERE id=1`); err != nil {
		return fail(err)
	}
	if err = discoverCounterJobs(ctx, tx, &budget); err != nil {
		return fail(err)
	}
	if budget > 0 {
		candidate, e := readCounterJob(ctx, tx)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return fail(e)
		}
		if e == nil {
			job = &candidate
			if _, err = tx.ExecContext(ctx, `SAVEPOINT counter_job`); err != nil {
				return fail(err)
			}
			if err = advanceCounterJob(ctx, tx, candidate, &budget); err != nil {
				// Keep discovery/new jobs while discarding only the failed chunk. This
				// allows later resources to progress even when a new job cannot publish.
				if _, e = tx.ExecContext(ctx, `ROLLBACK TO counter_job`); e != nil {
					return fail(e)
				}
				if _, e = tx.ExecContext(ctx, `RELEASE counter_job`); e != nil {
					return fail(e)
				}
				if e = saveCounterJob(ctx, tx, candidate, "failed", 5); e != nil {
					return fail(e)
				}
				if e = tx.Commit(); e != nil {
					return fail(e)
				}
				return ErrCounterRebuild
			}
			if _, err = tx.ExecContext(ctx, `RELEASE counter_job`); err != nil {
				return fail(err)
			}
		}
	}
	var completed bool
	err = tx.QueryRowContext(ctx, `UPDATE counter_rebuild_progress SET phase='idle',last_scan_completed_at=?,scan_error=0 WHERE id=1 AND phase='waiting' AND rescan_required=0 AND NOT EXISTS(SELECT 1 FROM counter_rebuild_jobs) RETURNING 1`, time.Now().Unix()).Scan(&completed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	if completed {
		q.counterRebuildFailure.CompareAndSwap(marker, nil)
	}
	return nil
}
