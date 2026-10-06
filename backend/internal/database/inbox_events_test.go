package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestInboxEventQueryCancelsWhileWaitingForDatabaseConnection(t *testing.T) {
	db, err := openFixture(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	q := NewQueries(db)
	db.SetMaxOpenConns(1)
	held, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			closeFixture(t, held)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := q.InboxEventSessions(ctx, "inbox", "owner", []string{"session"}); done <- err }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for db.Stats().WaitCount == 0 {
		select {
		case <-deadline.C:
			t.Fatal("event query never waited for the occupied database connection")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("query did not preserve cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("query ignored cancellation while database connection was occupied")
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	released = true
	if active, err := q.InboxEventSessions(context.Background(), "inbox", "owner", []string{"session"}); err != nil || len(active) != 0 {
		t.Fatalf("canceled query leaked database capacity: %v %v", active, err)
	}
}
