package store

import (
	"hash/fnv"
	"sync"
)

// API servers and cleanup workers in this process share the same bounded locks.
// Slot locks and transfer locks are separate: slot removal holds its slot lock
// while removing each child, acquiring only one transfer lock at a time.
var transferLocks [256]sync.Mutex
var slotLocks [256]sync.Mutex

func lockResource(locks *[256]sync.Mutex, id string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	mu := &locks[h.Sum32()%uint32(len(locks))]
	mu.Lock()
	return mu.Unlock
}

func LockTransfer(id string) func() { return lockResource(&transferLocks, id) }
func LockSlot(id string) func()     { return lockResource(&slotLocks, id) }
