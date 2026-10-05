package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInboxMembershipUsesExactIndexesAndRechecksOwner(t *testing.T) {
	q, _ := resourceFixture(t)
	if err := q.CreateUser(User{ID: "owner", Username: "owner", Role: "user", PasswordHash: []byte("test")}, false); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if err := q.CreateReceiveSlot("inbox", until, nil, "owner", 2, "key", 0); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlotTransfer("inbox", "child", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	entry, err := q.InboxTransferMembershipContext(context.Background(), "inbox", "child", "owner")
	if err != nil || entry.SlotID != "inbox" || entry.TransferID != "child" || entry.RecipientPublicKey != "key" {
		t.Fatalf("exact membership: %+v %v", entry, err)
	}
	for _, args := range [][3]string{{"inbox", "child", "wrong-owner"}, {"wrong-inbox", "child", "owner"}, {"inbox", "wrong-child", "owner"}} {
		if entry, err := q.InboxTransferMembershipContext(context.Background(), args[0], args[1], args[2]); !errors.Is(err, sql.ErrNoRows) || entry != nil {
			t.Fatalf("mismatch returned membership: %+v %v", entry, err)
		}
	}
	rows, err := q.db.Query("EXPLAIN QUERY PLAN "+inboxTransferMembershipQuery, "inbox", "child", "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, rows)
	probes := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(detail, "SEARCH ") || !strings.Contains(detail, "INDEX") {
			t.Fatalf("membership must use exact indexes: %s", detail)
		}
		probes++
	}
	if err := rows.Err(); err != nil || probes != 3 {
		t.Fatalf("want three indexed probes, got %d: %v", probes, err)
	}
}

func TestInboxMembershipQueriesHonorContextWhilePoolOccupied(t *testing.T) {
	q, _ := resourceFixture(t)
	q.db.SetMaxOpenConns(1)
	held, err := q.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, held)
	for _, query := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := q.InboxOwnerContext(ctx, "inbox"); return err },
		func(ctx context.Context) error {
			_, err := q.InboxTransferMembershipContext(ctx, "inbox", "child", "owner")
			return err
		},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		started := time.Now()
		err := query(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
			t.Fatalf("query ignored deadline: %v after %v", err, time.Since(started))
		}
	}
}
