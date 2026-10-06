package cleanup

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestCleanupSkipsBusyTransferAndRetries(t *testing.T) {
	dir := t.TempDir()
	db, err := openFixture(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
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
	if err := q.RequestResourceCleanup("transfer", "busy"); err != nil {
		t.Fatal(err)
	}
	worker.sweep()
	if _, err := q.GetTransfer("busy"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retry did not clean: %v", err)
	}
}

func TestSweepRetainsTrafficTotalsAndContinuesAfterRetentionFailure(t *testing.T) {
	dir := t.TempDir()
	db, err := openFixture(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	files, err := store.NewDiskStore(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := q.AddTraffic(now.AddDate(0, 0, -500), database.TrafficTotals{DownloadedBytes: 42}); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(q, files, time.Hour)
	worker.sweep()
	history, err := q.TrafficHistory(now)
	if err != nil || len(history.Days) != 0 || history.Lifetime.DownloadedBytes != 42 {
		t.Fatalf("retention lost lifetime totals or retained old detail: %+v / %v", history, err)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM traffic_days`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("sweep did not prune daily rows: %d / %v", rows, err)
	}
	if err := q.CreateTransfer("expired-during-retention-failure", now.Add(-time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE traffic_retention`); err != nil {
		t.Fatal(err)
	}
	worker.sweep()
	if _, err := q.GetTransfer("expired-during-retention-failure"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed retention blocked file cleanup: %v", err)
	}
}

func TestSecurityAuditRetentionFailureDoesNotBlockPayloadCleanup(t *testing.T) {
	dir := t.TempDir()
	db, err := openFixture(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	files, err := store.NewDiskStore(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.RecordSecurityEvent(database.SecurityEvent{Kind: "transfers.paused", Origin: "local", TargetType: "server", Outcome: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE security_events SET occurred_at='2000-01-01T00:00:00.000000000Z'`); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(q, files, time.Hour)
	worker.sweep()
	var retained int
	if err := db.QueryRow(`SELECT retained FROM security_audit_buckets WHERE bucket='administration'`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("audit retention was not swept: %d %v", retained, err)
	}
	if err := q.CreateTransfer("audit-cleanup", time.Now().Add(-time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE security_events`); err != nil {
		t.Fatal(err)
	}
	worker.sweep()
	if _, err := q.GetTransfer("audit-cleanup"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("audit failure prevented payload cleanup: %v", err)
	}
	if !q.SecurityAuditDegraded() {
		t.Fatal("audit cleanup failure not surfaced")
	}
}

func TestCanceledCleanupWorkerDoesNotPerformStartupWork(t *testing.T) {
	dir := t.TempDir()
	db, err := openFixture(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	files, err := store.NewDiskStore(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("expired", time.Now().Add(-time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := files.Save("expired/payload", strings.NewReader("preserve")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	NewWorker(q, files, time.Hour).Run(ctx)
	resource, err := q.GetTransfer("expired")
	if err != nil || resource.Status != "pending" {
		t.Fatal("canceled worker changed resource", resource, err)
	}
	if size, err := files.Size("expired/payload"); err != nil || size != 8 {
		t.Fatal("canceled worker removed payload", size, err)
	}
	status, err := q.ResourceCleanup("transfer", "expired")
	if err != nil || status.State != "none" {
		t.Fatal("canceled worker ran discovery", status, err)
	}
	if err := SweepPending(context.Background(), q, files); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetTransfer("expired"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("live explicit sweep did not remove resource", err)
	}
}
