package database

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestOrphanLocalFailureCannotBeClearedByOlderPass(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	now := time.Now()
	d, err := q.NextOrphanDirectory(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = q.MarkOrphanFailure(canceled); err == nil {
		t.Fatal("expected unpersisted failure")
	}
	if err = q.CommitOrphanPage(ctx, d, store.InventoryPage{Done: true}, now); err != nil {
		t.Fatal(err)
	}
	if err = q.FinishOrphanPass(ctx, now); err != nil {
		t.Fatal(err)
	}
	s, err := q.OrphanScanStatus(ctx)
	if err != nil || s.ScanErrorCode != "scan_failed" || !s.ScanPending {
		t.Fatal(s, err)
	}
	d, err = q.NextOrphanDirectory(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.CommitOrphanPage(ctx, d, store.InventoryPage{Done: true}, now); err != nil {
		t.Fatal(err)
	}
	if err = q.FinishOrphanPass(ctx, now); err != nil {
		t.Fatal(err)
	}
	s, err = q.OrphanScanStatus(ctx)
	if err != nil || s.ScanErrorCode != "" || s.ScanPending {
		t.Fatal(s, err)
	}
}

func TestOrphanDeleteCallbackHoldsWriterLockAcrossConnections(t *testing.T) {
	q, path := resourceFixture(t)
	ctx := context.Background()
	now := time.Now()
	if err := q.CreateTransfer("live", now.Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		t.Fatal(err)
	}
	e := store.InventoryEntry{Directory: "live", Name: "new"}
	raw, _ := json.Marshal(e)
	if _, err = q.db.Exec(`INSERT INTO orphan_candidates(directory,name,entry,fingerprint,first_seen,observed_at,seen_scan,parent_generation,touched,category) VALUES('live','new',?,'fingerprint',?,?,1,1,1,'pending')`, string(raw), now.Add(-2*time.Hour).Unix(), now.Add(-2*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	candidates, err := q.NextOrphanCandidates(ctx)
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	called := false
	removed, err := q.RemoveOrphanCandidate(ctx, candidates[0], now, func() (bool, error) {
		called = true
		// A second SQLite connection cannot allocate while physical deletion and its
		// last canonical membership check are in progress.
		if _, writeErr := conn.ExecContext(ctx, `INSERT INTO files(id,transfer_id,size) VALUES('new','live',1)`); writeErr == nil {
			t.Fatal("allocation bypassed orphan writer lock")
		}
		return true, nil
	})
	if err != nil || !removed || !called {
		t.Fatal(removed, called, err)
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO files(id,transfer_id,size) VALUES('new','live',1)`); err != nil {
		t.Fatal("allocation did not resume after delete transaction", err)
	}
}

func TestOrphanObservationAndCursorAreAtomicAndGuarded(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	now := time.Now()
	d, err := q.NextOrphanDirectory(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	p := store.InventoryPage{Entries: []store.InventoryEntry{{Name: "unexpected", Unsupported: true}}, Next: store.InventoryCursor{Begun: true, Cookie: 7}}
	if _, err = q.db.Exec(`CREATE TRIGGER reject_orphan BEFORE INSERT ON orphan_candidates BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err = q.CommitOrphanPage(ctx, d, p, now); err == nil {
		t.Fatal("expected observation failure")
	}
	again, err := q.NextOrphanDirectory(ctx, true)
	if err != nil || again.Cursor.Begun {
		t.Fatal("cursor advanced without observation", again, err)
	}
	if _, err = q.db.Exec(`DROP TRIGGER reject_orphan`); err != nil {
		t.Fatal(err)
	}
	if err = q.CommitOrphanPage(ctx, d, p, now); err != nil {
		t.Fatal(err)
	}
	// A stale page cannot inject a new observation or rewind the cursor.
	p.Entries[0].Name = "stale"
	p.Next.Cookie = 2
	if err = q.CommitOrphanPage(ctx, d, p, now); err != nil {
		t.Fatal(err)
	}
	again, err = q.NextOrphanDirectory(ctx, true)
	if err != nil || again.Cursor.Cookie != 7 {
		t.Fatal(again, err)
	}
	candidates, err := q.NextOrphanCandidates(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].Entry.Name != "unexpected" {
		t.Fatal(candidates, err)
	}
}

func TestOrphanStaleFinishCannotPublishOrResetNewGeneration(t *testing.T) {
	q, _ := resourceFixture(t)
	ctx := context.Background()
	now := time.Now()
	old, err := q.NextOrphanDirectory(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	oldEpoch := old.failureEpoch
	if err = q.ResetOrphanScan(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := q.NextOrphanDirectory(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.CommitOrphanPage(ctx, current, store.InventoryPage{Done: true}, now); err != nil {
		t.Fatal(err)
	}
	// Model a finisher that captured the old generation, paused, and resumes
	// after a newer generation has completed enumeration.
	if err = q.finishOrphanPass(ctx, now, old.Generation, oldEpoch); err != nil {
		t.Fatal(err)
	}
	var generation int64
	var done bool
	var completed any
	if err = q.db.QueryRow(`SELECT generation,root_done,completed_at FROM orphan_progress WHERE id=1`).Scan(&generation, &done, &completed); err != nil {
		t.Fatal(err)
	}
	if generation != current.Generation || !done || completed != nil {
		t.Fatalf("stale finish changed newer pass: generation=%d done=%v completed=%v", generation, done, completed)
	}
	q.orphan.Lock()
	active := q.orphan.passActive
	tracked := q.orphan.passGeneration
	q.orphan.Unlock()
	if !active || tracked != current.Generation {
		t.Fatal("stale finish reset newer local tracker", active, tracked)
	}
	if err = q.FinishOrphanPass(ctx, now); err != nil {
		t.Fatal(err)
	}
	s, err := q.OrphanScanStatus(ctx)
	if err != nil || s.ScanPending || s.LastScanCompletedAt == nil {
		t.Fatal(s, err)
	}
}
