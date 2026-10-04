package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestResourceLocksIndependentAndCancellable(t *testing.T) {
	locks := resourceLocks{entries: make(map[string]*resourceLock)}
	unlock, err := locks.acquire(context.Background(), "busy", true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := locks.acquire(context.Background(), "other", false)
	if err != nil {
		t.Fatal("unrelated resource blocked", err)
	}
	other()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := locks.acquire(ctx, "busy", true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := locks.acquire(context.Background(), "busy", false); !errors.Is(err, ErrResourceBusy) {
		t.Fatalf("try: %v", err)
	}
	unlock()
	unlock()
	if len(locks.entries) != 0 {
		t.Fatalf("leaked lock entries: %d", len(locks.entries))
	}
}
func TestResourceLockCardinalityBound(t *testing.T) {
	locks := resourceLocks{entries: make(map[string]*resourceLock)}
	releases := make([]func(), 0, maxLockKeys)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < maxLockKeys; i++ {
		release, err := locks.acquire(context.Background(), fmt.Sprint(i), false)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := locks.acquire(context.Background(), "overflow", false); !errors.Is(err, ErrResourceBusy) {
		t.Fatalf("capacity: %v", err)
	}
}
