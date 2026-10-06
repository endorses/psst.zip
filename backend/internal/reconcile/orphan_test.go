package reconcile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func orphanSweeps(t *testing.T, f *fixture, fs store.FileStore, now time.Time, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := SweepOrphansAt(context.Background(), f.q, fs, now.Add(time.Duration(i)*time.Second)); err != nil && !errors.Is(err, store.ErrInventoryChanged) {
			t.Fatal(err)
		}
	}
}
func TestOrphanGraceCanonicalAndSiblingPreservation(t *testing.T) {
	f := setup(t)
	now := time.Now().Truncate(time.Second)
	f.file(t, "live", "kept", 4, 4, true, body("kept"))
	if err := f.disk.Save("live/orphan", strings.NewReader("orphan")); err != nil {
		t.Fatal(err)
	}
	if err := f.disk.Save("gone/file", strings.NewReader("orphan")); err != nil {
		t.Fatal(err)
	}
	// Even an expired/revoked or payload-deleted row remains authoritative here.
	if _, err := f.db.Exec(`UPDATE files SET payload_deleted=1 WHERE id='kept'`); err != nil {
		t.Fatal(err)
	}
	orphanSweeps(t, f, f.disk, now, 10)
	if info, _ := f.disk.Inspect("live/orphan"); !info.Exists {
		t.Fatal("deleted before grace")
	}
	orphanSweeps(t, f, f.disk, now.Add(2*time.Hour), 20)
	if info, _ := f.disk.Inspect("live/orphan"); info.Exists {
		t.Fatal("orphan retained")
	}
	if info, _ := f.disk.Inspect("live/kept"); !info.Exists {
		t.Fatal("canonical sibling deleted")
	}
	if info, _ := f.disk.Inspect("gone/file"); info.Exists {
		t.Fatal("unreferenced file retained")
	}
	// Directory identity changes when its last file is unlinked, starting a new grace.
	orphanSweeps(t, f, f.disk, now.Add(4*time.Hour), 20)
	status, err := f.q.OrphanScanStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingCandidates != 0 || status.ScanPending {
		t.Fatalf("did not settle: %+v", status)
	}
}

func TestOrphanAllocationWriterRecheck(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	if err := f.q.CreateTransfer("live", now.Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.disk.Save("live/new", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	orphanSweeps(t, f, f.disk, now, 8)
	candidates, err := f.q.NextOrphanCandidates(ctx)
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	if err = f.q.CreateFile("new", "live", 4); err != nil {
		t.Fatal(err)
	}
	called := false
	removed, err := f.q.RemoveOrphanCandidate(ctx, candidates[0], now.Add(2*time.Hour), func() (bool, error) { called = true; return true, nil })
	if err != nil || !removed || called {
		t.Fatalf("writer recheck removed=%v callback=%v err=%v", removed, called, err)
	}
	if info, _ := f.disk.Inspect("live/new"); !info.Exists {
		t.Fatal("newly allocated file removed")
	}
}

func TestOrphanBusyReadersAndRestart(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	if err := f.q.CreateTransfer("live", now.Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.disk.Save("live/orphan", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	orphanSweeps(t, f, f.disk, now, 8)
	unlock, err := store.AcquireTransfer(ctx, "live")
	if err != nil {
		t.Fatal(err)
	}
	orphanSweeps(t, f, f.disk, now.Add(2*time.Hour), 4)
	unlock()
	reader, err := store.AcquireReader("live")
	if err != nil {
		t.Fatal(err)
	}
	orphanSweeps(t, f, f.disk, now.Add(2*time.Hour), 4)
	status, err := f.q.OrphanScanStatus(ctx)
	if err != nil || status.BusyCount != 1 {
		t.Fatal(status, err)
	}
	reader()
	if err = f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db, err = database.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.q = database.NewQueries(f.db)
	if err = f.q.ResetOrphanScan(ctx); err != nil {
		t.Fatal(err)
	}
	status, err = f.q.OrphanScanStatus(ctx)
	if err != nil || !status.ScanPending || status.PendingCandidates != 1 || status.LastScanCompletedAt != nil {
		t.Fatal(status, err)
	}
	orphanSweeps(t, f, f.disk, now.Add(2*time.Hour), 8)
	if info, _ := f.disk.Inspect("live/orphan"); info.Exists {
		t.Fatal("restart lost candidate progress")
	}
}

type failingOrphanStore struct {
	store.FileStore
	inventory  orphanStore
	directory  string
	failDelete bool
}

func (f failingOrphanStore) InventoryDirectory(ctx context.Context, d string, c store.InventoryCursor, n int) (store.InventoryPage, error) {
	if d == f.directory {
		return store.InventoryPage{}, errors.New("injected inventory failure")
	}
	return f.inventory.InventoryDirectory(ctx, d, c, n)
}
func (f failingOrphanStore) RemoveOrphan(ctx context.Context, e store.InventoryEntry) (bool, error) {
	if f.failDelete {
		return false, errors.New("injected durable delete failure")
	}
	return f.inventory.RemoveOrphan(ctx, e)
}
func TestOrphanDirectoryFailureDoesNotStarveTail(t *testing.T) {
	f := setup(t)
	now := time.Now().Truncate(time.Second)
	for i := 0; i < 90; i++ {
		if err := f.disk.Save(fmt.Sprintf("dir%03d/file", i), strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	broken := failingOrphanStore{FileStore: f.disk, inventory: f.disk, directory: "dir000"}
	for i := 0; i < 450; i++ {
		_ = SweepOrphansAt(context.Background(), f.q, broken, now.Add(time.Duration(i)*time.Minute))
	}
	if info, _ := f.disk.Inspect("dir089/file"); info.Exists {
		t.Fatal("healthy tail starved")
	}
	if info, _ := f.disk.Inspect("dir000/file"); !info.Exists {
		t.Fatal("failed directory removed")
	}
	s, err := f.q.OrphanScanStatus(context.Background())
	if err != nil || !s.ScanPending || s.ScanErrorCode != "scan_failed" || s.PendingDirectories > 64 || s.PendingCandidates > 256 {
		t.Fatal(s, err)
	}
}
func TestOrphanSaturatedUnsupportedCandidatesReachHealthyTail(t *testing.T) {
	f := setup(t)
	now := time.Now().Truncate(time.Second)
	if err := f.q.CreateTransfer("live", now.Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(filepath.Dir(f.path), "payloads", "live")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 280; i++ {
		if err := os.Symlink("/unfollowed", filepath.Join(root, fmt.Sprintf("bad%03d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.disk.Save("live/healthy", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		_ = SweepOrphansAt(context.Background(), f.q, f.disk, now.Add(time.Duration(i)*time.Minute))
	}
	if info, _ := f.disk.Inspect("live/healthy"); info.Exists {
		t.Fatal("healthy candidate starved by unsupported entries")
	}
	s, err := f.q.OrphanScanStatus(context.Background())
	if err != nil || s.PendingCandidates > 256 || !s.Saturated || !s.ScanPending || s.UnsupportedCount == 0 || s.LastScanCompletedAt != nil {
		t.Fatal(s, err)
	}
	if _, err = os.Lstat(filepath.Join(root, "bad000")); err != nil {
		t.Fatal("unsupported entry removed", err)
	}
}
func TestOrphanFailureAndStalePageCannotComplete(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now()
	d, err := f.q.NextOrphanDirectory(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.disk.InventoryDirectory(ctx, "", d.Cursor, d.Budget)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.q.ResetOrphanScan(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.q.CommitOrphanPage(ctx, d, page, now); err != nil {
		t.Fatal(err)
	}
	if err = f.q.FinishOrphanPass(ctx, now); err != nil {
		t.Fatal(err)
	}
	s, err := f.q.OrphanScanStatus(ctx)
	if err != nil || !s.ScanPending || s.LastScanCompletedAt != nil {
		t.Fatal(s, err)
	}
	broken := failingOrphanStore{FileStore: f.disk, inventory: f.disk, directory: ""}
	if err = SweepOrphansAt(ctx, f.q, broken, now); err == nil {
		t.Fatal("expected failure")
	}
	s, err = f.q.OrphanScanStatus(ctx)
	if err != nil || !s.ScanPending || s.ScanErrorCode != "scan_failed" {
		t.Fatal(s, err)
	}
	orphanSweeps(t, f, f.disk, now, 4)
	s, err = f.q.OrphanScanStatus(ctx)
	if err != nil || s.ScanPending || s.ScanErrorCode != "" {
		t.Fatal(s, err)
	}
}
func TestOrphanWorkerCanceled(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { RunOrphans(ctx, f.q, f.disk, time.Hour); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored cancellation")
	}
}

func TestOrphanMissingAfterFirstObservationAndDeleteFailure(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	if err := f.q.CreateTransfer("live", now.Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.disk.Save("live/missing", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	// Exactly one candidate observation: a second inventory observation cannot
	// happen once this path is absent.
	for i := 0; i < 4; i++ {
		orphanSweeps(t, f, f.disk, now, 1)
		s, _ := f.q.OrphanScanStatus(ctx)
		if s.PendingCandidates == 1 {
			break
		}
	}
	candidates, err := f.q.NextOrphanCandidates(ctx)
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	if !candidates[0].ObservedAt.Equal(candidates[0].FirstSeenAt) {
		t.Fatal("test needs one observation")
	}
	if err = os.Remove(filepath.Join(filepath.Dir(f.path), "payloads", "live", "missing")); err != nil {
		t.Fatal(err)
	}
	broken := failingOrphanStore{FileStore: f.disk, inventory: f.disk, directory: "never", failDelete: true}
	if err = SweepOrphansAt(ctx, f.q, broken, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expected durability failure")
	}
	s, err := f.q.OrphanScanStatus(ctx)
	if err != nil || s.PendingCandidates != 1 || s.FailedCount != 1 || s.LastScanCompletedAt != nil {
		t.Fatal(s, err)
	}
	orphanSweeps(t, f, f.disk, now.Add(2*time.Hour), 8)
	s, err = f.q.OrphanScanStatus(ctx)
	if err != nil || s.PendingCandidates != 0 || s.ScanPending {
		t.Fatal(s, err)
	}
}
func TestOrphanUnsupportedManualRemovalClearsOnlyAfterStableInventory(t *testing.T) {
	for _, removeParent := range []bool{false, true} {
		t.Run(fmt.Sprint(removeParent), func(t *testing.T) {
			f := setup(t)
			now := time.Now().Truncate(time.Second)
			root := filepath.Join(filepath.Dir(f.path), "payloads", "unknown")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/unfollowed", filepath.Join(root, "unexpected")); err != nil {
				t.Fatal(err)
			}
			orphanSweeps(t, f, f.disk, now, 8)
			s, err := f.q.OrphanScanStatus(context.Background())
			if err != nil || s.UnsupportedCount != 1 {
				t.Fatal(s, err)
			}
			if err = os.Remove(filepath.Join(root, "unexpected")); err != nil {
				t.Fatal(err)
			}
			if removeParent {
				if err = os.Remove(root); err != nil {
					t.Fatal(err)
				}
			}
			orphanSweeps(t, f, f.disk, now.Add(2*time.Hour), 8)
			s, err = f.q.OrphanScanStatus(context.Background())
			if err != nil || s.UnsupportedCount != 0 {
				t.Fatal(s, err)
			}
		})
	}
}

func TestOrphanReplacedParentRetiresStaleObservation(t *testing.T) {
	f := setup(t)
	now := time.Now().Truncate(time.Second)
	if err := f.q.CreateTransfer("live", now.Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.disk.Save("live/old", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	orphanSweeps(t, f, f.disk, now, 8)
	root := filepath.Join(filepath.Dir(f.path), "payloads", "live")
	// Keep the old inode alive outside the scanned root so inode reuse cannot
	// disguise the replacement in this regression.
	if err := os.Rename(root, filepath.Join(filepath.Dir(f.path), "old-parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	orphanSweeps(t, f, f.disk, now.Add(2*time.Hour), 10)
	s, err := f.q.OrphanScanStatus(context.Background())
	if err != nil || s.ScanPending || s.PendingCandidates != 0 {
		t.Fatal(s, err)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(f.path), "old-parent", "old")); err != nil {
		t.Fatal("retired observation mutated replaced parent", err)
	}
}

type hotOrphanStore struct{ *store.DiskStore }

func (f hotOrphanStore) InventoryDirectory(ctx context.Context, d string, c store.InventoryCursor, n int) (store.InventoryPage, error) {
	p, err := f.DiskStore.InventoryDirectory(ctx, d, c, n)
	if err == nil && strings.HasPrefix(d, "hot") {
		p.Done = false
		p.Unstable = true
		p.Next.Unstable = true
	}
	return p, err
}
func TestOrphanHotDirectoryQueueDoesNotStarveTail(t *testing.T) {
	f := setup(t)
	now := time.Now().Truncate(time.Second)
	for i := 0; i < 70; i++ {
		if err := f.disk.Save(fmt.Sprintf("hot%03d/file", i), strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.disk.Save("tail/file", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	hot := hotOrphanStore{f.disk}
	for i := 0; i < 430; i++ {
		_ = SweepOrphansAt(context.Background(), f.q, hot, now.Add(time.Duration(i)*time.Minute))
	}
	if info, _ := f.disk.Inspect("tail/file"); info.Exists {
		t.Fatal("hot prefixes starved healthy tail")
	}
	s, err := f.q.OrphanScanStatus(context.Background())
	if err != nil || !s.Unstable || !s.ScanPending || s.LastScanCompletedAt != nil {
		t.Fatal(s, err)
	}
}

type canceledOrphanStore struct {
	*store.DiskStore
	started chan struct{}
}

func (f canceledOrphanStore) InventoryDirectory(ctx context.Context, _ string, _ store.InventoryCursor, _ int) (store.InventoryPage, error) {
	close(f.started)
	<-ctx.Done()
	return store.InventoryPage{}, ctx.Err()
}
func TestOrphanWorkerInFlightCancellation(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs := canceledOrphanStore{f.disk, make(chan struct{})}
	done := make(chan struct{})
	go func() { RunOrphans(ctx, f.q, fs, time.Hour); close(done) }()
	select {
	case <-fs.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("in-flight cancellation ignored")
	}
}

func TestOrphanWorkerObservesDiskThenStopsBeforeGrace(t *testing.T) {
	f := setup(t)
	if err := f.disk.Save("new/file", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { RunOrphans(ctx, f.q, f.disk, 50*time.Millisecond); close(done) }()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
	observed := false
	for !observed {
		select {
		case <-deadline.C:
			t.Fatal("worker did not discover real disk orphan")
		case <-poll.C:
			s, err := f.q.OrphanScanStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			observed = s.PendingCandidates > 0
		}
	}
	cancel()
	select {
	case <-done:
	// A canceled sweep can finish durable progress using separate 500ms
	// contexts. Allow that bounded cleanup plus race-instrumented scheduling.
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	if info, err := f.disk.Inspect("new/file"); err != nil || !info.Exists {
		t.Fatal("worker removed payload before grace", info, err)
	}
}
