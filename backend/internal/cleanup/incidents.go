package cleanup

import (
	"context"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

// Incident cleanup works in small cursor batches, advancing past failed/busy
// resources so one damaged path cannot indefinitely block other revocations.
// Revoked rows remain the durable queue across restarts and all failures.
func RunIncidentCleanup(ctx context.Context, q *database.Queries, fs store.FileStore) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	transferAfter, slotAfter := "", ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, kind := range []string{"transfer", "slot"} {
			after := &transferAfter
			if kind == "slot" {
				after = &slotAfter
			}
			*after = incidentCleanupBatch(ctx, q, fs, kind, *after)
		}
	}
}

// Child transfers have their own bounded cleanup batch. Never recursively walk
// an unbounded inbox while processing one incident slot item.
func TryRemoveEmptyRevokedSlot(q *database.Queries, fs store.FileStore, id string) error {
	hasChildren, err := q.SlotHasTransfers(id)
	if err != nil {
		return err
	}
	if hasChildren {
		return store.ErrResourceBusy
	}
	return TryRemoveSlot(q, fs, id)
}

func incidentCleanupBatch(ctx context.Context, q *database.Queries, fs store.FileStore, kind, after string) string {
	ids, err := q.RevokedResourceBatch(kind, after, 16)
	if err != nil {
		return after
	}
	if len(ids) == 0 {
		return ""
	}
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return after
		default:
		}
		after = id
		if kind == "transfer" {
			_ = TryRemoveTransfer(q, fs, id)
		} else {
			_ = TryRemoveEmptyRevokedSlot(q, fs, id)
		}
	}
	return after
}
