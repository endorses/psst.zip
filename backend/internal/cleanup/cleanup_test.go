package cleanup

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestCleanupSkipsBusyTransferAndRetries(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := database.NewQueries(db)
	files, err := store.NewDiskStore(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"busy", "other"} {
		if err := q.CreateTransfer(id, time.Now().Add(-time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	unlock, err := store.AcquireTransfer(context.Background(), "busy")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	worker := NewWorker(q, files, time.Hour)
	done := make(chan struct{})
	go func() { worker.sweep(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("busy transfer stalled cleanup")
	}
	if _, err := q.GetTransfer("other"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other not removed: %v", err)
	}
	if transfer, err := q.GetTransfer("busy"); err != nil || transfer.Status != "revoked" {
		t.Fatalf("busy not retained revoked: %v %v", transfer, err)
	}
	unlock()
	worker.sweep()
	if _, err := q.GetTransfer("busy"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retry did not clean: %v", err)
	}
}
