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
func RemoveTransferContext(ctx context.Context, q *database.Queries, files store.FileStore, id string, actors ...*database.AdminActor) error {
	ctx, cancel := context.WithTimeout(ctx, removalTimeout)
	defer cancel()
	return removeTransfer(ctx, q, files, id, false, nil, actors...)
}
func TryRemoveTransfer(q *database.Queries, files store.FileStore, id string) error {
	return removeTransfer(context.Background(), q, files, id, true, nil)
}
func removeTransfer(ctx context.Context, q *database.Queries, files store.FileStore, id string, try bool, audit *database.SecurityEvent, actors ...*database.AdminActor) error {
	// Persist revocation even if an active upload prevents immediate removal.
	var revokeErr error
	if audit != nil {
		revokeErr = q.RevokeTransferAudited(id, *audit, actors...)
	} else {
		revokeErr = q.RevokeTransfer(id, actors...)
	}
	if err := revokeErr; err != nil {
		return err
	}
	store.CancelStreams(id)
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
	if try && store.HasReaders(id) {
		return store.ErrResourceBusy
	}
	if err := store.WaitForReaders(ctx, id); err != nil {
		return err
	}
	if err := files.DeleteAll(id); err != nil {
		return err
	}
	return q.DeleteTransfer(id)
}
func RemoveSlot(q *database.Queries, files store.FileStore, id string) error {
	return RemoveSlotContext(context.Background(), q, files, id)
}
func RemoveSlotContext(ctx context.Context, q *database.Queries, files store.FileStore, id string, actors ...*database.AdminActor) error {
	ctx, cancel := context.WithTimeout(ctx, removalTimeout)
	defer cancel()
	return removeSlot(ctx, q, files, id, false, nil, actors...)
}
func TryRemoveSlot(q *database.Queries, files store.FileStore, id string) error {
	return removeSlot(context.Background(), q, files, id, true, nil)
}
func removeSlot(ctx context.Context, q *database.Queries, files store.FileStore, id string, try bool, audit *database.SecurityEvent, actors ...*database.AdminActor) error {
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
	var children []string
	if audit != nil {
		children, err = q.RevokeSlotAudited(id, *audit, actors...)
	} else {
		children, err = q.RevokeSlot(id, actors...)
	}
	if err != nil {
		return err
	}
	store.CancelStreams("slot:" + id)
	var cleanupError error
	for _, child := range children {
		cleanupError = errors.Join(cleanupError, removeTransfer(ctx, q, files, child, try, nil))
	}
	if cleanupError != nil {
		return cleanupError
	}
	return q.DeleteSlot(id)
}

// Audited variants carry an explicitly authorized actor/capability into the
// revocation transaction. Background retries deliberately omit this event.
func RemoveTransferAuditedContext(ctx context.Context, q *database.Queries, files store.FileStore, id string, audit database.SecurityEvent, actors ...*database.AdminActor) error {
	ctx, cancel := context.WithTimeout(ctx, removalTimeout)
	defer cancel()
	return removeTransfer(ctx, q, files, id, false, &audit, actors...)
}
func RemoveSlotAuditedContext(ctx context.Context, q *database.Queries, files store.FileStore, id string, audit database.SecurityEvent, actors ...*database.AdminActor) error {
	ctx, cancel := context.WithTimeout(ctx, removalTimeout)
	defer cancel()
	return removeSlot(ctx, q, files, id, false, &audit, actors...)
}
