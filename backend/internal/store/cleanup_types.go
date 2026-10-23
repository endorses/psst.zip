package store

import (
	"context"
	"errors"
)

// BoundedDeleter supports incremental deletion while holding a resource lock.
// done=false keeps quota reservations charged and resumes on the next attempt.
// Legacy stores retain their DeleteAll contract; callers must not simulate
// cancellation by leaking a new goroutine for every blocked storage operation.
type BoundedDeleter interface {
	DeleteAllBounded(context.Context, string, int) (done bool, err error)
}

var errCleanupEntryChanged = errors.New("storage entry changed during cleanup")
