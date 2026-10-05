package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func counterTransfer(t *testing.T, q *Queries, id string) {
	t.Helper()
	if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
}
func counterDrain(t *testing.T, q *Queries) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if err := q.RebuildCounterBatch(context.Background(), 64); err != nil {
			t.Fatal(err)
		}
		status, err := q.CounterRebuildStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.PendingCount > 64 {
			t.Fatal("unbounded queue", status)
		}
		if !status.ScanPending {
			if status.State != "checked" || status.LastScanCompletedAt == nil {
				t.Fatal(status)
			}
			return
		}
	}
	t.Fatal("counter reconstruction failed to drain")
}
func counterForceDue(t *testing.T, q *Queries) {
	t.Helper()
	if _, err := q.db.Exec(`UPDATE counter_rebuild_jobs SET next_retry_at=0`); err != nil {
		t.Fatal(err)
	}
}
func TestCounterRebuildRepairsDerivedValuesAndPreservesLifetimeState(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	for _, id := range []string{"one", "two", "cleanup-a", "cleanup-b"} {
		counterTransfer(t, q, id)
	}
	if err := q.CreateSlot("slot", time.Now().Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err := q.LinkSlotTransfer("slot", id); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []struct {
		id, transfer string
		size, offset int64
	}{{"a", "one", 42, 11}, {"b", "two", 17, 17}} {
		if err := q.CreateFile(f.id, f.transfer, f.size); err != nil {
			t.Fatal(err)
		}
		if err := q.UpdateFileOffset(f.id, f.offset, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.SaveManifest("one", []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("two", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := q.RecordFileReconciliationIssue(ctx, "a", "busy", "resource_busy"); err != nil {
		t.Fatal(err)
	}
	if err := q.RecordFileReconciliationIssue(ctx, "b", "unavailable", "payload_missing"); err != nil {
		t.Fatal(err)
	}
	if err := q.ReleaseCleanedPayloads("two"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cleanup-a", "cleanup-b"} {
		if err := q.RevokeTransfer(id); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`UPDATE cleanup_tasks SET state='failed' WHERE resource_id='cleanup-a'`,
		`UPDATE cleanup_tasks SET state='waiting_children' WHERE resource_id='cleanup-b'`,
		`UPDATE admin_resource_totals SET file_count=999,child_transfer_count=999,reserved_bytes=999,occupied_bytes=999,manifest_bytes=999`,
		`DELETE FROM admin_resource_totals WHERE kind='transfer' AND resource_id='two'`,
		`INSERT INTO admin_resource_totals(kind,resource_id) VALUES('transfer','ghost')`,
		`UPDATE cleanup_progress SET pending_count=999,failed_count=999,busy_count=999,transfer_cursor='keep',slot_cursor='also-keep',transfer_discovered_at=123,slot_discovered_at=124`,
		`UPDATE file_reconciliation_progress SET busy_count=999,unavailable_count=999,failed_count=999,generation=7,cursor='keep',last_scan_completed_at=123,scan_error=1,pass_failed=1`,
		`UPDATE slots SET reserved_bytes=999999,reserved_files=55,upload_count=77 WHERE id='slot'`,
		`UPDATE files SET download_count=11`,
		`UPDATE transfers SET download_count=9`,
		`UPDATE traffic_retention SET uploaded_bytes=123456,downloaded_bytes=234567,archived_conservative_down=345678`,
	} {
		if _, err := q.db.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	counterDrain(t, q)
	assertResourceTotals(t, q, "transfer", "one", 1, 0, 45, 14, 3)
	assertResourceTotals(t, q, "transfer", "two", 1, 0, 5, 5, 5)
	assertResourceTotals(t, q, "slot", "slot", 2, 2, 50, 19, 8)
	var n int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM admin_resource_totals WHERE resource_id='ghost'`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	var pending, failed, busy int
	var cursor, slotCursor string
	var stamp int64
	if err := q.db.QueryRow(`SELECT pending_count,failed_count,busy_count,transfer_cursor,slot_cursor,transfer_discovered_at FROM cleanup_progress`).Scan(&pending, &failed, &busy, &cursor, &slotCursor, &stamp); err != nil || pending != 2 || failed != 1 || busy != 1 || cursor != "keep" || slotCursor != "also-keep" || stamp != 123 {
		t.Fatal(pending, failed, busy, cursor, slotCursor, stamp, err)
	}
	var unavailable, generation, scanError, passFailed int
	if err := q.db.QueryRow(`SELECT busy_count,unavailable_count,failed_count,generation,cursor,last_scan_completed_at,scan_error,pass_failed FROM file_reconciliation_progress`).Scan(&busy, &unavailable, &failed, &generation, &cursor, &stamp, &scanError, &passFailed); err != nil || busy != 1 || unavailable != 1 || failed != 0 || generation != 7 || cursor != "keep" || stamp != 123 || scanError != 1 || passFailed != 1 {
		t.Fatal(busy, unavailable, failed, generation, cursor, stamp, scanError, passFailed, err)
	}
	var reserved, files, uploads int64
	if err := q.db.QueryRow(`SELECT reserved_bytes,reserved_files,upload_count FROM slots WHERE id='slot'`).Scan(&reserved, &files, &uploads); err != nil || reserved != 999999 || files != 55 || uploads != 77 {
		t.Fatal("lifetime inbox counters changed", reserved, files, uploads, err)
	}
	for _, query := range []string{`SELECT COUNT(*) FROM files WHERE download_count!=11`, `SELECT COUNT(*) FROM transfers WHERE download_count!=9`} {
		if err := q.db.QueryRow(query).Scan(&n); err != nil || n != 0 {
			t.Fatal("download allowance changed", n, err)
		}
	}
	var up, down, archived int64
	if err := q.db.QueryRow(`SELECT uploaded_bytes,downloaded_bytes,archived_conservative_down FROM traffic_retention`).Scan(&up, &down, &archived); err != nil || up != 123456 || down != 234567 || archived != 345678 {
		t.Fatal("traffic counters changed", up, down, archived, err)
	}
}
func TestCounterRebuildRevisionConflictAndRestartPreserveBoundedWork(t *testing.T) {
	q, path := resourceFixture(t)
	ctx := context.Background()
	counterTransfer(t, q, "large")
	for i := 0; i < 150; i++ {
		if err := q.CreateFile(fmt.Sprintf("f-%03d", i), "large", 2); err != nil {
			t.Fatal(err)
		}
	}
	var cursor string
	var count int64
	for i := 0; i < 30; i++ {
		if err := q.RebuildCounterBatch(ctx, 64); err != nil {
			t.Fatal(err)
		}
		err := q.db.QueryRow(`SELECT cursor,file_count FROM counter_rebuild_jobs WHERE kind='transfer' AND resource_id='large' AND phase='files'`).Scan(&cursor, &count)
		if err == nil && count > 0 {
			break
		}
	}
	if count < 1 || count > 64 || cursor == "" {
		t.Fatal("unbounded or absent partial work", count, cursor)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	other := NewQueries(db)
	if err = other.ResetCounterRebuild(ctx); err != nil {
		t.Fatal(err)
	}
	var saved string
	var savedCount int64
	if err = other.db.QueryRow(`SELECT cursor,file_count FROM counter_rebuild_jobs WHERE kind='transfer' AND resource_id='large'`).Scan(&saved, &savedCount); err != nil || saved != cursor || savedCount != count {
		t.Fatal("startup discarded partial work", saved, savedCount, err)
	}
	if err = other.UpdateFileOffset("f-000", 1, false); err != nil {
		t.Fatal(err)
	}
	// Force this staged job's turn to exercise the revision comparison before publish.
	if _, err = q.db.Exec(`UPDATE counter_rebuild_jobs SET next_retry_at=0 WHERE resource_id='large'`); err != nil {
		t.Fatal(err)
	}
	if err = q.RebuildCounterBatch(ctx, 64); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = q.db.QueryRow(`SELECT state,file_count FROM counter_rebuild_jobs WHERE resource_id='large'`).Scan(&state, &savedCount); err != nil || state != "busy" || savedCount != 0 {
		t.Fatal("stale accumulator reused", state, savedCount, err)
	}
	counterForceDue(t, q)
	counterDrain(t, q)
	assertResourceTotals(t, q, "transfer", "large", 150, 0, 300, 1, 0)
}
func TestCounterRebuildSlotUsesCanonicalRowsAndBoundsEmptyChildren(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	if err := q.CreateSlot("slot", time.Now().Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 80; i++ {
		id := fmt.Sprintf("child-%03d", i)
		counterTransfer(t, q, id)
		if err := q.LinkSlotTransfer("slot", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.db.Exec(`UPDATE admin_resource_totals SET file_count=1000,reserved_bytes=999 WHERE kind='transfer'`); err != nil {
		t.Fatal(err)
	}
	// Stage slot directly so the test cannot accidentally rely on repaired child summaries.
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = enqueueCounterJob(ctx, tx, "slot", "slot"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = q.db.Exec(`UPDATE counter_rebuild_progress SET phase='waiting'`); err != nil {
		t.Fatal(err)
	}
	if err = q.RebuildCounterBatch(ctx, 64); err != nil {
		t.Fatal(err)
	}
	var children int
	var child string
	if err = q.db.QueryRow(`SELECT child_count,child FROM counter_rebuild_jobs WHERE kind='slot'`).Scan(&children, &child); err != nil || children < 1 || children > 22 || child == "" {
		t.Fatal("empty children escaped budget", children, child, err)
	}
	counterDrain(t, q)
	assertResourceTotals(t, q, "slot", "slot", 0, 80, 0, 0, 0)
}
func TestCounterRebuildPublishingFailureKeepsLaterResourcesProgressing(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	for i := 0; i < 70; i++ {
		counterTransfer(t, q, fmt.Sprintf("bad-%03d", i))
	}
	counterTransfer(t, q, "zzz-good")
	if _, err := q.db.Exec(`DELETE FROM admin_resource_totals`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`CREATE TRIGGER reject_bad_totals BEFORE INSERT ON admin_resource_totals WHEN NEW.resource_id LIKE 'bad-%' BEGIN SELECT RAISE(ABORT,'private path and key'); END`); err != nil {
		t.Fatal(err)
	}
	found := false
	for i := 0; i < 400; i++ {
		err := q.RebuildCounterBatch(ctx, 64)
		if err != nil && !errors.Is(err, ErrCounterRebuild) {
			t.Fatal(err)
		}
		if err != nil && strings.Contains(err.Error(), "private") {
			t.Fatal("leaked database failure", err)
		}
		status, err := q.CounterRebuildStatus(ctx)
		if err != nil || status.PendingCount > 64 {
			t.Fatal(status, err)
		}
		var n int
		if err = q.db.QueryRow(`SELECT COUNT(*) FROM admin_resource_totals WHERE resource_id='zzz-good'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("saturated failing queue stranded healthy resource")
	}
	status, err := q.CounterRebuildStatus(ctx)
	if err != nil || !status.ScanPending || status.State != "degraded" || status.LastScanCompletedAt != nil {
		t.Fatal("claimed omitted failures complete", status, err)
	}
	if _, err = q.db.Exec(`DROP TRIGGER reject_bad_totals`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		counterForceDue(t, q)
		if err = q.RebuildCounterBatch(ctx, 64); err != nil {
			t.Fatal(err)
		}
		status, err = q.CounterRebuildStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !status.ScanPending {
			break
		}
	}
	if status.ScanPending || status.State != "checked" {
		t.Fatal("retry pass never converged", status)
	}
	var n int
	if err = q.db.QueryRow(`SELECT COUNT(*) FROM admin_resource_totals WHERE kind='transfer'`).Scan(&n); err != nil || n != 71 {
		t.Fatal(n, err)
	}
}
func TestCounterSourceRevisionsTrackMembershipCascadesAndDoNotLeaveOrphans(t *testing.T) {
	q, _ := resourceFixture(t)
	for _, slot := range []string{"one", "two"} {
		if err := q.CreateSlot(slot, time.Now().Add(time.Hour), nil); err != nil {
			t.Fatal(err)
		}
	}
	counterTransfer(t, q, "child")
	if err := q.LinkSlotTransfer("one", "child"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "child", 5); err != nil {
		t.Fatal(err)
	}

	before := map[string]int64{}
	for _, slot := range []string{"one", "two"} {
		var value int64
		if err := q.db.QueryRow(`SELECT revision FROM counter_rebuild_sources WHERE kind='slot' AND resource_id=?`, slot).Scan(&value); err != nil {
			t.Fatal(err)
		}
		before[slot] = value
	}
	if _, err := q.db.Exec(`UPDATE slot_transfers SET slot_id='two' WHERE transfer_id='child'`); err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"one", "two"} {
		var value int64
		if err := q.db.QueryRow(`SELECT revision FROM counter_rebuild_sources WHERE kind='slot' AND resource_id=?`, slot).Scan(&value); err != nil || value <= before[slot] {
			t.Fatal("membership revision missed", slot, value, err)
		}
	}
	if _, err := q.db.Exec(`DELETE FROM transfers WHERE id='child'`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`DELETE FROM slots`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM counter_rebuild_sources WHERE kind IN ('transfer','slot')`).Scan(&n); err != nil || n != 0 {
		t.Fatal("orphan source revisions", n, err)
	}
}
func TestCounterRebuildRestoresMissingSingletons(t *testing.T) {
	q, _ := resourceFixture(t)
	counterTransfer(t, q, "one")
	if err := q.CreateFile("f", "one", 1); err != nil {
		t.Fatal(err)
	}
	if err := q.RecordFileReconciliationIssue(context.Background(), "f", "busy", "resource_busy"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE transfers SET status='revoked' WHERE id='one'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"cleanup_progress", "file_reconciliation_progress"} {
		if _, err := q.db.Exec(`DELETE FROM ` + table); err != nil {
			t.Fatal(err)
		}
	}
	counterDrain(t, q)
	var count, stamp int64
	var cursor string
	if err := q.db.QueryRow(`SELECT pending_count,transfer_cursor,transfer_discovered_at FROM cleanup_progress`).Scan(&count, &cursor, &stamp); err != nil || count != 1 || cursor != "" || stamp != 0 {
		t.Fatal(count, cursor, stamp, err)
	}
	var completed sql.NullInt64
	if err := q.db.QueryRow(`SELECT busy_count,cursor,last_scan_completed_at FROM file_reconciliation_progress`).Scan(&count, &cursor, &completed); err != nil || count != 1 || cursor != "" || completed.Valid {
		t.Fatal(count, cursor, completed, err)
	}
}
func TestCounterRebuildRejectsIdentityChangesAndAllowsWriterGuards(t *testing.T) {
	q, _ := resourceFixture(t)
	counterTransfer(t, q, "one")
	counterTransfer(t, q, "two")
	if err := q.CreateSlot("slot", time.Now().Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 80; i++ {
		if err := q.CreateFile(fmt.Sprintf("f-%03d", i), "one", 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.RecordFileReconciliationIssue(context.Background(), "f-000", "busy", "resource_busy"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE transfers SET status='revoked' WHERE id='two'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := q.RebuildCounterBatch(context.Background(), 4); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`UPDATE transfers SET id='renamed' WHERE id='one'`, `UPDATE slots SET id='renamed' WHERE id='slot'`,
		`UPDATE files SET id='before-cursor' WHERE id='f-079'`, `UPDATE cleanup_tasks SET resource_id='renamed' WHERE resource_id='two'`,
		`UPDATE file_reconciliation_issues SET file_id='f-001' WHERE file_id='f-000'`,
	} {
		if _, err := q.db.Exec(query); err == nil {
			t.Fatal("identity rename allowed", query)
		}
	}
	for _, query := range []string{`UPDATE transfers SET id=id`, `UPDATE slots SET id=id`, `UPDATE files SET id=id`, `UPDATE cleanup_tasks SET resource_id=resource_id`, `UPDATE file_reconciliation_issues SET file_id=file_id`} {
		if _, err := q.db.Exec(query); err != nil {
			t.Fatal("harmless guard rejected", err)
		}
	}
	counterDrain(t, q)
	assertResourceTotals(t, q, "transfer", "one", 80, 0, 80, 0, 0)
}
func TestCounterRebuildBudgetAndOverflow(t *testing.T) {
	q, _ := resourceFixture(t)
	counterTransfer(t, q, "one")
	for _, budget := range []int{-1, 0, 1, 2, 3, 65} {
		if err := q.RebuildCounterBatch(context.Background(), budget); err == nil {
			t.Fatal("unsupported budget", budget)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := q.RebuildCounterBatch(ctx, 64); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := q.RebuildCounterBatch(context.Background(), 4); err != nil {
			t.Fatal(err)
		}
		s, err := q.CounterRebuildStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !s.ScanPending {
			break
		}
		if i == 99 {
			t.Fatal("minimum budget made no progress")
		}
	}
	for _, test := range []struct{ old, add int64 }{{math.MaxInt64, 1}, {0, -1}, {-1, 1}} {
		n := test.old
		if err := addCounter(&n, test.add); err == nil || n != test.old {
			t.Fatal("unchecked arithmetic", test, n, err)
		}
	}
}

func TestCounterRebuildIdleMakesNoWrites(t *testing.T) {
	q, _ := resourceFixture(t)
	counterDrain(t, q)
	if _, err := q.db.Exec(`CREATE TRIGGER reject_idle_write BEFORE UPDATE ON counter_rebuild_progress BEGIN SELECT RAISE(ABORT,'unexpected idle write'); END`); err != nil {
		t.Fatal(err)
	}
	if err := q.RebuildCounterBatch(context.Background(), 64); err != nil {
		t.Fatal(err)
	}
}
func TestCounterRebuildSummaryRevisionAndInvalidSource(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	for i := 0; i < 70; i++ {
		id := fmt.Sprintf("cleanup-%03d", i)
		counterTransfer(t, q, id)
		if _, err := q.db.Exec(`UPDATE transfers SET status='revoked' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	// First chunk is an incomplete snapshot of the canonical cleanup queue.
	if err := q.RebuildCounterBatch(ctx, 64); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE cleanup_tasks SET state='failed' WHERE resource_id='cleanup-000'`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE counter_rebuild_jobs SET next_retry_at=0 WHERE kind='cleanup'`); err != nil {
		t.Fatal(err)
	}
	if err := q.RebuildCounterBatch(ctx, 64); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := q.db.QueryRow(`SELECT state FROM counter_rebuild_jobs WHERE kind='cleanup'`).Scan(&state); err != nil || state != "busy" {
		t.Fatal(state, err)
	}
	counterForceDue(t, q)
	counterDrain(t, q)
	var failed int
	if err := q.db.QueryRow(`SELECT failed_count FROM cleanup_progress`).Scan(&failed); err != nil || failed != 1 {
		t.Fatal(failed, err)
	}
	counterTransfer(t, q, "invalid")
	if err := q.CreateFile("bad-file", "invalid", 1); err != nil {
		t.Fatal(err)
	}
	// Retired payload size is not charged by the incremental trigger, but invalid
	// canonical metadata must still never become a successfully rebuilt summary.
	if _, err := q.db.Exec(`UPDATE files SET payload_deleted=1 WHERE id='bad-file'`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE files SET size=-1 WHERE id='bad-file'`); err != nil {
		t.Fatal(err)
	}
	if err := q.ResetCounterRebuild(ctx); err != nil {
		t.Fatal(err)
	}
	seen := false
	for i := 0; i < 150; i++ {
		err := q.RebuildCounterBatch(ctx, 64)
		if err != nil && !errors.Is(err, ErrCounterRebuild) {
			t.Fatal(err)
		}
		if errors.Is(err, ErrCounterRebuild) {
			seen = true
			break
		}
	}
	if !seen {
		t.Fatal("negative canonical size published")
	}
	status, err := q.CounterRebuildStatus(ctx)
	if err != nil || status.State != "degraded" || status.FailedCount != 1 || !status.ScanPending {
		t.Fatal(status, err)
	}
}

func TestCounterRebuildSummaryRechecksOnlyInvalidateChangedCounts(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	counterTransfer(t, q, "one")
	for i := 0; i < 70; i++ {
		id := fmt.Sprintf("f-%03d", i)
		if err := q.CreateFile(id, "one", 1); err != nil {
			t.Fatal(err)
		}
		if err := q.RecordFileReconciliationIssue(ctx, id, "busy", "resource_busy"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.db.Exec(`UPDATE transfers SET status='revoked' WHERE id='one'`); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"cleanup", "reconciliation"} {
		var before, after int64
		if err := q.db.QueryRow(`SELECT revision FROM counter_rebuild_sources WHERE kind=?`, kind).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if kind == "cleanup" {
			if _, err := q.db.Exec(`UPDATE cleanup_tasks SET state=state,last_attempt_at=unixepoch()`); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := q.RecordFileReconciliationIssue(ctx, "f-000", "busy", "resource_busy"); err != nil {
				t.Fatal(err)
			}
		}
		if err := q.db.QueryRow(`SELECT revision FROM counter_rebuild_sources WHERE kind=?`, kind).Scan(&after); err != nil || before != after {
			t.Fatal("same-state recheck invalidated counts", kind, before, after, err)
		}
	}
	// Force repeated timestamp-only rechecks between all bounded chunks.
	for i := 0; i < 100; i++ {
		if err := q.RebuildCounterBatch(ctx, 4); err != nil {
			t.Fatal(err)
		}
		if err := q.RecordFileReconciliationIssue(ctx, "f-000", "busy", "resource_busy"); err != nil {
			t.Fatal(err)
		}
		status, err := q.CounterRebuildStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !status.ScanPending {
			return
		}
	}
	t.Fatal("same-category rechecks prevented summary convergence")
}

func TestCounterRebuildIdleFailureResumesVerification(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(fmt.Sprintf("persisted=%v", persisted), func(t *testing.T) {
			q, _ := resourceFixture(t)
			ctx := context.Background()
			counterTransfer(t, q, "one")
			if err := q.CreateFile("f", "one", 7); err != nil {
				t.Fatal(err)
			}
			counterDrain(t, q)
			if persisted {
				if _, err := q.db.Exec(`UPDATE counter_rebuild_progress SET scan_error=1`); err != nil {
					t.Fatal(err)
				}
			} else {
				// Make the fixed preflight query fail, including persistence of its error.
				// SQLite updates trigger references during rename, so restoring the table
				// models recovered database access without dismantling trigger integrity.
				if _, err := q.db.Exec(`ALTER TABLE counter_rebuild_progress RENAME TO unavailable_progress`); err != nil {
					t.Fatal(err)
				}
				if err := q.RebuildCounterBatch(ctx, 64); !errors.Is(err, ErrCounterRebuild) {
					t.Fatal(err)
				}
				if _, err := q.db.Exec(`ALTER TABLE unavailable_progress RENAME TO counter_rebuild_progress`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := q.db.Exec(`UPDATE admin_resource_totals SET reserved_bytes=999 WHERE resource_id='one'`); err != nil {
				t.Fatal(err)
			}
			status, err := q.CounterRebuildStatus(ctx)
			if err != nil || status.State != "degraded" || !status.ScanPending {
				t.Fatal(status, err)
			}
			counterDrain(t, q)
			assertResourceTotals(t, q, "transfer", "one", 1, 0, 7, 0, 0)
			status, err = q.CounterRebuildStatus(ctx)
			if err != nil || status.ScanErrorCode != "" || status.State != "checked" || q.counterRebuildFailure.Load() != nil {
				t.Fatal(status, err)
			}
		})
	}
}

func TestCounterRebuildLateTransferRepairRechecksPublishedSlots(t *testing.T) {
	for _, test := range []struct {
		name           string
		unlink, delete bool
	}{{name: "link"}, {name: "unlink", unlink: true}, {name: "unlink_then_delete", unlink: true, delete: true}} {
		t.Run(test.name, func(t *testing.T) {
			q, _ := resourceFixture(t)
			ctx := context.Background()
			counterTransfer(t, q, "large")
			for i := 0; i < 150; i++ {
				if err := q.CreateFile(fmt.Sprintf("f-%03d", i), "large", 2); err != nil {
					t.Fatal(err)
				}
			}
			if err := q.CreateSlot("slot", time.Now().Add(time.Hour), nil); err != nil {
				t.Fatal(err)
			}
			if test.unlink {
				if err := q.LinkSlotTransfer("slot", "large"); err != nil {
					t.Fatal(err)
				}
			}
			corrupt := `UPDATE admin_resource_totals SET file_count=1,reserved_bytes=1 WHERE kind='transfer' AND resource_id='large'`
			if test.delete {
				// A missing child summary contributes zero to membership deltas and
				// allows the child to disappear before its pending repair publishes.
				corrupt = `DELETE FROM admin_resource_totals WHERE kind='transfer' AND resource_id='large'`
			}
			if _, err := q.db.Exec(corrupt); err != nil {
				t.Fatal(err)
			}
			// Start a real partial transfer scan, then defer its next chunk so both
			// membership variants deterministically finish the slot job first.
			staged := false
			for i := 0; i < 20; i++ {
				if err := q.RebuildCounterBatch(ctx, 64); err != nil {
					t.Fatal(err)
				}
				if err := q.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM counter_rebuild_jobs WHERE kind='transfer' AND resource_id='large' AND file_count>0)`).Scan(&staged); err != nil {
					t.Fatal(err)
				}
				if staged {
					break
				}
			}
			if !staged {
				t.Fatal("transfer did not preserve a partial scan")
			}
			if _, err := q.db.Exec(`UPDATE counter_rebuild_jobs SET next_retry_at=? WHERE kind='transfer' AND resource_id='large'`, time.Now().Add(time.Hour).Unix()); err != nil {
				t.Fatal(err)
			}
			published := false
			for i := 0; i < 20; i++ {
				if err := q.RebuildCounterBatch(ctx, 64); err != nil {
					t.Fatal(err)
				}
				if err := q.db.QueryRow(`SELECT phase IN ('stale','waiting') AND NOT EXISTS(SELECT 1 FROM counter_rebuild_jobs WHERE kind='slot') FROM counter_rebuild_progress`).Scan(&published); err != nil {
					t.Fatal(err)
				}
				if published {
					break
				}
			}
			if !published {
				t.Fatal("slot did not finish before transfer")
			}
			if test.unlink {
				assertResourceTotals(t, q, "slot", "slot", 150, 1, 300, 0, 0)
				if _, err := q.db.Exec(`DELETE FROM slot_transfers WHERE slot_id='slot' AND transfer_id='large'`); err != nil {
					t.Fatal(err)
				}
				if test.delete {
					assertResourceTotals(t, q, "slot", "slot", 150, 0, 300, 0, 0)
				} else {
					assertResourceTotals(t, q, "slot", "slot", 149, 0, 299, 0, 0)
				}
			} else {
				assertResourceTotals(t, q, "slot", "slot", 0, 0, 0, 0, 0)
				if err := q.LinkSlotTransfer("slot", "large"); err != nil {
					t.Fatal(err)
				}
				assertResourceTotals(t, q, "slot", "slot", 1, 1, 1, 0, 0)
			}
			if test.delete {
				if _, err := q.db.Exec(`DELETE FROM transfers WHERE id='large'`); err != nil {
					t.Fatal(err)
				}
			}
			counterForceDue(t, q)
			counterDrain(t, q)
			if !test.delete {
				assertResourceTotals(t, q, "transfer", "large", 150, 0, 300, 0, 0)
			}
			if test.unlink {
				assertResourceTotals(t, q, "slot", "slot", 0, 0, 0, 0, 0)
			} else {
				assertResourceTotals(t, q, "slot", "slot", 150, 1, 300, 0, 0)
			}
		})
	}
}

func TestCounterRebuildUnchangedLatePublicationConverges(t *testing.T) {
	q, _ := resourceFixture(t)
	counterTransfer(t, q, "large")
	for i := 0; i < 150; i++ {
		if err := q.CreateFile(fmt.Sprintf("f-%03d", i), "large", 2); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.CreateSlot("slot", time.Now().Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	counterDrain(t, q)
	assertResourceTotals(t, q, "transfer", "large", 150, 0, 300, 0, 0)
	assertResourceTotals(t, q, "slot", "slot", 0, 0, 0, 0, 0)
	if _, err := q.db.Exec(`CREATE TRIGGER reject_completed_counter_write BEFORE UPDATE ON counter_rebuild_progress BEGIN SELECT RAISE(ABORT,'unexpected completed-pass write'); END`); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer("slot", "large"); err != nil {
		t.Fatal("idle membership invalidated verified counters", err)
	}
	counterDrain(t, q)
	assertResourceTotals(t, q, "slot", "slot", 150, 1, 300, 0, 0)
}
