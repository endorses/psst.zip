package reconcile

import (
	"context"
	"testing"
	"time"
)

func TestCanceledCounterWorkerDoesNoStartupWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A canceled worker must not even need a live database connection.
	RunCounters(ctx, nil, 0)
}

func TestCounterWorkerRepairsAndStops(t *testing.T) {
	f := setup(t)
	f.file(t, "counter-worker", "file", 7, 3, false, nil)
	if _, err := f.db.Exec(`UPDATE admin_resource_totals SET file_count=17,reserved_bytes=999,occupied_bytes=99 WHERE kind='transfer' AND resource_id='counter-worker'`); err != nil {
		t.Fatal(err)
	}
	if err := f.q.ResetCounterRebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunCounters(ctx, f.q, time.Millisecond)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("counter worker did not stop")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := f.q.CounterRebuildStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.State == "checked" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("counter worker did not complete: %+v", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var count, reserved, occupied int64
	if err := f.db.QueryRow(`SELECT file_count,reserved_bytes,occupied_bytes FROM admin_resource_totals WHERE kind='transfer' AND resource_id='counter-worker'`).Scan(&count, &reserved, &occupied); err != nil || count != 1 || reserved != 7 || occupied != 3 {
		t.Fatalf("worker did not repair source totals: %d %d %d %v", count, reserved, occupied, err)
	}
}
