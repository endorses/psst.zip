package cleanup

import (
	"context"
	"errors"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

const removalTimeout = 5 * time.Second
const deletionEntryBudget = 256

var errCleanupPending = errors.New("cleanup remains pending")
var errCleanupChildren = errors.New("cleanup is waiting for child transfers")

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
func removeStoredPayload(ctx context.Context, files store.FileStore, id string) (bool, error) {
	if bounded, ok := files.(store.BoundedDeleter); ok {
		return bounded.DeleteAllBounded(ctx, id, deletionEntryBudget)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	err := files.DeleteAll(id)
	return err == nil, err
}
func cleanupResult(q *database.Queries, kind, id, state, failure string, cause error) error {
	return errors.Join(cause, q.RecordCleanupAttempt(kind, id, state, failure, time.Now()))
}
func removeTransfer(ctx context.Context, q *database.Queries, files store.FileStore, id string, try bool, audit *database.SecurityEvent, actors ...*database.AdminActor) error {
	var err error
	if audit != nil {
		err = q.RevokeTransferAudited(id, *audit, actors...)
	} else {
		err = q.RevokeTransfer(id, actors...)
	}
	if err != nil {
		return err
	}
	store.CancelStreams(id)
	if err = q.StartCleanupAttempt("transfer", id, time.Now()); err != nil {
		return err
	}
	var unlock func()
	if try {
		unlock, err = store.TryLockTransfer(id)
	} else {
		unlock, err = store.AcquireTransfer(ctx, id)
	}
	if err != nil {
		return cleanupResult(q, "transfer", id, "busy", "", err)
	}
	defer unlock()
	if try && store.HasReaders(id) {
		return cleanupResult(q, "transfer", id, "busy", "", store.ErrResourceBusy)
	}
	if err = store.WaitForReaders(ctx, id); err != nil {
		return cleanupResult(q, "transfer", id, "busy", "", err)
	}
	done, err := removeStoredPayload(ctx, files, id)
	if err != nil {
		return cleanupResult(q, "transfer", id, "failed", "storage_delete_failed", err)
	}
	if !done {
		return cleanupResult(q, "transfer", id, "pending", "", errCleanupPending)
	}
	if err = q.DeleteTransfer(id); err != nil {
		return cleanupResult(q, "transfer", id, "failed", "metadata_delete_failed", err)
	}
	return nil
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
	var event database.SecurityEvent
	if audit != nil {
		event = *audit
	}
	if err := q.RevokeSlotQueued(id, event, actors...); err != nil {
		return err
	}
	store.CancelStreams("slot:" + id)
	store.CancelStreamScope(q.StreamNamespace() + "\x00slot:" + id)
	if err := q.StartCleanupAttempt("slot", id, time.Now()); err != nil {
		return err
	}
	var unlock func()
	var err error
	if try {
		unlock, err = store.TryLockSlot(id)
	} else {
		unlock, err = store.AcquireSlot(ctx, id)
	}
	if err != nil {
		return cleanupResult(q, "slot", id, "busy", "", err)
	}
	defer unlock()
	// An explicit small deletion may finish promptly; large inboxes retain their
	// durable queue. Background work never recursively processes child payloads.
	if !try {
		children, err := q.SlotCleanupChildren(id)
		if err != nil {
			return cleanupResult(q, "slot", id, "failed", "metadata_delete_failed", err)
		}
		for _, child := range children {
			if ctx.Err() != nil {
				break
			}
			_ = removeTransfer(ctx, q, files, child, false, nil)
		}
	}
	children, err := q.SlotHasTransfers(id)
	if err != nil {
		return cleanupResult(q, "slot", id, "failed", "metadata_delete_failed", err)
	}
	if children {
		return cleanupResult(q, "slot", id, "waiting_children", "", errCleanupChildren)
	}
	if err = q.DeleteSlot(id); err != nil {
		return cleanupResult(q, "slot", id, "failed", "metadata_delete_failed", err)
	}
	return nil
}
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
func removeExhaustedPayload(ctx context.Context, q *database.Queries, files store.FileStore, id string) error {
	transfer, err := q.GetTransfer(id)
	if err != nil {
		return err
	}
	// A full revocation/expiry supersedes exhausted-payload cleanup.
	if transfer.Status == "revoked" || !time.Now().Before(transfer.ExpiresAt) {
		return removeTransfer(ctx, q, files, id, true, nil)
	}
	if err = q.StartCleanupAttempt("transfer", id, time.Now()); err != nil {
		return err
	}
	unlock, err := store.TryLockTransfer(id)
	if err != nil {
		return cleanupResult(q, "transfer", id, "busy", "", err)
	}
	defer unlock()
	// Do not cancel the final authorized response merely because its allowance
	// was consumed. Retain the complete reservation until every reader closes.
	if store.HasReaders(id) {
		return cleanupResult(q, "transfer", id, "busy", "", store.ErrResourceBusy)
	}
	done, err := removeStoredPayload(ctx, files, id)
	if err != nil {
		return cleanupResult(q, "transfer", id, "failed", "storage_delete_failed", err)
	}
	if !done {
		return cleanupResult(q, "transfer", id, "pending", "", errCleanupPending)
	}
	if err = q.ReleaseCleanedPayloads(id); err != nil {
		return cleanupResult(q, "transfer", id, "failed", "metadata_delete_failed", err)
	}
	return nil
}
