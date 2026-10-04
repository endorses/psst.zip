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
			log.Printf("cleanup: remove expired or revoked transfer %s: %v", id, err)
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
			log.Printf("cleanup: delete slot %s: %v", id, err)
		} else {
			log.Printf("cleanup: removed expired slot %s", id)
		}
	}
}

// File readers open a handle before their GET allowance is reserved. Removing
// exhausted disk payloads therefore leaves already-open downloads readable;
// metadata and the encrypted manifest remain available until the normal TTL.
func (w *Worker) sweepExhaustedPayloads() {
	ids, err := w.queries.ExhaustedTransferIDs()
	if err != nil {
		log.Printf("cleanup: list exhausted transfers: %v", err)
		return
	}
	for _, id := range ids {
		unlock, err := store.TryLockTransfer(id)
		if err != nil {
			log.Printf("cleanup: busy exhausted transfer %s; retry next sweep", id)
			continue
		}
		err = w.files.DeleteAll(id)
		unlock()
		if err != nil {
			log.Printf("cleanup: delete exhausted payloads for transfer %s: %v", id, err)
		}
	}
}
