package database

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCleanupDiscoveryBoundedDurableAndOffsetAware(t *testing.T) {
	q, path := resourceFixture(t)
	for i := 0; i < 70; i++ {
		if err := q.CreateTransfer(fmt.Sprintf("old-%03d", i), time.Now().Add(-time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	// This historical local timestamp is in the future despite its wall clock.
	if err := q.CreateTransfer("z-offset", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour).In(time.FixedZone("OLD", -12*3600))
	if _, err := q.db.Exec(`UPDATE transfers SET expires_at=? WHERE id='z-offset'`, future); err != nil {
		t.Fatal(err)
	}
	if err := q.DiscoverCleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	overview, err := q.CleanupOverview()
	if err != nil || overview.PendingCount != 64 || !overview.DiscoveryPending || overview.OldestPendingAt == nil {
		t.Fatal(overview, err)
	}
	if err := q.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	q = NewQueries(db)
	if err := q.DiscoverCleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	overview, err = q.CleanupOverview()
	if err != nil || overview.PendingCount != 70 || overview.DiscoveryPending || overview.LastDiscoveryAt == nil {
		t.Fatal(overview, err)
	}
	state, err := q.ResourceCleanup("transfer", "z-offset")
	if err != nil || state.State != "none" {
		t.Fatal("timezone misclassified", state, err)
	}
	tasks, err := q.DueCleanup("transfer", time.Now())
	if err != nil || len(tasks) != CleanupWorkBatch {
		t.Fatal(len(tasks), err)
	}
}
func TestCleanupRetryProofAndDurableFailureMetadata(t *testing.T) {
	q, path := resourceFixture(t)
	if err := q.CreateTransfer("live", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.RequestResourceCleanup("transfer", "live"); !errors.Is(err, ErrCleanupNotEligible) {
		t.Fatal("active resource eligible", err)
	}
	if err := q.RevokeTransfer("live"); err != nil {
		t.Fatal(err)
	}
	state, err := q.ResourceCleanup("transfer", "live")
	if err != nil || state.State != "pending" || state.Reason != "revoked" {
		t.Fatal(state, err)
	}
	now := time.Now()
	if err := q.StartCleanupAttempt("transfer", "live", now); err != nil {
		t.Fatal(err)
	}
	if err := q.RecordCleanupAttempt("transfer", "live", "failed", "storage_delete_failed", now); err != nil {
		t.Fatal(err)
	}
	if err := q.RecordCleanupAttempt("transfer", "live", "failed", "/private/path: raw error", now); !errors.Is(err, ErrCleanupNotEligible) {
		t.Fatal("freeform failure accepted", err)
	}
	if err := q.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	q = NewQueries(db)
	state, err = q.ResourceCleanup("transfer", "live")
	if err != nil || state.AttemptCount != 1 || state.LastFailureAt == nil || state.NextRetryAt == nil || !state.NextRetryAt.After(now) {
		t.Fatal(state, err)
	}
	overview, err := q.CleanupOverview()
	if err != nil || overview.PendingCount != 1 || overview.FailedCount != 1 {
		t.Fatal(overview, err)
	}
	tasks, err := q.DueCleanup("transfer", now)
	if err != nil || len(tasks) != 0 {
		t.Fatal("backoff lost on restart", tasks, err)
	}
	if err := q.RequestResourceCleanup("transfer", "live"); err != nil {
		t.Fatal(err)
	}
	tasks, err = q.DueCleanup("transfer", now)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	if err := q.DeleteTransfer("live"); err != nil {
		t.Fatal(err)
	}
	overview, err = q.CleanupOverview()
	if err != nil || overview.PendingCount != 0 || overview.FailedCount != 0 || overview.OldestPendingAt != nil {
		t.Fatal("deleted queue/counts retained", overview, err)
	}
}

func TestCleanupRevokedInboxRetryDoesNotTouchAllChildren(t *testing.T) {
	q, _ := resourceFixture(t)
	if err := q.CreateSlot("inbox", time.Now().Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 80; i++ {
		id := fmt.Sprintf("child-%03d", i)
		if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := q.db.Exec(`INSERT INTO slot_transfers(slot_id,transfer_id) VALUES('inbox',?)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.RevokeSlotQueued("inbox", SecurityEvent{}); err != nil {
		t.Fatal(err)
	}
	overview, err := q.CleanupOverview()
	if err != nil || overview.PendingCount != 81 {
		t.Fatal(overview, err)
	}
	// An insert trigger fires even for ON CONFLICT DO NOTHING. It detects a
	// repeated INSERT SELECT over all children, not just actual duplicate rows.
	if _, err := q.db.Exec(`CREATE TRIGGER no_child_requeue BEFORE INSERT ON cleanup_tasks WHEN NEW.kind='transfer' BEGIN SELECT RAISE(ABORT,'child requeue'); END; CREATE TRIGGER no_child_revoke BEFORE UPDATE ON transfers BEGIN SELECT RAISE(ABORT,'child rewrite'); END`); err != nil {
		t.Fatal(err)
	}
	if err := q.RevokeSlotQueued("inbox", SecurityEvent{}); err != nil {
		t.Fatal("waiting parent repeated bulk child work", err)
	}
	children, err := q.SlotCleanupChildren("inbox")
	if err != nil || len(children) != CleanupWorkBatch {
		t.Fatal(len(children), err)
	}
}

func TestCleanupDiscoveryRepairsLegacyRevokedParentInBoundedPages(t *testing.T) {
	q, _ := resourceFixture(t)
	if err := q.CreateSlot("inbox", time.Now().Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 70; i++ {
		id := fmt.Sprintf("child-%03d", i)
		if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := q.db.Exec(`INSERT INTO slot_transfers(slot_id,transfer_id) VALUES('inbox',?)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.db.Exec(`UPDATE slots SET status='revoked' WHERE id='inbox'`); err != nil {
		t.Fatal(err)
	}
	if err := q.DiscoverCleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM transfers WHERE status='revoked'`).Scan(&count); err != nil || count != 64 {
		t.Fatal("legacy repair not bounded", count, err)
	}
	if err := q.DiscoverCleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM transfers WHERE status='revoked'`).Scan(&count); err != nil || count != 70 {
		t.Fatal(count, err)
	}
}

func TestCleanupOverdueRetryPrecedesFreshArrivals(t *testing.T) {
	q, _ := resourceFixture(t)
	if err := q.CreateTransfer("zzz-old", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.RevokeTransfer("zzz-old"); err != nil {
		t.Fatal(err)
	}
	earlier := time.Now().Add(-time.Minute)
	if err := q.StartCleanupAttempt("transfer", "zzz-old", earlier); err != nil {
		t.Fatal(err)
	}
	if err := q.RecordCleanupAttempt("transfer", "zzz-old", "failed", "storage_delete_failed", earlier); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("new-%02d", i)
		if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
		if err := q.RevokeTransfer(id); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := q.DueCleanup("transfer", time.Now())
	if err != nil || len(tasks) != CleanupWorkBatch || tasks[0].ID != "zzz-old" {
		t.Fatal("fresh arrivals starved overdue retry", tasks, err)
	}
}
