package cleanup

import (
	"context"
	"errors"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

const removalTimeout = 5 * time.Second

// API deletions briefly wait for in-flight mutation. Background sweeps use the
// nonblocking variants so one busy transfer never delays unrelated cleanup.
func RemoveTransfer(q *database.Queries, files store.FileStore, id string) error {
	return RemoveTransferContext(context.Background(), q, files, id)
}
func RemoveTransferContext(ctx context.Context, q *database.Queries, files store.FileStore, id string) error {
	ctx, cancel := context.WithTimeout(ctx, removalTimeout)
	defer cancel()
	return removeTransfer(ctx, q, files, id, false)
}
func TryRemoveTransfer(q *database.Queries, files store.FileStore, id string) error {
	return removeTransfer(context.Background(), q, files, id, true)
}
func removeTransfer(ctx context.Context, q *database.Queries, files store.FileStore, id string, try bool) error {
	// Persist revocation even if an active upload prevents immediate removal.
	if err := q.RevokeTransfer(id); err != nil {
		return err
	}
	var unlock func()
	var err error
	if try {
		unlock, err = store.TryLockTransfer(id)
	} else {
		unlock, err = store.AcquireTransfer(ctx, id)
	}
	if err != nil {
		return err
	}
	defer unlock()
	if err := files.DeleteAll(id); err != nil {
		return err
	}
	return q.DeleteTransfer(id)
}
func RemoveSlot(q *database.Queries, files store.FileStore, id string) error {
	return RemoveSlotContext(context.Background(), q, files, id)
}
func RemoveSlotContext(ctx context.Context, q *database.Queries, files store.FileStore, id string) error {
	ctx, cancel := context.WithTimeout(ctx, removalTimeout)
	defer cancel()
	return removeSlot(ctx, q, files, id, false)
}
func TryRemoveSlot(q *database.Queries, files store.FileStore, id string) error {
	return removeSlot(context.Background(), q, files, id, true)
}
func removeSlot(ctx context.Context, q *database.Queries, files store.FileStore, id string, try bool) error {
	var unlock func()
	var err error
	if try {
		unlock, err = store.TryLockSlot(id)
	} else {
		unlock, err = store.AcquireSlot(ctx, id)
	}
	if err != nil {
		return err
	}
	defer unlock()
	children, err := q.RevokeSlot(id)
	if err != nil {
		return err
	}
	var cleanupError error
	for _, child := range children {
		cleanupError = errors.Join(cleanupError, removeTransfer(ctx, q, files, child, try))
	}
	if cleanupError != nil {
		return cleanupError
	}
	return q.DeleteSlot(id)
}
