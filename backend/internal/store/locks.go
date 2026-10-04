package store

import (
	"context"
	"errors"
	"sync"
)

// Resource locks are keyed, so a stalled upload cannot hold an unrelated
// transfer's lock. Both active keys and queued callers have finite bounds.
const maxLockKeys = 1024
const maxLockReferences = 17 // one holder and at most sixteen waiting callers

var ErrResourceBusy = errors.New("resource is busy; retry later")

type resourceLock struct {
	token chan struct{}
	refs  int
}
type resourceLocks struct {
	mu      sync.Mutex
	entries map[string]*resourceLock
}

var transferLocks = resourceLocks{entries: make(map[string]*resourceLock)}
var slotLocks = resourceLocks{entries: make(map[string]*resourceLock)}

func (locks *resourceLocks) acquire(ctx context.Context, id string, wait bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	locks.mu.Lock()
	entry := locks.entries[id]
	if entry == nil {
		if len(locks.entries) >= maxLockKeys {
			locks.mu.Unlock()
			return nil, ErrResourceBusy
		}
		entry = &resourceLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		locks.entries[id] = entry
	}
	if entry.refs >= maxLockReferences {
		locks.mu.Unlock()
		return nil, ErrResourceBusy
	}
	entry.refs++
	locks.mu.Unlock()
	releaseRef := func() {
		locks.mu.Lock()
		defer locks.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(locks.entries, id)
		}
	}
	if wait {
		select {
		case <-entry.token:
		case <-ctx.Done():
			releaseRef()
			return nil, ctx.Err()
		}
	} else {
		select {
		case <-entry.token:
		default:
			releaseRef()
			return nil, ErrResourceBusy
		}
	}
	if err := ctx.Err(); err != nil {
		entry.token <- struct{}{}
		releaseRef()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { entry.token <- struct{}{}; releaseRef() }) }, nil
}

func AcquireTransfer(ctx context.Context, id string) (func(), error) {
	return transferLocks.acquire(ctx, id, true)
}
func AcquireSlot(ctx context.Context, id string) (func(), error) {
	return slotLocks.acquire(ctx, id, true)
}
func TryLockTransfer(id string) (func(), error) {
	return transferLocks.acquire(context.Background(), id, false)
}
func TryLockSlot(id string) (func(), error) {
	return slotLocks.acquire(context.Background(), id, false)
}
