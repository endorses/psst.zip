package reconcile

import (
	"context"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

// RunCounters owns one bounded database reconstruction step at a time. Startup
// resets coverage separately so this loop preserves durable partial work.
func RunCounters(ctx context.Context, q *database.Queries, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	step := func() {
		if ctx.Err() != nil {
			return
		}
		stepCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		// The database layer records fixed failure state; raw SQL errors must
		// not be logged or exposed to the administrator response.
		_ = q.RebuildCounterBatch(stepCtx, 64)
	}
	if ctx.Err() != nil {
		return
	}
	step()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			step()
		}
	}
}
