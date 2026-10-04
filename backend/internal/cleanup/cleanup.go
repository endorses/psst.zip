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
	if err := w.queries.PruneAuthentication(); err != nil {
		log.Printf("cleanup: expired authentication credentials: %v", err)
	}
	if _, err := w.queries.PruneTrafficHistory(time.Now()); err != nil {
		log.Printf("cleanup: traffic history retention failed")
	}
	w.sweepTransfers()
	w.sweepExhaustedPayloads()
	w.sweepSlots()
}

func (w *Worker) sweepTransfers() {
	ids, err := w.queries.ExpiredTransferIDs()
	if err != nil {
		log.Printf("cleanup: list expired transfers: %v", err)
		return
	}
	for _, id := range ids {
		if err := TryRemoveTransfer(w.queries, w.files, id); err != nil {
			log.Printf("cleanup: expired or revoked transfer removal failed")
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
		if err := TryRemoveSlot(w.queries, w.files, id); err != nil {
			log.Printf("cleanup: slot removal failed")
		} else {
			log.Printf("cleanup: expired slot removed")
		}
	}
}

// Exhausted payloads wait until all authorized readers close, retaining their
// physical storage reservation while the final permitted response finishes.
// Metadata and the encrypted manifest remain available until the normal TTL.
func (w *Worker) sweepExhaustedPayloads() {
	ids, err := w.queries.ExhaustedTransferIDs()
	if err != nil {
		log.Printf("cleanup: list exhausted transfers: %v", err)
		return
	}
	for _, id := range ids {
		unlock, err := store.TryLockTransfer(id)
		if err != nil {
			log.Printf("cleanup: busy exhausted transfer; retry next sweep")
			continue
		}
		if store.HasReaders(id) {
			unlock()
			continue
		}
		err = w.files.DeleteAll(id)
		if err == nil {
			err = w.queries.ReleasePayloads(id)
		}
		unlock()
		if err != nil {
			log.Printf("cleanup: exhausted payload removal failed")
		}
	}
}
