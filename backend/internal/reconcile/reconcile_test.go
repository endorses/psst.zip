package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

type fixture struct {
	q    *database.Queries
	disk *store.DiskStore
	db   *sql.DB
	path string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := store.NewDiskStore(filepath.Join(dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{database.NewQueries(db), disk, db, path}
	t.Cleanup(func() { f.db.Close() })
	return f
}
func (f *fixture) file(t *testing.T, transfer, id string, size, offset int64, complete bool, body *string) {
	t.Helper()
	if _, err := f.q.GetTransfer(transfer); err != nil {
		if err = f.q.CreateTransfer(transfer, time.Now().Add(time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.q.CreateFile(id, transfer, size); err != nil {
		t.Fatal(err)
	}
	if err := f.q.UpdateFileOffset(id, offset, complete); err != nil {
		t.Fatal(err)
	}
	if body != nil {
		if err := f.disk.Save(transfer+"/"+id, strings.NewReader(*body)); err != nil {
			t.Fatal(err)
		}
	}
}
func body(s string) *string { return &s }
func check(t *testing.T, f *fixture, fs store.FileStore, id string) error {
	t.Helper()
	file, err := f.q.GetFile(id)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := store.AcquireTransfer(context.Background(), file.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	return CheckFile(context.Background(), f.q, fs, id)
}
func assertFile(t *testing.T, f *fixture, id string, offset int64, complete bool, physical *int64) {
	t.Helper()
	file, err := f.q.GetFile(id)
	if err != nil || file.UploadOffset != offset || file.UploadComplete != complete {
		t.Fatalf("metadata %+v %v", file, err)
	}
	info, err := f.disk.Inspect(file.TransferID + "/" + id)
	if err != nil {
		t.Fatal(err)
	}
	if physical == nil {
		if info.Exists {
			t.Fatal("unexpected payload", info)
		}
	} else if !info.Exists || info.Size != *physical {
		t.Fatal("payload", info)
	}
}
func number(n int64) *int64 { return &n }
func TestPendingFilesReconcileOnlyCommittedPrefix(t *testing.T) {
	for _, tt := range []struct {
		name         string
		size, offset int64
		complete     bool
		body         *string
		wantOffset   int64
		wantComplete bool
		physical     *int64
	}{
		{"missing", 8, 4, false, nil, 0, false, nil},
		{"short", 8, 6, false, body("abc"), 3, false, number(3)},
		{"long", 8, 3, false, body("abcdef"), 3, false, number(3)},
		{"complete-short", 8, 8, true, body("abc"), 3, false, number(3)},
		{"missing-empty", 0, 0, true, nil, 0, false, nil},
		{"never-promote", 4, 0, false, body("abcd"), 0, false, number(0)},
		{"complete-tails", 4, 4, true, body("abcdef"), 4, true, number(4)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := setup(t)
			f.file(t, "transfer", "file", tt.size, tt.offset, tt.complete, tt.body)
			before, err := f.q.ResourceUsage("")
			if err != nil {
				t.Fatal(err)
			}
			if err = check(t, f, f.disk, "file"); err != nil {
				t.Fatal(err)
			}
			assertFile(t, f, "file", tt.wantOffset, tt.wantComplete, tt.physical)
			after, err := f.q.ResourceUsage("")
			if err != nil || after.ReservedBytes != before.ReservedBytes || after.Files != before.Files {
				t.Fatal("refunded reservation", before, after, err)
			}
			if _, err = f.q.FileReconciliationIssue(context.Background(), "file"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("resolved issue persisted", err)
			}
		})
	}
}
func TestPublishedMismatchNeverMutatesOrRefunds(t *testing.T) {
	for _, tt := range []struct {
		name string
		size int64
		body *string
		code string
	}{
		{"missing", 4, nil, "payload_missing"}, {"short", 4, body("ab"), "payload_size_mismatch"},
		{"long", 4, body("abcdef"), "payload_size_mismatch"}, {"missing-empty", 0, nil, "payload_missing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := setup(t)
			f.file(t, "published", "file", tt.size, tt.size, true, tt.body)
			if err := f.q.CompleteTransfer("published"); err != nil {
				t.Fatal(err)
			}
			before, _ := f.q.ResourceUsage("")
			if err := check(t, f, f.disk, "file"); !errors.Is(err, ErrPayloadUnavailable) {
				t.Fatal(err)
			}
			var physical *int64
			if tt.body != nil {
				physical = number(int64(len(*tt.body)))
			}
			assertFile(t, f, "file", tt.size, true, physical)
			after, _ := f.q.ResourceUsage("")
			if before != after {
				t.Fatal("published quota changed", before, after)
			}
			issue, err := f.q.FileReconciliationIssue(context.Background(), "file")
			if err != nil || issue.Code != tt.code || issue.Category != "unavailable" {
				t.Fatal(issue, err)
			}
			if err = f.q.UpdateFileOffset("file", 0, false); err != nil {
				t.Fatal(err)
			}
			if err = check(t, f, f.disk, "file"); !errors.Is(err, ErrPayloadUnavailable) {
				t.Fatal(err)
			}
			issue, err = f.q.FileReconciliationIssue(context.Background(), "file")
			if err != nil || issue.Code != "metadata_invalid" {
				t.Fatal(issue, err)
			}
		})
	}
}

type failingStore struct {
	store.FileStore
	inspectFail, truncateFail bool
	inspected                 int
}

func (f *failingStore) Inspect(key string) (store.PayloadInfo, error) {
	f.inspected++
	if f.inspectFail {
		return store.PayloadInfo{}, errors.New("private filename secret-key raw error")
	}
	return f.FileStore.Inspect(key)
}
func (f *failingStore) Truncate(key string, size int64) error {
	if f.truncateFail {
		return errors.New("private path secret-key")
	}
	return f.FileStore.Truncate(key, size)
}
func TestFailuresAreFixedDurableAndResolvedOnRetry(t *testing.T) {
	f := setup(t)
	f.file(t, "pending", "file", 8, 3, false, body("abcdef"))
	broken := &failingStore{FileStore: f.disk, inspectFail: true}
	if err := check(t, f, broken, "file"); !errors.Is(err, ErrStorageCheckFailed) || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	issue, err := f.q.FileReconciliationIssue(context.Background(), "file")
	if err != nil || issue.Code != "inspect_failed" {
		t.Fatal(issue, err)
	}
	broken.inspectFail = false
	broken.truncateFail = true
	if err = check(t, f, broken, "file"); !errors.Is(err, ErrStorageCheckFailed) {
		t.Fatal(err)
	}
	issue, err = f.q.FileReconciliationIssue(context.Background(), "file")
	if err != nil || issue.Code != "truncate_failed" {
		t.Fatal(issue, err)
	}
	assertFile(t, f, "file", 3, false, number(6))
	f.db.Close()
	f.db, err = database.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.q = database.NewQueries(f.db)
	status, err := f.q.ReconciliationStatus(context.Background())
	if err != nil || status.FailedCount != 1 || status.State != "degraded" {
		t.Fatal(status, err)
	}
	if err = check(t, f, f.disk, "file"); err != nil {
		t.Fatal(err)
	}
	status, err = f.q.ReconciliationStatus(context.Background())
	if err != nil || status.IssueCount != 0 {
		t.Fatal(status, err)
	}
	assertFile(t, f, "file", 3, false, number(3))
}
func TestCleanupIntentAndRetiredPayloadsAreNotRecreated(t *testing.T) {
	for _, mode := range []string{"revoked", "cleanup", "retired"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			f.file(t, "retiring", "file", 8, 8, true, nil)
			if err := f.q.RecordFileReconciliationIssue(context.Background(), "file", "unavailable", "payload_missing"); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "revoked":
				if err := f.q.RevokeTransfer("retiring"); err != nil {
					t.Fatal(err)
				}
			case "cleanup":
				if _, err := f.db.Exec(`INSERT INTO cleanup_tasks(kind,resource_id,mode,reason,pending_since) VALUES('transfer','retiring','full','expired',0)`); err != nil {
					t.Fatal(err)
				}
			case "retired":
				if err := f.q.ReleaseCleanedPayloads("retiring"); err != nil {
					t.Fatal(err)
				}
			}
			broken := &failingStore{FileStore: f.disk, inspectFail: true}
			if err := check(t, f, broken, "file"); err != nil || broken.inspected != 0 {
				t.Fatal("checked retiring payload", err, broken.inspected)
			}
			assertFile(t, f, "file", 8, true, nil)
			if _, err := f.q.FileReconciliationIssue(context.Background(), "file"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal(err)
			}
		})
	}
}
func TestSweepBoundedCursorRestartAndBusyFairness(t *testing.T) {
	f := setup(t)
	for i := 0; i < 70; i++ {
		transfer := "available"
		if i == 0 {
			transfer = "busy"
		}
		f.file(t, transfer, fmt.Sprintf("f-%03d", i), 4, 1, false, body("tail"))
	}
	unlock, err := store.AcquireTransfer(context.Background(), "busy")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	counted := &failingStore{FileStore: f.disk}
	if err = Sweep(context.Background(), f.q, counted); err != nil {
		t.Fatal(err)
	}
	if counted.inspected != 63 {
		t.Fatal("unbounded work", counted.inspected)
	}
	status, err := f.q.ReconciliationStatus(context.Background())
	if err != nil || status.BusyCount != 1 || !status.ScanPending || status.LastScanCompletedAt != nil {
		t.Fatal(status, err)
	}
	assertFile(t, f, "f-063", 1, false, number(1))
	assertFile(t, f, "f-064", 1, false, number(4))
	f.db.Close()
	f.db, err = database.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.q = database.NewQueries(f.db)
	if err = Sweep(context.Background(), f.q, counted); err != nil {
		t.Fatal(err)
	}
	if counted.inspected != 69 {
		t.Fatal("restart lost cursor", counted.inspected)
	}
	status, err = f.q.ReconciliationStatus(context.Background())
	if err != nil || status.LastScanCompletedAt == nil || status.State != "pending" || !status.ScanPending {
		t.Fatal(status, err)
	}
	unlock()
	if err = Sweep(context.Background(), f.q, counted); err != nil {
		t.Fatal(err)
	}
	if err = Sweep(context.Background(), f.q, counted); err != nil {
		t.Fatal(err)
	}
	status, err = f.q.ReconciliationStatus(context.Background())
	if err != nil || status.State != "checked" || status.IssueCount != 0 || status.ScanPending {
		t.Fatal(status, err)
	}
	if err = f.q.ResetReconciliationScan(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err = f.q.ReconciliationStatus(context.Background())
	if err != nil || status.State != "pending" || status.LastScanCompletedAt != nil {
		t.Fatal(status, err)
	}
}
func TestCanceledSweepAndWorkerPerformNoWork(t *testing.T) {
	f := setup(t)
	f.file(t, "pending", "file", 4, 1, false, body("tail"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Sweep(ctx, f.q, f.disk); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	Run(ctx, f.q, f.disk, time.Millisecond)
	assertFile(t, f, "file", 1, false, number(4))
}
func TestRepairedDataRetainsCommittedBytes(t *testing.T) {
	f := setup(t)
	f.file(t, "pending", "file", 10, 3, false, body("abcUNCOMMITTED"))
	if err := check(t, f, f.disk, "file"); err != nil {
		t.Fatal(err)
	}
	r, err := f.disk.Load("pending/file")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	bytes, err := io.ReadAll(r)
	if err != nil || string(bytes) != "abc" {
		t.Fatal(string(bytes), err)
	}
}

type slowInspectStore struct {
	store.FileStore
	calls int
}

func (s *slowInspectStore) Inspect(key string) (store.PayloadInfo, error) {
	s.calls++
	time.Sleep(25 * time.Millisecond)
	return s.FileStore.Inspect(key)
}
func TestSlowPrefixStillAdvancesAndExposesIncompleteScan(t *testing.T) {
	f := setup(t)
	for i := 0; i < 5; i++ {
		f.file(t, "pending", fmt.Sprintf("f-%d", i), 4, 1, false, body("tail"))
	}
	slow := &slowInspectStore{FileStore: f.disk}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := Sweep(ctx, f.q, slow); err == nil {
		t.Fatal("slow scan exceeded deadline without error")
	}
	next, err := f.q.NextReconciliationBatch(context.Background())
	if err != nil || next.Before == "" || len(next.Files) == 5 {
		t.Fatal("slow prefix stranded later rows", next, err)
	}
	status, err := f.q.ReconciliationStatus(context.Background())
	if err != nil || status.ScanErrorCode != "scan_failed" || status.State != "degraded" || status.LastScanCompletedAt != nil {
		t.Fatal(status, err)
	}
	// Finish that failed pass, then a complete healthy pass clears the scan marker.
	if err = Sweep(context.Background(), f.q, f.disk); err != nil {
		t.Fatal(err)
	}
	if err = Sweep(context.Background(), f.q, f.disk); err != nil {
		t.Fatal(err)
	}
	status, err = f.q.ReconciliationStatus(context.Background())
	if err != nil || status.ScanErrorCode != "" || status.State != "checked" {
		t.Fatal(status, err)
	}
	assertFile(t, f, "f-4", 1, false, number(1))
}
func TestIssuePersistenceFailureRemainsVisibleAndCannotStrandLaterRows(t *testing.T) {
	f := setup(t)
	f.file(t, "published", "file", 4, 4, true, nil)
	if err := f.q.CompleteTransfer("published"); err != nil {
		t.Fatal(err)
	}
	// Simulate a failed audit/status write without preventing reads or later resource work.
	if _, err := f.db.Exec(`CREATE TRIGGER reject_issue BEFORE INSERT ON file_reconciliation_issues BEGIN SELECT RAISE(ABORT,'write unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := Sweep(context.Background(), f.q, f.disk); !errors.Is(err, ErrStorageCheckFailed) {
		t.Fatal(err)
	}
	status, err := f.q.ReconciliationStatus(context.Background())
	if err != nil || status.State != "degraded" || status.ScanErrorCode != "scan_failed" || status.IssueCount != 0 {
		t.Fatal(status, err)
	}
	f.db.Close()
	f.db, err = database.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.q = database.NewQueries(f.db)
	status, err = f.q.ReconciliationStatus(context.Background())
	if err != nil || status.ScanErrorCode != "scan_failed" {
		t.Fatal("lost restart marker", status, err)
	}
	if _, err = f.db.Exec(`DROP TRIGGER reject_issue`); err != nil {
		t.Fatal(err)
	}
	if err = f.disk.Save("published/file", strings.NewReader("safe")); err != nil {
		t.Fatal(err)
	}
	if err = Sweep(context.Background(), f.q, f.disk); err != nil {
		t.Fatal(err)
	}
	status, err = f.q.ReconciliationStatus(context.Background())
	if err != nil || status.State != "checked" || status.ScanErrorCode != "" {
		t.Fatal(status, err)
	}
}

func TestPayloadCleanupIntentDoesNotHidePublishedDamage(t *testing.T) {
	f := setup(t)
	f.file(t, "published", "file", 4, 4, true, body("ab"))
	if err := f.q.CompleteTransfer("published"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO cleanup_tasks(kind,resource_id,mode,reason,pending_since) VALUES('transfer','published','payload','download_limit',0)`); err != nil {
		t.Fatal(err)
	}
	if err := check(t, f, f.disk, "file"); !errors.Is(err, ErrPayloadUnavailable) {
		t.Fatal("payload cleanup hid damaged file", err)
	}
	assertFile(t, f, "file", 4, true, number(2))
}
