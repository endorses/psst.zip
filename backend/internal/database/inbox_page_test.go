package database

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func inboxSummary(t *testing.T, q *Queries, id string, files, completed, size int64) {
	t.Helper()
	page, err := q.OwnerInboxPage(context.Background(), id, "", 1, "", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := page.Summary
	if s.State != "ready" || s.FileCount == nil || s.CompletedFiles == nil || s.TotalSize == nil || *s.FileCount != files || *s.CompletedFiles != completed || *s.TotalSize != size {
		t.Fatalf("unexpected summary %+v, want %d/%d/%d", s, files, completed, size)
	}
}
func TestInboxPageBoundsRawRowsAndAdvancesEmptyPages(t *testing.T) {
	q, _ := resourceFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateSlot("inbox", until, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 137; i++ {
		id := fmt.Sprintf("child-%03d", i)
		if err := q.CreateTransfer(id, until, 0, nil); err != nil {
			t.Fatal(err)
		}
		if err := q.LinkSlotTransfer("inbox", id); err != nil {
			t.Fatal(err)
		}
		if i < 100 {
			column := "expires_at"
			if i%2 == 0 {
				column = "pending_expires_at"
			}
			if _, err := q.db.Exec(`UPDATE transfers SET `+column+`='2000-01-01' WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	page, err := q.OwnerInboxPage(context.Background(), "inbox", "", 50, "", false, time.Now())
	if err != nil || len(page.Transfers) != 0 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	seen := map[string]bool{}
	pages := 1
	for page.NextCursor != nil {
		page, err = q.OwnerInboxPage(context.Background(), "inbox", "", 50, *page.NextCursor, false, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, item := range page.Transfers {
			if seen[item.TransferID] {
				t.Fatal("duplicate", item)
			}
			seen[item.TransferID] = true
		}
	}
	if pages != 3 || len(seen) != 37 {
		t.Fatal(pages, len(seen))
	}
	if _, err = q.OwnerInboxPage(context.Background(), "inbox", "", 100, "", true, time.Now()); !errors.Is(err, ErrInboxPaginationRequired) {
		t.Fatal("legacy silently truncated", err)
	}
	// The seek uses the covering membership primary key, even when source rows
	// exceed the current submission cap. No status filter defeats its row limit.
	rows, err := q.db.Query(`EXPLAIN QUERY PLAN SELECT transfer_id FROM slot_transfers WHERE slot_id=? AND transfer_id>? ORDER BY transfer_id LIMIT ?`, "inbox", "child-050", 51)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, rows)
	var plan string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if !strings.Contains(plan, "SEARCH slot_transfers USING COVERING INDEX") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal(plan)
	}
}
func TestInboxPageCursorCancellationAndOversizedChild(t *testing.T) {
	q, _ := resourceFixture(t)
	until := time.Now().Add(time.Hour)
	for _, id := range []string{"one", "two"} {
		if err := q.CreateSlot(id, until, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b"} {
		if err := q.CreateTransfer(id, until, 0, nil); err != nil {
			t.Fatal(err)
		}
		if err := q.LinkSlotTransfer("one", id); err != nil {
			t.Fatal(err)
		}
	}
	page, err := q.OwnerInboxPage(context.Background(), "one", "", 1, "", false, time.Now())
	if err != nil || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	for _, cursor := range []string{*page.NextCursor + "=", "!", strings.Repeat("a", 513), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"slot":"one","after":"a","extra":1}`))} {
		if _, err = q.OwnerInboxPage(context.Background(), "one", "", 1, cursor, false, time.Now()); !errors.Is(err, ErrInvalidPage) {
			t.Fatal("accepted bad cursor", cursor, err)
		}
	}
	if _, err = q.OwnerInboxPage(context.Background(), "two", "", 1, *page.NextCursor, false, time.Now()); !errors.Is(err, ErrInvalidPage) {
		t.Fatal("accepted cross-inbox cursor", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = q.OwnerInboxPage(ctx, "one", "", 1, "", false, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		if err = q.CreateFile(fmt.Sprint(i), "a", 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = q.OwnerInboxPage(context.Background(), "one", "", 1, "", false, time.Now()); !errors.Is(err, ErrTransferFileLimit) {
		t.Fatal("oversized child silently truncated", err)
	}
	// Hidden children do not incur file probes or disclose their oversized state.
	if _, err = q.db.Exec(`UPDATE transfers SET status='revoked' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	page, err = q.OwnerInboxPage(context.Background(), "one", "", 1, "", false, time.Now())
	if err != nil || len(page.Transfers) != 0 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
}
func TestInboxTotalsTrackCompletionMembershipAndDeletion(t *testing.T) {
	q, _ := adminResourceFixture(t)
	if err := q.LinkSlotTransfer("one", "a"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "a", 60); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "one", 1, 0, 60)
	if err := q.UpdateFileOffset("file", 60, true); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "one", 1, 0, 60)
	if _, err := q.db.Exec(`UPDATE transfers SET status='complete' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "one", 1, 1, 60)
	if _, err := q.db.Exec(`UPDATE files SET upload_complete=0 WHERE id='file'`); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "one", 1, 0, 60)
	if _, err := q.db.Exec(`UPDATE files SET upload_complete=1,payload_deleted=1 WHERE id='file'`); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "one", 1, 1, 60)
	if _, err := q.db.Exec(`UPDATE transfers SET expires_at='2000-01-01' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "one", 1, 1, 60) // retained, not wall-clock-filtered totals
	if _, err := q.db.Exec(`UPDATE slot_transfers SET slot_id='two' WHERE slot_id='one'`); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "one", 0, 0, 0)
	inboxSummary(t, q, "two", 1, 1, 60)
	if _, err := q.db.Exec(`UPDATE transfers SET status='revoked' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "two", 1, 0, 60)
	if err := q.DeleteTransfer("a"); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "two", 0, 0, 0)
	// Cascading a completed child must remove its completion contribution too.
	if err := q.LinkSlotTransfer("two", "b"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("other", "b", 80); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("other", 80, true); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE transfers SET status='complete' WHERE id='b'`); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteTransfer("b"); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "two", 0, 0, 0)
	counterDrain(t, q)
	inboxSummary(t, q, "two", 0, 0, 0)
}
func TestInboxTotalsHistoricalMigrationAndBoundedRebuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, old)
	for version, migration := range migrations[:len(migrations)-1] {
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
	until := time.Now().Add(time.Hour)
	if err = q.CreateSlot("inbox", until, nil); err != nil {
		t.Fatal(err)
	}
	if err = q.CreateTransfer("child", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err = q.LinkSlotTransfer("inbox", "child"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 90; i++ {
		id := fmt.Sprint(i)
		if err = q.CreateFile(id, "child", 60); err != nil {
			t.Fatal(err)
		}
		if err = q.UpdateFileOffset(id, 60, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = old.Exec(`UPDATE transfers SET status='complete' WHERE id='child'`); err != nil {
		t.Fatal(err)
	}
	closeFixture(t, old)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	q = NewQueries(db)
	page, err := q.OwnerInboxPage(context.Background(), "inbox", "", 10, "", false, time.Now())
	if err != nil || page.Summary.State != "updating" || page.Summary.FileCount != nil || page.Summary.CompletedFiles != nil || page.Summary.TotalSize != nil {
		t.Fatal(page, err)
	}
	if err = q.RebuildCounterBatch(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	page, err = q.OwnerInboxPage(context.Background(), "inbox", "", 10, "", false, time.Now())
	if err != nil || page.Summary.State != "updating" {
		t.Fatal("historical summary was fabricated", page, err)
	}
	counterDrain(t, q)
	inboxSummary(t, q, "inbox", 90, 90, 5400)
	if _, err = db.Exec(`UPDATE files SET upload_complete=0 WHERE id='0'`); err != nil {
		t.Fatal(err)
	}
	inboxSummary(t, q, "inbox", 90, 89, 5400)
	// A restart retains the exact summary and partial reconstruction safely.
	if err = q.ResetCounterRebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	counterDrain(t, q)
	inboxSummary(t, q, "inbox", 90, 89, 5400)
}

func TestInboxUnknownChildMembershipWakesBoundedRepair(t *testing.T) {
	q, _ := adminResourceFixture(t)
	if err := q.CreateFile("file", "a", 60); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("file", 60, true); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE transfers SET status='complete' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	counterDrain(t, q)
	if _, err := q.db.Exec(`UPDATE admin_resource_totals SET inbox_known=0 WHERE kind='transfer' AND resource_id='a'`); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer("one", "a"); err != nil {
		t.Fatal(err)
	}
	page, err := q.OwnerInboxPage(context.Background(), "one", "", 50, "", false, time.Now())
	if err != nil || page.Summary.State != "updating" {
		t.Fatal(page, err)
	}
	status, err := q.CounterRebuildStatus(context.Background())
	if err != nil || !status.ScanPending {
		t.Fatal("unknown summary did not wake repair", status, err)
	}
	counterDrain(t, q)
	inboxSummary(t, q, "one", 1, 1, 60)
	// A contradictory published summary cannot override bounded canonical page
	// observations. No lifetime counters are inferred from these derived values.
	if _, err := q.db.Exec(`UPDATE admin_resource_totals SET file_count=0,completed_files=0,total_file_bytes=0 WHERE kind='slot' AND resource_id='one'`); err != nil {
		t.Fatal(err)
	}
	page, err = q.OwnerInboxPage(context.Background(), "one", "", 50, "", false, time.Now())
	if err != nil || page.Summary.State != "updating" {
		t.Fatal("contradictory summary advertised ready", page, err)
	}
}

func TestInboxRebuildRestartsPartialCompletionSnapshotAfterMutation(t *testing.T) {
	q, path := resourceFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateSlot("inbox", until, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("child", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer("inbox", "child"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 90; i++ {
		id := fmt.Sprintf("f%03d", i)
		if err := q.CreateFile(id, "child", 60); err != nil {
			t.Fatal(err)
		}
		if err := q.UpdateFileOffset(id, 60, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.db.Exec(`UPDATE admin_resource_totals SET inbox_known=0`); err != nil {
		t.Fatal(err)
	}
	staged := false
	for i := 0; i < 30; i++ {
		if err := q.RebuildCounterBatch(context.Background(), 16); err != nil {
			t.Fatal(err)
		}
		if err := q.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM counter_rebuild_jobs WHERE kind='transfer' AND resource_id='child' AND phase='files' AND file_count>0)`).Scan(&staged); err != nil {
			t.Fatal(err)
		}
		if staged {
			break
		}
	}
	if !staged {
		t.Fatal("did not stage a partial file scan")
	}
	if _, err := q.db.Exec(`UPDATE files SET upload_complete=0 WHERE id='f000'; UPDATE transfers SET status='complete' WHERE id='child'`); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, other)
	restarted := NewQueries(other)
	if err = restarted.ResetCounterRebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		counterForceDue(t, restarted)
		if err = restarted.RebuildCounterBatch(context.Background(), 16); err != nil {
			t.Fatal(err)
		}
		status, e := restarted.CounterRebuildStatus(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if !status.ScanPending {
			inboxSummary(t, restarted, "inbox", 90, 89, 5400)
			return
		}
	}
	t.Fatal("completion revision change did not converge after restart")
}
