// Package reconcile verifies payloads represented by database file rows.
// It does not discover orphan disk paths or reconstruct lifetime abuse allowances.
package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

var ErrPayloadUnavailable = errors.New("stored payload is unavailable")
var ErrStorageCheckFailed = errors.New("storage check could not complete")
var errTruncateFailed = errors.New("payload truncation failed")

func issue(ctx context.Context, q *database.Queries, id, category, code string, cause error) error {
	if err := q.RecordFileReconciliationIssue(ctx, id, category, code); err != nil {
		_ = q.MarkReconciliationScanFailure(ctx)
		return ErrStorageCheckFailed
	}
	return cause
}

// CheckFile must be called with its transfer's mutation lock held. It never
// publishes newly discovered bytes, refunds reservations, or edits published data.
func CheckFile(ctx context.Context, q *database.Queries, fs store.FileStore, fileID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := q.ReconciliationFile(ctx, fileID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		_ = q.MarkReconciliationScanFailure(ctx)
		return ErrStorageCheckFailed
	}
	if f.PayloadDeleted || f.TransferStatus == "revoked" || f.CleanupPending {
		return q.ClearFileReconciliationIssue(ctx, fileID)
	}
	published := f.TransferStatus != "pending"
	if f.Size < 0 || f.UploadOffset < 0 || f.UploadOffset > f.Size || (published && (!f.UploadComplete || f.UploadOffset != f.Size)) {
		category, cause := "failed", ErrStorageCheckFailed
		if published {
			category, cause = "unavailable", ErrPayloadUnavailable
		}
		return issue(ctx, q, fileID, category, "metadata_invalid", cause)
	}
	info, err := fs.Inspect(f.TransferID + "/" + f.ID)
	if err != nil || info.Size < 0 {
		return issue(ctx, q, fileID, "failed", "inspect_failed", ErrStorageCheckFailed)
	}
	if published {
		if !info.Exists {
			return issue(ctx, q, fileID, "unavailable", "payload_missing", ErrPayloadUnavailable)
		}
		if info.Size != f.Size {
			return issue(ctx, q, fileID, "unavailable", "payload_size_mismatch", ErrPayloadUnavailable)
		}
		return q.ClearFileReconciliationIssue(ctx, fileID)
	}
	physical := info.Size
	if !info.Exists {
		physical = 0
	}
	offset := min(physical, f.UploadOffset)
	complete := f.UploadComplete && info.Exists && offset == f.Size
	if physical == f.UploadOffset && complete == f.UploadComplete {
		return q.ClearFileReconciliationIssue(ctx, fileID)
	}
	var truncate func() error
	if info.Exists && physical > f.UploadOffset {
		truncate = func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := fs.Truncate(f.TransferID+"/"+f.ID, f.UploadOffset); err != nil {
				return errTruncateFailed
			}
			return nil
		}
	}
	repaired, err := q.RepairPendingFile(ctx, f.File, offset, complete, truncate)
	if err != nil {
		code := "offset_update_failed"
		if errors.Is(err, errTruncateFailed) {
			code = "truncate_failed"
		}
		return issue(ctx, q, fileID, "failed", code, ErrStorageCheckFailed)
	}
	if !repaired {
		return store.ErrResourceBusy
	}
	return nil
}

// Sweep traverses at most 64 file rows and never waits for a resource lock.
// Busy or failed files retain visible issues and are retried on the next pass.
func Sweep(ctx context.Context, q *database.Queries, fs store.FileStore) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	batch, err := q.NextReconciliationBatch(ctx)
	if err != nil {
		_ = q.MarkReconciliationScanFailure(ctx)
		return err
	}
	var firstErr error
	attempted := 0
	for _, f := range batch.Files {
		if err := ctx.Err(); err != nil {
			firstErr = err
			break
		}
		attempted++
		batch.After = f.ID
		unlock, err := store.TryLockTransfer(f.TransferID)
		if err != nil {
			if writeErr := q.RecordFileReconciliationIssue(ctx, f.ID, "busy", "resource_busy"); writeErr != nil {
				batch.Failed = true
				if firstErr == nil {
					firstErr = ErrStorageCheckFailed
				}
			}
			continue
		}
		err = CheckFile(ctx, q, fs, f.ID)
		unlock()
		if errors.Is(err, store.ErrResourceBusy) {
			if writeErr := q.RecordFileReconciliationIssue(ctx, f.ID, "busy", "resource_busy"); writeErr != nil {
				batch.Failed = true
				if firstErr == nil {
					firstErr = ErrStorageCheckFailed
				}
			}
		} else if err != nil {
			if !errors.Is(err, ErrPayloadUnavailable) {
				batch.Failed = true
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if attempted < len(batch.Files) || ctx.Err() != nil {
		if firstErr == nil {
			firstErr = ctx.Err()
		}
		batch.Complete = false
		batch.Failed = true
		if attempted == 0 {
			batch.After = batch.Before
		}
	}
	// Persist through the last attempted row even after cancellation/deadline.
	// This short independent commit prevents a slow prefix starving later files.
	progressCtx, progressCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer progressCancel()
	if batch.Failed {
		_ = q.MarkReconciliationScanFailure(progressCtx)
	}
	if err = q.AdvanceReconciliationBatch(progressCtx, batch); err != nil {
		_ = q.MarkReconciliationScanFailure(progressCtx)
		return err
	}
	return firstErr
}

// Run is lifecycle-owned by main. ResetReconciliationScan belongs to startup,
// not this loop, so recreating a worker does not discard durable scan progress.
func Run(ctx context.Context, q *database.Queries, fs store.FileStore, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	if ctx.Err() != nil {
		return
	}
	_ = Sweep(ctx, q, fs)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = Sweep(ctx, q, fs)
		}
	}
}
