package cleanup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestCleanupPartialDeletionRetainsReservationAndResumesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q := database.NewQueries(db)
	disk, err := store.NewDiskStore(filepath.Join(dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("large", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 140; i++ {
		id := fmt.Sprintf("f-%03d", i)
		if err := q.CreateFile(id, "large", 1); err != nil {
			t.Fatal(err)
		}
		if err := disk.Save("large/"+id, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.RevokeTransfer("large"); err != nil {
		t.Fatal(err)
	}
	if err := SweepPending(context.Background(), q, disk); err != nil {
		t.Fatal(err)
	}
	state, err := q.ResourceCleanup("transfer", "large")
	if err != nil || state.State != "pending" || state.AttemptCount != 1 {
		t.Fatal(state, err)
	}
	usage, err := q.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 140 {
		t.Fatal("partial cleanup refunded unverified bytes", usage, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q = database.NewQueries(db)
	for i := 0; i < 5; i++ {
		if err := q.RequestResourceCleanup("transfer", "large"); err != nil {
			t.Fatal(err)
		}
		if err := SweepPending(context.Background(), q, disk); err != nil {
			t.Fatal(err)
		}
		if _, err := q.GetTransfer("large"); errors.Is(err, sql.ErrNoRows) {
			break
		}
	}
	if _, err := q.GetTransfer("large"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restart did not finish queued deletion", err)
	}
	usage, err = q.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 0 {
		t.Fatal(usage, err)
	}
	overview, err := q.CleanupOverview()
	if err != nil || overview.PendingCount != 0 {
		t.Fatal(overview, err)
	}
}
func TestCleanupExhaustedPayloadWaitsForFinalReaderAndPreservesMetadata(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	disk, err := store.NewDiskStore(filepath.Join(dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("final-reader", time.Now().Add(time.Hour), 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "final-reader", 4); err != nil {
		t.Fatal(err)
	}
	if err := disk.Save("final-reader/file", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("file", 4, true); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("final-reader", []byte("manifest")); err != nil {
		t.Fatal(err)
	}
	if err := q.CompleteTransfer("final-reader"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE transfers SET download_count=1 WHERE id='final-reader';UPDATE files SET download_count=1 WHERE id='file'`); err != nil {
		t.Fatal(err)
	}
	unlock, err := store.AcquireTransfer(context.Background(), "final-reader")
	if err != nil {
		t.Fatal(err)
	}
	release, err := store.AcquireReader("final-reader")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	unlock()
	if err := SweepPending(context.Background(), q, disk); err != nil {
		t.Fatal(err)
	}
	state, err := q.ResourceCleanup("transfer", "final-reader")
	if err != nil || state.State != "busy" || state.FailureCode != "" {
		t.Fatal(state, err)
	}
	if size, err := disk.Size("final-reader/file"); err != nil || size != 4 {
		t.Fatal("final reader payload removed", size, err)
	}
	release()
	if err := q.RequestResourceCleanup("transfer", "final-reader"); err != nil {
		t.Fatal(err)
	}
	if err := SweepPending(context.Background(), q, disk); err != nil {
		t.Fatal(err)
	}
	if size, err := disk.Size("final-reader/file"); err != nil || size != 0 {
		t.Fatal(size, err)
	}
	transfer, err := q.GetTransfer("final-reader")
	if err != nil || transfer.Status != "complete" || transfer.DownloadCount != 1 {
		t.Fatal("acknowledgement metadata lost", transfer, err)
	}
	if _, err := q.GetManifest("final-reader"); err != nil {
		t.Fatal("manifest lost", err)
	}
	for i := 0; i < 2; i++ {
		if err := SweepPending(context.Background(), q, disk); err != nil {
			t.Fatal(err)
		}
	}
	overview, err := q.CleanupOverview()
	if err != nil || overview.PendingCount != 0 {
		t.Fatal("released payload repeatedly queued", overview, err)
	}
}
func TestCleanupMetadataDeletionFailureRetainsReservationAndRetries(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	disk, err := store.NewDiskStore(filepath.Join(dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("failed-db", time.Now().Add(-time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "failed-db", 4); err != nil {
		t.Fatal(err)
	}
	if err := disk.Save("failed-db/file", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_resource_delete BEFORE DELETE ON transfers BEGIN SELECT RAISE(ABORT,'private/error details'); END`); err != nil {
		t.Fatal(err)
	}
	if err := SweepPending(context.Background(), q, disk); err == nil {
		t.Fatal("metadata failure swallowed")
	}
	state, err := q.ResourceCleanup("transfer", "failed-db")
	if err != nil || state.FailureCode != "metadata_delete_failed" || state.State != "failed" {
		t.Fatal(state, err)
	}
	usage, err := q.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 4 {
		t.Fatal("failed DB completion refunded reservation", usage, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_resource_delete`); err != nil {
		t.Fatal(err)
	}
	if err := q.RequestResourceCleanup("transfer", "failed-db"); err != nil {
		t.Fatal(err)
	}
	if err := SweepPending(context.Background(), q, disk); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetTransfer("failed-db"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing payload did not safely retry", err)
	}
}

type uncertainDeletionStore struct{ *store.DiskStore }

func (s uncertainDeletionStore) DeleteAllBounded(ctx context.Context, id string, budget int) (bool, error) {
	done, err := s.DiskStore.DeleteAllBounded(ctx, id, budget)
	if err != nil || !done {
		return done, err
	}
	// Model unlink succeeding before a directory sync/close failure. Absence
	// alone does not authorize releasing the reservation on this attempt.
	return false, errors.New("private directory durability failure")
}

func TestCleanupUncertainDeletionRetainsMetadataAndReservation(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	disk, err := store.NewDiskStore(filepath.Join(dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("uncertain", time.Now().Add(-time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "uncertain", 4); err != nil {
		t.Fatal(err)
	}
	if err := disk.Save("uncertain/file", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if err := SweepPending(context.Background(), q, uncertainDeletionStore{disk}); err == nil {
		t.Fatal("uncertain deletion was acknowledged")
	}
	if info, err := disk.Inspect("uncertain/file"); err != nil || info.Exists {
		t.Fatal("test did not remove physical payload", info, err)
	}
	if _, err := q.GetFile("file"); err != nil {
		t.Fatal("uncertain deletion removed metadata", err)
	}
	state, err := q.ResourceCleanup("transfer", "uncertain")
	if err != nil || state.State != "failed" || state.FailureCode != "storage_delete_failed" {
		t.Fatal(state, err)
	}
	usage, err := q.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 4 {
		t.Fatal("uncertain deletion refunded reservation", usage, err)
	}
	if err := q.RequestResourceCleanup("transfer", "uncertain"); err != nil {
		t.Fatal(err)
	}
	if err := SweepPending(context.Background(), q, disk); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetTransfer("uncertain"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("successful durable retry did not release metadata", err)
	}
	usage, err = q.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 0 {
		t.Fatal(usage, err)
	}
}
