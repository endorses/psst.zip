package database

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func historyWatermark(t *testing.T, q *Queries, actor string) string {
	t.Helper()
	page, err := q.AccountHistoryPage(context.Background(), actor, false, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Generation == "" || page.SyncCursor == "" {
		t.Fatal("snapshot lacks consistent watermark")
	}
	return page.SyncCursor
}
func historyDeltas(t *testing.T, q *Queries, cursor string) map[string]HistoryChange {
	t.Helper()
	out := map[string]HistoryChange{}
	for i := 0; i < 100; i++ {
		page, err := q.AccountHistoryChanges(context.Background(), "alice", 3, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range page.Changes {
			key := event.Kind + ":" + event.ID
			if previous, ok := out[key]; !ok || event.Revision >= previous.Revision {
				out[key] = event
			}
		}
		if !page.HasMore {
			return out
		}
		if page.NextCursor == cursor {
			t.Fatal("nonadvancing continuation")
		}
		cursor = page.NextCursor
	}
	t.Fatal("catch-up failed to terminate")
	return nil
}
func TestHistorySyncMutationsAndParentSummary(t *testing.T) {
	q := historyFixture(t)
	ctx := context.Background()
	until := time.Now().Add(time.Hour)
	cursor := historyWatermark(t, q, "alice")
	if err := q.CreateReceiveSlot("inbox", until, nil, "alice", 2, "key", 0); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("send", until, 1, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlotTransfer("inbox", "private-child", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "private-child", 512); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("file", 512, true); err != nil {
		t.Fatal(err)
	}
	if err := q.CompleteTransfer("private-child"); err != nil {
		t.Fatal(err)
	}
	title := "Shared title"
	if err := q.RenameLinkTitle(ctx, "slot", "inbox", "alice", &title); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("sent-file", "send", 123); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("sent-file", 123, true); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("send", []byte("ciphertext")); err != nil {
		t.Fatal(err)
	}
	if err := q.CompleteTransfer("send"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ReserveFileDownload("send", "sent-file"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AcknowledgeDownload("send", time.Now()); err != nil {
		t.Fatal(err)
	}
	changes := historyDeltas(t, q, cursor)
	parent := changes["slot:inbox"].Resource
	if parent == nil || parent.Slot.Title == nil || *parent.Slot.Title != title || parent.Summary.CompletedFiles == nil || *parent.Summary.CompletedFiles != 1 || *parent.Summary.TotalSize != 512 {
		t.Fatalf("invalid parent summary: %+v", parent)
	}
	if child, ok := changes["transfer:private-child"]; ok && child.Action != "remove" {
		t.Fatal("private child leaked", child)
	}
	send := changes["transfer:send"].Resource
	if send == nil || !send.HasManifest || !send.Transfer.Exhausted || !send.Transfer.DownloadedAt.Valid || *send.Summary.FileCount != 1 {
		t.Fatalf("missing download/manifest/completion facts: %+v", send)
	}
	// Repair writes and old-record renames are deltas too, not merely new arrivals.
	cursor = historyWatermark(t, q, "alice")
	if _, err := q.db.Exec(`UPDATE admin_resource_totals SET inbox_known=0 WHERE kind='slot' AND resource_id='inbox'; UPDATE transfers SET created_at='2000-01-01',title='Older record' WHERE id='send'`); err != nil {
		t.Fatal(err)
	}
	changes = historyDeltas(t, q, cursor)
	if changes["slot:inbox"].Resource.Summary.State != "updating" || *changes["transfer:send"].Resource.Transfer.Title != "Older record" {
		t.Fatal("repair/older update lost")
	}
	cursor = historyWatermark(t, q, "alice")
	if err := q.RevokeTransfer("send"); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteSlot("inbox"); err != nil {
		t.Fatal(err)
	}
	changes = historyDeltas(t, q, cursor)
	if changes["transfer:send"].Resource.Transfer.Status != "revoked" || changes["slot:inbox"].Action != "remove" {
		t.Fatal("inactive versus removed not preserved", changes)
	}
}
func TestHistorySyncEmptyScopeValidationAndAccess(t *testing.T) {
	q := historyFixture(t)
	ctx := context.Background()
	cursor := historyWatermark(t, q, "alice")
	if err := q.CreateTransfer("bob-only", time.Now().Add(time.Hour), 0, nil, "bob"); err != nil {
		t.Fatal(err)
	}
	page, err := q.AccountHistoryChanges(ctx, "alice", 50, cursor)
	if err != nil || len(page.Changes) != 0 || page.HasMore || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	again, err := q.AccountHistoryChanges(ctx, "alice", 50, page.NextCursor)
	if err != nil || len(again.Changes) != 0 || again.NextCursor != page.NextCursor {
		t.Fatal(again, err)
	}
	for _, invalid := range []string{"", "malformed", cursor + "=", encodeHistorySyncCursor("bob", page.Generation, 0), encodeHistorySyncCursor("alice", page.Generation, -1)} {
		if _, err = q.AccountHistoryChanges(ctx, "alice", 50, invalid); !errors.Is(err, ErrInvalidPage) {
			t.Fatal("accepted invalid cursor", invalid, err)
		}
	}
	for _, sql := range []string{`UPDATE users SET disabled=1 WHERE id='alice'`, `UPDATE users SET disabled=0,must_change_password=1 WHERE id='alice'`, `UPDATE users SET must_change_password=0,role='admin' WHERE id='alice'`} {
		if _, err = q.db.Exec(sql); err != nil {
			t.Fatal(err)
		}
		if _, err = q.AccountHistoryChanges(ctx, "alice", 50, page.NextCursor); !errors.Is(err, ErrHistoryAccess) {
			t.Fatal("cached cursor bypassed account restrictions", err)
		}
	}
}
func TestHistorySyncAtomicRollbackAndNoPayloadByteEvents(t *testing.T) {
	q := historyFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateTransfer("send", until, 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "send", 100); err != nil {
		t.Fatal(err)
	}
	cursor := historyWatermark(t, q, "alice")
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE transfers SET title='rolled back' WHERE id='send'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = q.UpdateFileOffset("file", 50, false); err != nil {
		t.Fatal(err)
	}
	page, err := q.AccountHistoryChanges(context.Background(), "alice", 100, cursor)
	if err != nil || len(page.Changes) != 0 || page.NextCursor != cursor {
		t.Fatal("rollback or offset emitted history mutation", page, err)
	}
}
func TestHistorySyncCountAndAgeRetention(t *testing.T) {
	q := historyFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateTransfer("send", until, 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	cursor := historyWatermark(t, q, "alice")
	var initialCount int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM history_sync_events`).Scan(&initialCount); err != nil {
		t.Fatal(err)
	}
	// The fixture starts exactly at the production account cap. Only the few
	// mutations that reach/past the boundary need the full metadata write path.
	seeded := 10000 - initialCount
	seedHistoryEvents(t, q, seeded, 1, "send", "upsert")
	assertHistoryFixtureBoundary(t, q, 10000)
	for i := seeded; i < 10010; i++ {
		title := fmt.Sprint(i)
		if err := q.RenameLinkTitle(context.Background(), "transfer", "send", "alice", &title); err != nil {
			t.Fatal(err)
		}
	}
	var count, stateCount, accountCount, accountFloor, globalFloor int
	err := q.db.QueryRow(`SELECT COUNT(*),(SELECT event_count FROM history_sync_state),(SELECT event_count FROM history_sync_accounts WHERE owner_id='alice'),(SELECT floor FROM history_sync_accounts WHERE owner_id='alice'),(SELECT global_floor FROM history_sync_state) FROM history_sync_events`).Scan(&count, &stateCount, &accountCount, &accountFloor, &globalFloor)
	if err != nil {
		t.Fatal(err)
	}
	if count != initialCount+10010-256 || count != stateCount || count != accountCount || accountFloor != 256 || globalFloor != 0 {
		t.Fatal("log count drift/unbounded or wrong prune batch", count, stateCount, accountCount, accountFloor, globalFloor)
	}
	if _, err = q.AccountHistoryChanges(context.Background(), "alice", 100, cursor); !errors.Is(err, ErrHistorySyncReset) {
		t.Fatal("pruned cursor accepted", err)
	}
	cursor = historyWatermark(t, q, "alice")
	if _, err = q.db.Exec(`UPDATE transfers SET title='aged' WHERE id='send'; UPDATE history_sync_events SET created_at=? WHERE revision=(SELECT MAX(revision) FROM history_sync_events)`, time.Now().Add(-8*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = q.AccountHistoryChanges(context.Background(), "alice", 100, cursor); !errors.Is(err, ErrHistorySyncReset) {
		t.Fatal("aged cursor accepted", err)
	}
	if err = q.PruneHistoryChanges(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = q.AccountHistoryChanges(context.Background(), "alice", 100, cursor); !errors.Is(err, ErrHistorySyncReset) {
		t.Fatal("floor lost after physical pruning", err)
	}
}
func TestHistorySyncRestartGenerationAndCanceledReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueries(db)
	if err = q.CreateUser(User{ID: "alice", Username: "alice", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	cursor := historyWatermark(t, q, "alice")
	closeFixture(t, db)
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	q = NewQueries(db)
	if _, err = q.AccountHistoryChanges(context.Background(), "alice", 50, cursor); !errors.Is(err, ErrHistorySyncReset) {
		t.Fatal("restart cursor accepted", err)
	}
	fresh := historyWatermark(t, q, "alice")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = q.AccountHistoryChanges(ctx, "alice", 50, fresh); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled request completed", err)
	}
}

func TestHistorySyncSnapshotHandoffUnderConcurrentMutation(t *testing.T) {
	q := historyFixture(t)
	if err := q.CreateTransfer("send", time.Now().Add(time.Hour), 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	errorsCh := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := q.db.Exec(`UPDATE transfers SET title=? WHERE id='send'`, fmt.Sprint(i)); err != nil {
				errorsCh <- err
				return
			}
		}
		errorsCh <- nil
	}()
	for i := 0; i < 100; i++ {
		snapshot, err := q.AccountHistoryPage(context.Background(), "alice", false, 50, "")
		if err != nil {
			t.Fatal(err)
		}
		cursor, err := decodeHistorySyncCursor(snapshot.SyncCursor, "alice")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range snapshot.Resources {
			if row.Revision > cursor.Revision {
				t.Fatal("snapshot facts exceed watermark")
			}
		}
		feed, err := q.AccountHistoryChanges(context.Background(), "alice", 50, snapshot.SyncCursor)
		if err != nil {
			t.Fatal(err)
		}
		if feed.HasMore && feed.NextCursor == snapshot.SyncCursor {
			t.Fatal("catch-up failed to advance")
		}
	}
	if err := <-errorsCh; err != nil {
		t.Fatal(err)
	}
	// Once the writer stops, a fresh snapshot and empty feed agree exactly.
	cursor := historyWatermark(t, q, "alice")
	feed, err := q.AccountHistoryChanges(context.Background(), "alice", 50, cursor)
	if err != nil || len(feed.Changes) != 0 || feed.NextCursor != cursor {
		t.Fatal(feed, err)
	}
}
func TestHistorySyncGlobalRetentionAndMetadataEstimate(t *testing.T) {
	q := historyFixture(t)
	if err := q.CreateTransfer("global-boundary", time.Now().Add(time.Hour), 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	cursor := historyWatermark(t, q, "alice")
	// An indexed log may fill globally before any individual account reaches its
	// bound. Simulate twenty independent accounts producing a legitimate journal.
	var initialCount int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM history_sync_events`).Scan(&initialCount); err != nil {
		t.Fatal(err)
	}
	seedHistoryEvents(t, q, 100000-initialCount, 20, "retained", "remove")
	assertHistoryFixtureBoundary(t, q, 100000)
	for i := 0; i < 10; i++ {
		title := fmt.Sprint(i)
		if err := q.RenameLinkTitle(context.Background(), "transfer", "global-boundary", "alice", &title); err != nil {
			t.Fatal(err)
		}
	}
	var count, stateCount, accountDrift, largestAccount int
	var floor int64
	if err := q.db.QueryRow(`SELECT COUNT(*),(SELECT event_count FROM history_sync_state),(SELECT global_floor FROM history_sync_state),
 (SELECT COUNT(*) FROM history_sync_accounts a WHERE a.event_count<>(SELECT COUNT(*) FROM history_sync_events e WHERE e.owner_id=a.owner_id)),
 (SELECT MAX(event_count) FROM history_sync_accounts) FROM history_sync_events`).Scan(&count, &stateCount, &floor, &accountDrift, &largestAccount); err != nil {
		t.Fatal(err)
	}
	if count != 100010-256 || count != stateCount || floor != 256 || accountDrift != 0 || largestAccount > 10000 {
		t.Fatal("global cap/floor lost or account counts drifted", count, stateCount, floor, accountDrift, largestAccount)
	}
	if _, err := q.AccountHistoryChanges(context.Background(), "alice", 50, cursor); !errors.Is(err, ErrHistorySyncReset) {
		t.Fatal("global pruning did not reset cursor", err)
	}
	usage, err := q.ResourceUsage("")
	if err != nil || usage.HistoryMetadataBytes < int64(count)*512 {
		t.Fatal("journal omitted from storage estimate", usage, err)
	}
}
func TestHistorySyncSessionRevocationInsideReadSnapshot(t *testing.T) {
	q := historyFixture(t)
	cursor := historyWatermark(t, q, "alice")
	// Even after HTTP authentication observed a session, the database read must
	// reject its now-retired identity rather than return a cached empty response.
	if _, err := q.AccountHistoryChanges(context.Background(), "alice", 50, cursor, "retired-session"); !errors.Is(err, ErrHistoryAccess) {
		t.Fatal("retired session accepted", err)
	}
}

func TestHistorySyncRowAnchorsSurviveDeletionAndMatchFilters(t *testing.T) {
	q := historyFixture(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("send-%02d", i)
		if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.CreateSlot("slot", time.Now().Add(time.Hour), nil, "alice"); err != nil {
		t.Fatal(err)
	}
	// Stored SQLite timestamps need not use JSON's ISO8601 representation.
	if _, err := q.db.Exec(`UPDATE transfers SET created_at='2020-01-01 00:00:00'; UPDATE slots SET created_at='2020-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	first, err := q.AccountHistoryPage(ctx, "alice", false, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	last := first.Resources[len(first.Resources)-1]
	if last.HistoryAfter == "" || last.HistoryAfterKind == "" || last.HistoryAfter != *first.NextCursor {
		t.Fatal("incorrect all-history anchor", last)
	}
	filter := last.Transfer != nil
	kind := "slot"
	id := "slot"
	if filter {
		kind = "transfer"
		id = last.Transfer.ID
	}
	expected, err := q.AccountHistoryPage(ctx, "alice", false, 50, last.HistoryAfterKind, kind)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`DELETE FROM `+kind+`s WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	after, err := q.AccountHistoryPage(ctx, "alice", false, 50, last.HistoryAfterKind, kind)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Resources) != len(expected.Resources) {
		t.Fatal("deleted anchor broke seek")
	}
	for i, row := range after.Resources {
		if row.Transfer.ID != expected.Resources[i].Transfer.ID {
			t.Fatal("seek changed ordering")
		}
	}
	if _, err := q.AccountHistoryPage(ctx, "alice", false, 50, last.HistoryAfterKind); !errors.Is(err, ErrInvalidPage) {
		t.Fatal("kind anchor accepted for wrong filter", err)
	}
}

func TestHistorySyncFailsClosedWhenJournalStateIsMissing(t *testing.T) {
	q := historyFixture(t)
	if err := q.CreateTransfer("send", time.Now().Add(time.Hour), 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`DELETE FROM history_sync_state WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE transfers SET title='must not commit' WHERE id='send'`); err == nil {
		t.Fatal("metadata write succeeded without required journal")
	}
	row, err := q.GetTransfer("send")
	if err != nil {
		t.Fatal(err)
	}
	if row.Title != nil {
		t.Fatal("failed mutation was not rolled back")
	}
	if _, err = q.db.Exec(`UPDATE transfers SET owner_id=NULL WHERE id='send'`); err == nil {
		t.Fatal("ownership removal succeeded without required journal")
	}
}
func TestHistorySyncSnapshotRechecksRetiredSession(t *testing.T) {
	q := historyFixture(t)
	if _, err := q.AccountHistoryPageForSession(context.Background(), "alice", "retired", false, 50, ""); !errors.Is(err, ErrHistoryAccess) {
		t.Fatal("retired session snapshot accepted", err)
	}
}
func TestHistorySyncOwnershipRemovalAndReplay(t *testing.T) {
	q := historyFixture(t)
	ctx := context.Background()
	if err := q.CreateTransfer("send", time.Now().Add(time.Hour), 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	alice := historyWatermark(t, q, "alice")
	bob := historyWatermark(t, q, "bob")
	if _, err := q.db.Exec(`UPDATE transfers SET owner_id='bob' WHERE id='send'`); err != nil {
		t.Fatal(err)
	}
	a, err := q.AccountHistoryChanges(ctx, "alice", 50, alice)
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.AccountHistoryChanges(ctx, "bob", 50, bob)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Changes) != 1 || a.Changes[0].Action != "remove" || a.Changes[0].Resource != nil {
		t.Fatal("old ownership leaked resource", a)
	}
	if len(b.Changes) != 1 || b.Changes[0].Action != "upsert" || b.Changes[0].Resource.OwnerID != "bob" {
		t.Fatal("new owner missing update", b)
	}
	replay, err := q.AccountHistoryChanges(ctx, "alice", 50, alice)
	if err != nil || len(replay.Changes) != 1 || replay.NextCursor != a.NextCursor || replay.Changes[0].Revision != a.Changes[0].Revision {
		t.Fatal("delivery not idempotent", replay, err)
	}
}

func TestHistorySyncAccountShutdownIsAtomicAndRetained(t *testing.T) {
	q := historyFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateTransfer("send", until, 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlot("slot", until, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	cursor := historyWatermark(t, q, "alice")
	result, err := q.ShutdownAccount("alice")
	if err != nil {
		t.Fatal(err)
	}
	if result.RevokedTransfers != 1 || result.RevokedSlots != 1 {
		t.Fatal(result)
	}
	if _, err = q.AccountHistoryChanges(context.Background(), "alice", 50, cursor); !errors.Is(err, ErrHistoryAccess) {
		t.Fatal("disabled account read journal", err)
	}
	if _, err = q.db.Exec(`UPDATE users SET disabled=0 WHERE id='alice'`); err != nil {
		t.Fatal(err)
	}
	changes := historyDeltas(t, q, cursor)
	if changes["transfer:send"].Resource.Transfer.Status != "revoked" || changes["slot:slot"].Resource.Slot.Status != "revoked" {
		t.Fatal("shutdown mutation omitted", changes)
	}
}

func TestHistorySyncPrivateChildrenNeverPublishIdentities(t *testing.T) {
	q := historyFixture(t)
	ctx := context.Background()
	until := time.Now().Add(time.Hour)
	if err := q.CreateReceiveSlot("inbox", until, nil, "alice", 2, "key", 0); err != nil {
		t.Fatal(err)
	}
	cursor := historyWatermark(t, q, "alice")
	if err := q.CreateSlotTransfer("inbox", "private-child", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "private-child", 16); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("file", 16, true); err != nil {
		t.Fatal(err)
	}
	if err := q.CompleteTransfer("private-child"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM history_sync_events WHERE kind='transfer' AND resource_id='private-child'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("private identity was journaled", count)
	}
	changes := historyDeltas(t, q, cursor)
	if _, ok := changes["transfer:private-child"]; ok {
		t.Fatal("private identity was returned", changes)
	}
	if changes["slot:inbox"].Resource == nil || *changes["slot:inbox"].Resource.Summary.CompletedFiles != 1 {
		t.Fatal("parent summary lost", changes)
	}
	// An unlink/delete of the parent must not transform the child into public
	// history while its retained files/metadata still exist.
	if err := q.DeleteSlot("inbox"); err != nil {
		t.Fatal(err)
	}
	page, err := q.AccountHistoryPage(ctx, "alice", false, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Resources {
		if row.Transfer != nil && row.Transfer.ID == "private-child" {
			t.Fatal("orphaned private child became public history")
		}
	}
	if err = q.DeleteTransfer("private-child"); err != nil {
		t.Fatal(err)
	}
	if err = q.db.QueryRow(`SELECT COUNT(*) FROM history_sync_private_transfers`).Scan(&count); err != nil || count != 0 {
		t.Fatal("private marker was not bounded by resource lifetime", count, err)
	}
}
func TestHistorySyncPublicAttachmentStillRemovesPublishedIdentity(t *testing.T) {
	q := historyFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateTransfer("published", until, 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlot("inbox", until, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	cursor := historyWatermark(t, q, "alice")
	if err := q.LinkSlotTransfer("inbox", "published"); err != nil {
		t.Fatal(err)
	}
	changes := historyDeltas(t, q, cursor)
	if changes["transfer:published"].Action != "remove" || changes["transfer:published"].Resource != nil {
		t.Fatal("published attachment removal lost", changes)
	}
}
