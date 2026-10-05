package cleanup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

type incidentFailureStore struct {
	store.FileStore
	fail bool
}

func (s *incidentFailureStore) DeleteAll(id string) error {
	if s.fail && id == "00" {
		return errors.New("injected deletion failure")
	}
	return s.FileStore.DeleteAll(id)
}
func TestIncidentCleanupBatchesAdvancePastFailuresAndRetainReservations(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	disk, err := store.NewDiskStore(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	fs := &incidentFailureStore{FileStore: disk, fail: true}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("%02d", i)
		if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
		if err := q.CreateFile("f"+id, id, 1); err != nil {
			t.Fatal(err)
		}
		if err := disk.Save(id+"/f"+id, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
		if err := q.RevokeTransfer(id); err != nil {
			t.Fatal(err)
		}
	}
	_ = SweepPending(context.Background(), q, fs)
	usage, err := q.ResourceUsage("")
	if err != nil || usage.Transfers != 5 || usage.ReservedBytes != 5 {
		t.Fatalf("failed unlink refunded or batch unbounded %+v %v", usage, err)
	}
	_ = SweepPending(context.Background(), q, fs)
	usage, err = q.ResourceUsage("")
	if err != nil || usage.Transfers != 1 || usage.ReservedBytes != 1 {
		t.Fatalf("failed first item blocked other cleanup %+v %v", usage, err)
	}
	state, err := q.ResourceCleanup("transfer", "00")
	if err != nil || state.State != "failed" || state.FailureCode != "storage_delete_failed" || state.AttemptCount != 1 || state.NextRetryAt == nil {
		t.Fatalf("missing durable failure: %+v / %v", state, err)
	}
	_ = SweepPending(context.Background(), q, fs)
	unchanged, _ := q.ResourceCleanup("transfer", "00")
	if unchanged.AttemptCount != state.AttemptCount {
		t.Fatal("failed resource retried before backoff")
	}
	fs.fail = false
	if err := q.RequestResourceCleanup("transfer", "00"); err != nil {
		t.Fatal(err)
	}
	_ = SweepPending(context.Background(), q, fs)
	usage, err = q.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 0 {
		t.Fatalf("retry did not release verified deletion %+v %v", usage, err)
	}
}
