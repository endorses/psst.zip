package cleanup

import (
	"context"
	"log"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

// Worker bounds operational metadata maintenance and offers the same durable
// cleanup queue as the faster incident monitor. Neither performs a full scan.
type Worker struct {
	queries  *database.Queries
	files    store.FileStore
	interval time.Duration
}

func NewWorker(q *database.Queries, fs store.FileStore, interval time.Duration) *Worker {
	return &Worker{q, fs, interval}
}
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.sweepContext(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweepContext(ctx)
		}
	}
}
func (w *Worker) sweep() { w.sweepContext(context.Background()) }
func (w *Worker) sweepContext(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if _, err := w.queries.PruneSecurityEvents(time.Now()); err != nil {
		log.Print("cleanup: security activity retention failed")
	}
	if err := w.queries.PruneAuthentication(); err != nil {
		log.Print("cleanup: authentication retention failed")
	}
	if _, err := w.queries.PruneTrafficHistory(time.Now()); err != nil {
		log.Print("cleanup: traffic history retention failed")
	}
	if err := SweepPending(ctx, w.queries, w.files); err != nil {
		log.Print("cleanup: bounded cleanup remains pending")
	}
}
