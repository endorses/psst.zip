package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func textPointer(s string) *string { return &s }
func TestLinkTitleValidationAndOwnerPersistence(t *testing.T) {
	for _, input := range []string{"control\n", "a\x7f", "a\u0085", strings.Repeat("😀", 201), string([]byte{0xff})} {
		if _, err := NormalizeLinkTitle(&input); !errors.Is(err, ErrInvalidTitle) {
			t.Fatalf("accepted invalid title %q", input)
		}
	}
	for _, input := range []string{"", " \u2000", strings.Repeat("😀", 200), "  音楽 <title>  "} {
		if _, err := NormalizeLinkTitle(&input); err != nil {
			t.Fatal(err)
		}
	}
	q := historyFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateTransferWithTitle("send", until, 0, nil, "alice", textPointer("  shared title  ")); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateReceiveSlot("slot", until, nil, "alice", 2, "public-key", 0, textPointer("Inbox")); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlotTransfer("slot", "child", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"transfer", "slot"} {
		id := "send"
		if kind == "slot" {
			id = "slot"
		}
		for _, actor := range []string{"bob", "admin", ""} {
			if err := q.RenameLinkTitle(context.Background(), kind, id, actor, textPointer("unauthorized")); !errors.Is(err, ErrTitleOwnership) {
				t.Fatal(actor, err)
			}
		}
	}
	if err := q.RenameLinkTitle(context.Background(), "transfer", "child", "alice", textPointer("override")); !errors.Is(err, ErrTitleOwnership) {
		t.Fatal("child can override invitation title", err)
	}
	child, err := q.GetTransfer("child")
	if err != nil || child.Title == nil || *child.Title != "Inbox" {
		t.Fatal(child, err)
	}
	if err = q.RenameLinkTitle(context.Background(), "slot", "slot", "alice", textPointer(" Updated ")); err != nil {
		t.Fatal(err)
	}
	child, err = q.GetTransfer("child")
	if err != nil || *child.Title != "Updated" {
		t.Fatal(child, err)
	}
	if err = q.RenameLinkTitle(context.Background(), "transfer", "send", "alice", textPointer("  ")); err != nil {
		t.Fatal(err)
	}
	send, err := q.GetTransfer("send")
	if err != nil || send.Title != nil {
		t.Fatal(send, err)
	}
}
func TestHistoryKindFilterQueriesBeyondFirstPageAndScopesCursor(t *testing.T) {
	q := historyFixture(t)
	until := time.Now().Add(time.Hour)
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("send-%03d", i)
		if err := q.CreateTransfer(id, until, 0, nil, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := q.CreateSlot(fmt.Sprintf("slot-%03d", i), until, nil, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := q.AccountHistoryPage(context.Background(), "alice", false, 2, "", "slot")
	if err != nil || len(first.Resources) != 2 || first.NextCursor == nil {
		t.Fatal(first, err)
	}
	for _, item := range first.Resources {
		if item.Slot == nil {
			t.Fatal("kind filter returned a transfer")
		}
	}
	if _, err = q.AccountHistoryPage(context.Background(), "alice", false, 2, *first.NextCursor, "transfer"); !errors.Is(err, ErrInvalidPage) {
		t.Fatal("cursor accepted across filters", err)
	}
	next, err := q.AccountHistoryPage(context.Background(), "alice", false, 2, *first.NextCursor, "slot")
	if err != nil || len(next.Resources) != 1 || next.NextCursor != nil {
		t.Fatal(next, err)
	}
}
func TestExhaustedSnapshotMultiFileConcurrentRestartAndReceipt(t *testing.T) {
	q, path := resourceFixture(t)
	completeDownloadFixture(t, q, 1, 2)
	allowed, err := q.ReserveFileDownload("download", "first")
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	partial, err := q.GetTransfer("download")
	if err != nil || partial.Exhausted {
		t.Fatal("partial exhaustion closed whole link", partial, err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, other)
	start := make(chan struct{})
	result := make(chan bool, 2)
	var workers sync.WaitGroup
	for _, allocator := range []*Queries{q, NewQueries(other)} {
		workers.Add(1)
		go func(allocator *Queries) {
			defer workers.Done()
			<-start
			admitted, e := allocator.ReserveFileDownload("download", "second")
			if e != nil {
				t.Error(e)
			}
			result <- admitted
		}(allocator)
	}
	close(start)
	workers.Wait()
	close(result)
	count := 0
	for admitted := range result {
		if admitted {
			count++
		}
	}
	if count != 1 {
		t.Fatal("last allowance not atomic", count)
	}
	final, err := NewQueries(other).GetTransfer("download")
	if err != nil || !final.Exhausted || final.Status != "complete" || final.DownloadedAt.Valid {
		t.Fatal("reopened exhausted snapshot incorrectly reports saved", final, err)
	}
	if ack, err := q.AcknowledgeDownload("download", time.Now()); err != nil || !ack {
		t.Fatal("exhaustion invalidated final receipt", ack, err)
	}
	final, err = q.GetTransfer("download")
	if err != nil || !final.Exhausted || !final.DownloadedAt.Valid {
		t.Fatal(final, err)
	}
}

func TestExhaustionMetadataDoesNotReadFileRows(t *testing.T) {
	q, _ := resourceFixture(t)
	completeDownloadFixture(t, q, 1, 1)
	allowed, err := q.ReserveFileDownload("download", "first")
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	before, err := q.GetTransfer("download")
	if err != nil || !before.Exhausted {
		t.Fatal(before, err)
	}
	// If the read touched payload metadata, this would fail. It also protects
	// against unbounded scans in restored transfers exceeding today's file cap.
	if _, err = q.db.Exec(`DROP TABLE files`); err != nil {
		t.Fatal(err)
	}
	after, err := q.GetTransfer("download")
	if err != nil || !after.Exhausted || after.DownloadCount != before.DownloadCount {
		t.Fatal(after, err)
	}
}
func TestEmptyAndUnlimitedTransfersNeverBecomeExhausted(t *testing.T) {
	q, _ := resourceFixture(t)
	if err := q.CreateTransfer("empty", time.Now().Add(time.Hour), 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CompleteTransfer("empty"); err != nil {
		t.Fatal(err)
	}
	empty, err := q.GetTransfer("empty")
	if err != nil || empty.Exhausted {
		t.Fatal(empty, err)
	}
	completeDownloadFixture(t, q, 0, 1)
	for i := 0; i < 3; i++ {
		if admitted, err := q.ReserveFileDownload("download", "first"); err != nil || !admitted {
			t.Fatal(admitted, err)
		}
	}
	unlimited, err := q.GetTransfer("download")
	if err != nil || unlimited.Exhausted || unlimited.DownloadCount != 3 {
		t.Fatal(unlimited, err)
	}
}
