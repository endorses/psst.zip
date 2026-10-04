package store

import (
	"context"
	"sync"
)

// Shared across HTTP server objects and cleanup workers in this process.
// Database revocation is persisted before cancellation; a new operation must
// still pass the handler's live-resource check after registering here.
var activeStreams = struct {
	sync.Mutex
	operations map[string]map[*streamOperation]struct{}
	readers    map[string]*readerGroup
	count      int
}{operations: make(map[string]map[*streamOperation]struct{}), readers: make(map[string]*readerGroup)}

type streamOperation struct{ cancel context.CancelFunc }
type readerGroup struct {
	count int
	done  chan struct{}
}

func RegisterStream(id string, cancel context.CancelFunc) (func(), error) {
	activeStreams.Lock()
	if activeStreams.count >= 4096 || (len(activeStreams.operations[id]) == 0 && len(activeStreams.operations) >= maxLockKeys) {
		activeStreams.Unlock()
		return nil, ErrResourceBusy
	}
	operation := &streamOperation{cancel: cancel}
	if activeStreams.operations[id] == nil {
		activeStreams.operations[id] = make(map[*streamOperation]struct{})
	}
	activeStreams.operations[id][operation] = struct{}{}
	activeStreams.count++
	activeStreams.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			activeStreams.Lock()
			defer activeStreams.Unlock()
			delete(activeStreams.operations[id], operation)
			activeStreams.count--
			if len(activeStreams.operations[id]) == 0 {
				delete(activeStreams.operations, id)
			}
		})
	}, nil
}
func CancelStreams(id string) {
	activeStreams.Lock()
	operations := make([]*streamOperation, 0, len(activeStreams.operations[id]))
	for operation := range activeStreams.operations[id] {
		operations = append(operations, operation)
	}
	activeStreams.Unlock()
	for _, operation := range operations {
		operation.cancel()
	}
}

// AcquireReader must be called while holding the transfer mutation lock before
// opening/releasing the file for streaming. Release only after the file closes.
func AcquireReader(id string) (func(), error) {
	activeStreams.Lock()
	group := activeStreams.readers[id]
	if group == nil {
		if len(activeStreams.readers) >= maxLockKeys {
			activeStreams.Unlock()
			return nil, ErrResourceBusy
		}
		group = &readerGroup{done: make(chan struct{})}
		activeStreams.readers[id] = group
	}
	group.count++
	activeStreams.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			activeStreams.Lock()
			defer activeStreams.Unlock()
			group.count--
			if group.count == 0 {
				delete(activeStreams.readers, id)
				close(group.done)
			}
		})
	}, nil
}
func HasReaders(id string) bool {
	activeStreams.Lock()
	defer activeStreams.Unlock()
	return activeStreams.readers[id] != nil
}

// Call with the mutation lock held to prevent new readers joining while a
// deletion waits. Background cleanup checks HasReaders and never waits.
func WaitForReaders(ctx context.Context, id string) error {
	activeStreams.Lock()
	group := activeStreams.readers[id]
	activeStreams.Unlock()
	if group == nil {
		return nil
	}
	select {
	case <-group.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
