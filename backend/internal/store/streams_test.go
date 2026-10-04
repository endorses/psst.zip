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

func TestIncidentStreamScopeCancellationIsIsolated(t *testing.T) {
	a, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	b, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	c, cancelC := context.WithCancel(context.Background())
	defer cancelC()
	ua, err := RegisterScopedStream("incident-a", cancelA, "db-a:payload", "db-a:owner-one")
	if err != nil {
		t.Fatal(err)
	}
	defer ua()
	ub, err := RegisterScopedStream("incident-b", cancelB, "db-a:payload", "db-a:owner-two")
	if err != nil {
		t.Fatal(err)
	}
	defer ub()
	uc, err := RegisterScopedStream("incident-c", cancelC, "db-b:payload", "db-b:owner-one")
	if err != nil {
		t.Fatal(err)
	}
	defer uc()
	CancelStreamScope("db-a:owner-one")
	if a.Err() == nil || b.Err() != nil || c.Err() != nil {
		t.Fatal("owner incident crossed scopes")
	}
	CancelStreamScope("db-a:payload")
	if b.Err() == nil || c.Err() != nil {
		t.Fatal("global pause crossed independent database scope")
	}
}
