package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

type orphanStore interface {
	InventoryDirectory(context.Context, string, store.InventoryCursor, int) (store.InventoryPage, error)
	RemoveOrphan(context.Context, store.InventoryEntry) (bool, error)
}

func sameOrphanDirectory(a, b store.InventoryIdentity) bool {
	return a.Device == b.Device && a.Inode == b.Inode && a.Mode == b.Mode
}

// SweepOrphansAt performs bounded, durable work with an injectable wall clock.
// Each step enumerates at most two 16-entry pages and retries eight candidates.
// No filesystem operation creates a goroutine or waits for a resource lock.
func SweepOrphansAt(ctx context.Context, q *database.Queries, fs store.FileStore, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	inventory, ok := fs.(orphanStore)
	if !ok {
		_ = q.MarkOrphanFailure(ctx)
		return store.ErrInventoryUnsupported
	}
	var first error
	record := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, root := range []bool{true, false} {
		if ctx.Err() != nil {
			record(ctx.Err())
			break
		}
		d, err := q.NextOrphanDirectory(ctx, root)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			record(err)
			continue
		}
		page, err := inventory.InventoryDirectory(ctx, d.Name, d.Cursor, d.Budget)
		if err == nil && d.Name != "" && (!sameOrphanDirectory(d.Entry.Root, page.Next.Root) || !sameOrphanDirectory(d.Entry.File, page.Next.Directory)) {
			err = store.ErrInventoryChanged
		}
		// Persist the last bounded step even if its filesystem operation exhausted
		// the scheduling deadline. Progress is committed independently and briefly.
		progress, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
		if err != nil {
			record(err)
			record(q.FailOrphanDirectory(progress, d))
		} else {
			record(q.CommitOrphanPage(progress, d, page, now))
		}
		stop()
	}
	candidates, err := q.NextOrphanCandidates(ctx)
	record(err)
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			record(ctx.Err())
			break
		}
		category := "pending"
		switch {
		case candidate.Entry.Unsupported:
			category = "unsupported"
		case now.Before(candidate.FirstSeenAt.Add(database.OrphanGracePeriod)):
		default:
			id := candidate.Entry.Directory
			if id == "" {
				id = candidate.Entry.Name
			}
			unlock, lockErr := store.TryLockTransfer(id)
			if lockErr != nil {
				category = "busy"
			} else {
				if store.HasReaders(id) {
					category = "busy"
				} else {
					removed, removeErr := q.RemoveOrphanCandidate(ctx, candidate, now, func() (bool, error) { return inventory.RemoveOrphan(ctx, candidate.Entry) })
					if removeErr != nil {
						category = "failed"
						record(removeErr)
						if errors.Is(removeErr, store.ErrInventoryChanged) {
							record(q.RetireChangedOrphan(ctx, candidate))
						}
					} else if !removed {
						category = "busy"
					}
				}
				unlock()
			}
		}
		record(q.TouchOrphanCandidate(ctx, candidate, category))
	}
	if first != nil && !errors.Is(first, context.Canceled) {
		progress, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = q.MarkOrphanFailure(progress)
		stop()
	}
	if err := q.FinishOrphanPass(ctx, now); err != nil {
		record(err)
		progress, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = q.MarkOrphanFailure(progress)
		stop()
	}
	return first
}

func RunOrphans(ctx context.Context, q *database.Queries, fs store.FileStore, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	if ctx.Err() != nil {
		return
	}
	_ = SweepOrphansAt(ctx, q, fs, time.Now())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = SweepOrphansAt(ctx, q, fs, time.Now())
		}
	}
}
