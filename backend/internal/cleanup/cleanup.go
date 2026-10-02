package cleanup

import (
	"context"
	"log"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

// Worker periodically removes expired transfers and slots, along with their
// files on disk.
type Worker struct {
	queries  *database.Queries
	files    store.FileStore
	interval time.Duration
}

// NewWorker creates a cleanup worker.
func NewWorker(q *database.Queries, fs store.FileStore, interval time.Duration) *Worker {
	return &Worker{queries: q, files: fs, interval: interval}
}

// Run blocks until ctx is cancelled, sweeping expired data on each tick.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run once immediately at startup.
	w.sweep()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep()
		}
	}
}

func (w *Worker) sweep() {
	w.sweepTransfers()
	w.sweepSlots()
}

func (w *Worker) sweepTransfers() {
	ids, err := w.queries.ExpiredTransferIDs()
	if err != nil {
		log.Printf("cleanup: list expired transfers: %v", err)
		return
	}
	for _, id := range ids {
		if err := w.files.DeleteAll(id); err != nil {
			log.Printf("cleanup: delete files for transfer %s: %v", id, err)
			continue
		}
		if err := w.queries.DeleteTransfer(id); err != nil {
			log.Printf("cleanup: delete transfer %s: %v", id, err)
		} else {
			log.Printf("cleanup: removed expired transfer %s", id)
		}
	}
}

func (w *Worker) sweepSlots() {
	ids, err := w.queries.ExpiredSlotIDs()
	if err != nil {
		log.Printf("cleanup: list expired slots: %v", err)
		return
	}
	for _, id := range ids {
		if err := w.queries.DeleteSlot(id); err != nil {
			log.Printf("cleanup: delete slot %s: %v", id, err)
		} else {
			log.Printf("cleanup: removed expired slot %s", id)
		}
	}
}
