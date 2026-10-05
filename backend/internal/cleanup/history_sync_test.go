package cleanup

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestHistorySyncExpiryCleanupPublishesInactiveAndRemoval(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	q := database.NewQueries(db)
	if err = q.CreateUser(database.User{ID: "alice", Username: "alice", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	files, err := store.NewDiskStore(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Hour)
	if err = q.CreateTransfer("busy", expired, 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = q.CreateTransfer("child", expired, 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = q.CreateSlot("slot", expired, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = q.LinkSlotTransfer("slot", "child"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := q.AccountHistoryPage(context.Background(), "alice", false, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := store.AcquireTransfer(context.Background(), "busy")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err = SweepPending(context.Background(), q, files); err != nil {
		t.Fatal(err)
	}
	page, err := q.AccountHistoryChanges(context.Background(), "alice", 100, snapshot.SyncCursor)
	if err != nil {
		t.Fatal(err)
	}
	facts := map[string]database.HistoryChange{}
	for _, change := range page.Changes {
		facts[change.Kind+":"+change.ID] = change
	}
	busy := facts["transfer:busy"]
	if busy.Resource == nil || busy.Resource.Transfer.Status != "revoked" {
		t.Fatal("busy expired row not published inactive", busy)
	}
	slot := facts["slot:slot"]
	if slot.Action != "remove" || slot.Resource != nil {
		t.Fatal("expired parent cleanup did not emit retained removal", slot)
	}
	if child := facts["transfer:child"]; child.Resource != nil {
		t.Fatal("private child became visible during cleanup", child)
	}
	unlock()
	if err = q.RequestResourceCleanup("transfer", "busy"); err != nil {
		t.Fatal(err)
	}
	if err = SweepPending(context.Background(), q, files); err != nil {
		t.Fatal(err)
	}
	page, err = q.AccountHistoryChanges(context.Background(), "alice", 100, page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Changes) != 1 || page.Changes[0].ID != "busy" || page.Changes[0].Action != "remove" {
		t.Fatal("cleanup removal missing", page)
	}
}
