package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestReconciliationRepairGuardsAllMetadataBeforePhysicalMutation(t *testing.T) {
	for _, change := range []string{"offset", "size", "complete", "published", "revoked", "cleanup", "retired", "deleted"} {
		t.Run(change, func(t *testing.T) {
			q, _ := resourceFixture(t)
			ctx := context.Background()
			if err := q.CreateTransfer("t", time.Now().Add(time.Hour), 0, nil); err != nil {
				t.Fatal(err)
			}
			if err := q.CreateFile("f", "t", 8); err != nil {
				t.Fatal(err)
			}
			if err := q.UpdateFileOffset("f", 4, false); err != nil {
				t.Fatal(err)
			}
			expected, err := q.GetFile("f")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "offset":
				_, err = q.db.Exec(`UPDATE files SET upload_offset=5 WHERE id='f'`)
			case "size":
				_, err = q.db.Exec(`UPDATE files SET size=9 WHERE id='f'`)
			case "complete":
				_, err = q.db.Exec(`UPDATE files SET upload_complete=1 WHERE id='f'`)
			case "published":
				_, err = q.db.Exec(`UPDATE transfers SET status='complete' WHERE id='t'`)
			case "revoked":
				err = q.RevokeTransfer("t")
			case "cleanup":
				_, err = q.db.Exec(`INSERT INTO cleanup_tasks(kind,resource_id,mode,reason,pending_since) VALUES('transfer','t','full','expired',0)`)
			case "retired":
				err = q.ReleaseCleanedPayloads("t")
			case "deleted":
				err = q.DeleteTransfer("t")
			}
			if err != nil {
				t.Fatal(err)
			}
			called := false
			repaired, err := q.RepairPendingFile(ctx, *expected, 2, false, func() error { called = true; return nil })
			if err != nil || repaired || called {
				t.Fatal("stale repair mutated payload", repaired, called, err)
			}
		})
	}
}
func TestReconciliationRepairRollsBackAndSerializesPublication(t *testing.T) {
	q, path := resourceFixture(t)
	ctx := context.Background()
	if err := q.CreateTransfer("t", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("f", "t", 8); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("f", 8, true); err != nil {
		t.Fatal(err)
	}
	expected, err := q.GetFile("f")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.RepairPendingFile(ctx, *expected, 2, false, func() error { return errors.New("truncate failure") }); err == nil {
		t.Fatal("accepted failed truncate")
	}
	actual, _ := q.GetFile("f")
	if actual.UploadOffset != 8 || !actual.UploadComplete {
		t.Fatal(actual)
	}
	otherDB, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer otherDB.Close()
	other := NewQueries(otherDB)
	entered, finish := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	var repaired bool
	var repairErr error
	go func() {
		defer wg.Done()
		repaired, repairErr = q.RepairPendingFile(ctx, *expected, 2, false, func() error { close(entered); <-finish; return nil })
	}()
	<-entered
	published := make(chan error, 1)
	go func() { published <- other.CompleteTransfer("t") }()
	select {
	case err := <-published:
		t.Fatal("publication bypassed repair writer lock", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(finish)
	wg.Wait()
	if repairErr != nil || !repaired {
		t.Fatal(repaired, repairErr)
	}
	if err = <-published; err == nil {
		t.Fatal("published rewound incomplete upload")
	}
	actual, _ = q.GetFile("f")
	if actual.UploadOffset != 2 || actual.UploadComplete {
		t.Fatal(actual)
	}
}
func TestReconciliationIssuesAreBoundedTypedAndCascaded(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	if err := q.CreateTransfer("t", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := q.CreateFile(fmt.Sprint(i), "t", 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []struct{ id, category, code string }{{"0", "busy", "resource_busy"}, {"1", "unavailable", "payload_missing"}, {"2", "failed", "inspect_failed"}} {
		for i := 0; i < 3; i++ {
			if err := q.RecordFileReconciliationIssue(ctx, entry.id, entry.category, entry.code); err != nil {
				t.Fatal(err)
			}
		}
	}
	status, err := q.ReconciliationStatus(ctx)
	if err != nil || status.IssueCount != 3 || status.BusyCount != 1 || status.UnavailableCount != 1 || status.FailedCount != 1 {
		t.Fatal(status, err)
	}
	if err = q.RecordFileReconciliationIssue(ctx, "1", "failed", "truncate_failed"); err != nil {
		t.Fatal(err)
	}
	status, err = q.ReconciliationStatus(ctx)
	if err != nil || status.IssueCount != 3 || status.UnavailableCount != 0 || status.FailedCount != 2 {
		t.Fatal(status, err)
	}
	if err = q.RecordFileReconciliationIssue(ctx, "0", "failed", "secret-key/path"); err == nil {
		t.Fatal("accepted raw error")
	}
	if err = q.RecordFileReconciliationIssue(ctx, "missing", "failed", "inspect_failed"); err != nil {
		t.Fatal(err)
	}
	if err = q.DeleteTransfer("t"); err != nil {
		t.Fatal(err)
	}
	status, err = q.ReconciliationStatus(ctx)
	if err != nil || status.IssueCount != 0 {
		t.Fatal("cascade counter drift", status, err)
	}
}
func TestReconciliationPageAndStaleProgressAreBounded(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	if err := q.CreateTransfer("t", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 70; i++ {
		if err := q.CreateFile(fmt.Sprintf("f-%03d", i), "t", 0); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := q.TransferFileIDs(ctx, "t", "", 1000)
	if err != nil || len(ids) != 64 || ids[63] != "f-063" {
		t.Fatal(ids, err)
	}
	next, err := q.TransferFileIDs(ctx, "t", ids[63], 64)
	if err != nil || len(next) != 6 || next[0] != "f-064" {
		t.Fatal(next, err)
	}
	batch, err := q.NextReconciliationBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.ResetReconciliationScan(ctx); err != nil {
		t.Fatal(err)
	}
	if err = q.AdvanceReconciliationBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	again, err := q.NextReconciliationBatch(ctx)
	if err != nil || again.Before != "" || again.Generation == batch.Generation {
		t.Fatal(again, err)
	}
}

func TestReconciliationGlobalFailureSurvivesFailedPersistence(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	batch, err := q.NextReconciliationBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.AdvanceReconciliationBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if _, err = q.db.Exec(`CREATE TRIGGER reject_scan_failure BEFORE UPDATE OF scan_error ON file_reconciliation_progress WHEN NEW.scan_error=1 BEGIN SELECT RAISE(ABORT,'write unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err = q.MarkReconciliationScanFailure(ctx); err == nil {
		t.Fatal("expected status write failure")
	}
	status, err := q.ReconciliationStatus(ctx)
	if err != nil || status.State != "degraded" || status.ScanErrorCode != "scan_failed" {
		t.Fatal(status, err)
	}
}

func TestReconciliationPassCannotClearConcurrentUnpersistedFailure(t *testing.T) {
	for _, rows := range []int{0, 65} {
		t.Run(fmt.Sprint(rows), func(t *testing.T) {
			q, _ := resourceFixture(t)
			ctx := context.Background()
			if err := q.CreateTransfer("t", time.Now().Add(time.Hour), 0, nil); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < rows; i++ {
				if err := q.CreateFile(fmt.Sprintf("f-%03d", i), "t", 0); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := q.db.Exec(`CREATE TRIGGER reject_scan_failure BEFORE UPDATE OF scan_error ON file_reconciliation_progress WHEN NEW.scan_error=1 BEGIN SELECT RAISE(ABORT,'write unavailable'); END`); err != nil {
				t.Fatal(err)
			}
			// Hold this traversal's initial snapshot while another request reports a
			// failure. Its persistent write fails, so only the local epoch remembers it.
			first, err := q.NextReconciliationBatch(ctx)
			if err != nil {
				t.Fatal(err)
			}
			failed := make(chan error, 1)
			go func() { failed <- q.MarkReconciliationScanFailure(ctx) }()
			if err = <-failed; err == nil {
				t.Fatal("expected failed marker persistence")
			}
			if err = q.AdvanceReconciliationBatch(ctx, first); err != nil {
				t.Fatal(err)
			}
			if !first.Complete {
				last, err := q.NextReconciliationBatch(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !last.Complete {
					t.Fatal("unexpected extra page")
				}
				if err = q.AdvanceReconciliationBatch(ctx, last); err != nil {
					t.Fatal(err)
				}
			}
			status, err := q.ReconciliationStatus(ctx)
			if err != nil || status.State != "degraded" || status.ScanErrorCode != "scan_failed" {
				t.Fatal("pass erased newer request failure", status, err)
			}
			// Only a complete traversal that starts after that failure may clear it.
			for {
				batch, err := q.NextReconciliationBatch(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err = q.AdvanceReconciliationBatch(ctx, batch); err != nil {
					t.Fatal(err)
				}
				if batch.Complete {
					break
				}
			}
			status, err = q.ReconciliationStatus(ctx)
			if err != nil || status.State != "checked" || status.ScanErrorCode != "" {
				t.Fatal(status, err)
			}
		})
	}
}
