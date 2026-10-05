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
	sqlite3 "modernc.org/sqlite/lib"
)

func requireSQLiteFull(t *testing.T, err error) {
	t.Helper()
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code()&0xff != sqlite3.SQLITE_FULL {
		t.Fatalf("expected SQLite FULL, received %v", err)
	}
}

func TestCleanupSQLiteFullPreservesReservationsAndRecovers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func(opened *sql.DB) { _ = opened.Close() }(db)
	// max_page_count is connection-scoped; keep all statements on one connection.
	db.SetMaxOpenConns(1)
	q := database.NewQueries(db)
	files, err := store.NewDiskStore(filepath.Join(root, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("full-db", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "full-db", 4); err != nil {
		t.Fatal(err)
	}
	if err := files.Save("full-db/file", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if err := q.RevokeTransfer("full-db"); err != nil {
		t.Fatal(err)
	}
	// Bound a disposable database, never its host filesystem. The trigger makes
	// cleanup's metadata transaction request a page even when its normal DELETE
	// could reuse existing space. SQLite itself produces FULL and rolls it back.
	if _, err := db.Exec(`CREATE TABLE pressure(data BLOB NOT NULL);
CREATE TRIGGER cleanup_pressure BEFORE DELETE ON transfers BEGIN
INSERT INTO pressure(data) VALUES(zeroblob(65536)); END`); err != nil {
		t.Fatal(err)
	}
	var pages, limit int
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(fmt.Sprintf("PRAGMA max_page_count=%d", pages+16)).Scan(&limit); err != nil || limit != pages+16 {
		t.Fatal("could not bound disposable database", limit, err)
	}
	filled := false
	for i := 0; i < 1024; i++ {
		_, err = db.Exec(`INSERT INTO pressure(data) VALUES(zeroblob(4096))`)
		if err != nil {
			requireSQLiteFull(t, err)
			filled = true
			break
		}
	}
	if !filled {
		t.Fatal("bounded pressure fixture did not reach SQLite FULL")
	}
	requireSQLiteFull(t, SweepPending(context.Background(), q, files))
	if info, err := files.Inspect("full-db/file"); err != nil || info.Exists {
		t.Fatal("fixture did not exercise metadata failure after verified payload removal", info, err)
	}
	if _, err := q.GetTransfer("full-db"); err != nil {
		t.Fatal("failed metadata transaction removed the transfer", err)
	}
	usage, err := q.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 4 || usage.Files != 1 {
		t.Fatal("SQLite FULL refunded uncommitted deletion", usage, err)
	}
	state, err := q.ResourceCleanup("transfer", "full-db")
	if err != nil || state.State != "failed" || state.FailureCode != "metadata_delete_failed" {
		t.Fatal("cleanup lost its durable failure/retry state", state, err)
	}
	// Reopening restores page headroom (analogous to operator capacity recovery),
	// but must not lose the queue, failure or resource reservations.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func(opened *sql.DB) { _ = opened.Close() }(db)
	q = database.NewQueries(db)
	restored, err := q.ResourceCleanup("transfer", "full-db")
	if err != nil || restored.State != "failed" || restored.AttemptCount != state.AttemptCount {
		t.Fatal("restart lost queued cleanup", restored, err)
	}
	usage, err = q.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 4 {
		t.Fatal("restart lost conservative reservations", usage, err)
	}
	if _, err := db.Exec(`DROP TRIGGER cleanup_pressure; DROP TABLE pressure`); err != nil {
		t.Fatal(err)
	}
	if err := q.RequestResourceCleanup("transfer", "full-db"); err != nil {
		t.Fatal(err)
	}
	if err := SweepPending(context.Background(), q, files); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetTransfer("full-db"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("recovery did not converge", err)
	}
	usage, err = q.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 0 || usage.Files != 0 {
		t.Fatal("verified retry did not release capacity", usage, err)
	}
	status, err := q.CleanupOverview()
	if err != nil || status.PendingCount != 0 || status.FailedCount != 0 {
		t.Fatal("completed retry retained failure/queue counters", status, err)
	}
}
