package cleanup

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

// Both incident and normal expiry cleanup share a persistent, fair queue.
// Discovery inspects at most 64 transfers and 64 slots per tick; each queue
// services at most 16 items, and failing resources have a persisted retry delay.
func RunIncidentCleanup(ctx context.Context, q *database.Queries, fs store.FileStore) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := SweepPending(ctx, q, fs); err != nil {
			log.Print("cleanup: bounded cleanup remains pending")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func SweepPending(ctx context.Context, q *database.Queries, fs store.FileStore) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	discoveryErr := q.DiscoverCleanup(time.Now())
	var workErr error
	for _, kind := range []string{"transfer", "slot"} {
		tasks, err := q.DueCleanup(kind, time.Now())
		if err != nil {
			workErr = errors.Join(workErr, err)
			continue
		}
		for _, task := range tasks {
			if err := ctx.Err(); err != nil {
				return errors.Join(discoveryErr, workErr, err)
			}
			if task.Kind == "slot" {
				err = removeSlot(ctx, q, fs, task.ID, true, nil)
			} else if task.Mode == "payload" {
				err = removeExhaustedPayload(ctx, q, fs, task.ID)
			} else {
				err = removeTransfer(ctx, q, fs, task.ID, true, nil)
			}
			if err != nil && !errors.Is(err, store.ErrResourceBusy) && !errors.Is(err, errCleanupPending) && !errors.Is(err, errCleanupChildren) {
				workErr = errors.Join(workErr, err)
			}
		}
	}
	return errors.Join(discoveryErr, workErr)
}
func TryRemoveEmptyRevokedSlot(q *database.Queries, fs store.FileStore, id string) error {
	return TryRemoveSlot(q, fs, id)
}
