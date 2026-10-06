package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestAdminCleanupResultAfterConcurrentPayloadCompletion(t *testing.T) {
	db, err := openFixture(filepath.Join(t.TempDir(), "cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	s := &Server{queries: q}
	id := "finished-payload"
	if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", id, 1); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("file", 1, true); err != nil {
		t.Fatal(err)
	}
	if err := q.CompleteTransfer(id); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.ReserveFileDownload(id, "file"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := q.RequestResourceCleanup("transfer", id); err != nil {
		t.Fatal(err)
	}
	// Simulate the worker finishing after the action queued work and before
	// its response reads the durable result. No physical payload exists here.
	if err := q.ReleaseCleanedPayloads(id); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	s.writeAdminCleanupResult(response, "transfer", id)
	var result struct {
		State   string
		Cleanup database.ResourceCleanupStatus
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || result.State != "complete" || result.Cleanup.State != "none" {
		t.Fatalf("finished payload cleanup was misreported: %d %s", response.Code, response.Body.String())
	}
	transfer, err := q.GetTransfer(id)
	if err != nil || transfer.Status != "complete" {
		t.Fatal("metadata was not retained", transfer, err)
	}
}
