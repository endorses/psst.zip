package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStreamCancellationAndReaderLease(t *testing.T) {
	id := "stream-lease-test"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	unregister, err := RegisterStream(id, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()
	release, err := AcquireReader(id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	CancelStreams(id)
	if ctx.Err() == nil {
		t.Fatal("revocation did not cancel stream")
	}
	if !HasReaders(id) {
		t.Fatal("cancellation released reader before file closed")
	}
	timeout, stop := context.WithTimeout(context.Background(), time.Millisecond)
	defer stop()
	if err := WaitForReaders(timeout, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("reader wait did not honor deadline", err)
	}
	release()
	release()
	if HasReaders(id) {
		t.Fatal("reader lease leaked")
	}
	if err := WaitForReaders(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}
