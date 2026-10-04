package database

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func historyFixture(t *testing.T) *Queries {
	t.Helper()
	q, _ := resourceFixture(t)
	for _, id := range []string{"alice", "bob", "admin"} {
		role := "user"
		if id == "admin" {
			role = "admin"
		}
		if err := q.CreateUser(User{ID: id, Username: id, Role: role, PasswordHash: []byte("hash")}, false); err != nil {
			t.Fatal(err)
		}
	}
	return q
}
func TestHistoryPageBoundsRawChildrenAndAdvancesFilteredPages(t *testing.T) {
	q := historyFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateSlot("inbox", until, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("send", until, 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 110; i++ {
		id := fmt.Sprintf("child-%03d", i)
		if err := q.CreateTransfer(id, until, 0, nil, "alice"); err != nil {
			t.Fatal(err)
		}
		if err := q.LinkSlotTransfer("inbox", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.db.Exec(`UPDATE slots SET created_at='2020-01-01'; UPDATE transfers SET created_at='2020-01-01' WHERE id='send'; UPDATE transfers SET created_at='2021-01-01' WHERE id!='send'`); err != nil {
		t.Fatal(err)
	}
	policy, err := q.ResourcePolicy()
	if err != nil {
		t.Fatal(err)
	}
	policy.AccountTransfers = 1
	if err = q.SetResourcePolicy(policy); err != nil {
		t.Fatal(err)
	}
	after := ""
	empty := 0
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		page, e := q.AccountHistoryPage(context.Background(), "alice", false, 10, after)
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Resources) > 10 {
			t.Fatal("unbounded response")
		}
		if len(page.Resources) == 0 {
			empty++
			if page.NextCursor == nil {
				t.Fatal("filtered prefix hid later resources")
			}
		}
		for _, item := range page.Resources {
			id := ""
			if item.Transfer != nil {
				id = item.Transfer.ID
			} else {
				id = item.Slot.ID
			}
			if seen[id] {
				t.Fatal("duplicate", id)
			}
			seen[id] = true
		}
		if page.NextCursor == nil {
			break
		}
		if len(*page.NextCursor) > 512 {
			t.Fatal("unbounded cursor")
		}
		after = *page.NextCursor
	}
	if empty != 11 || len(seen) != 2 || !seen["inbox"] || !seen["send"] {
		t.Fatal(empty, seen)
	}
}
func TestHistoryPageMergeTieBreakAndScopedCursors(t *testing.T) {
	q := historyFixture(t)
	until := time.Now().Add(time.Hour)
	for _, owner := range []string{"alice", "bob"} {
		for _, id := range []string{"same", "next"} {
			id = owner + "-" + id
			if err := q.CreateTransfer(id, until, 0, nil, owner); err != nil {
				t.Fatal(err)
			}
			if err := q.CreateSlot(id, until, nil, owner); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := q.db.Exec(`UPDATE transfers SET created_at='2020-01-01 01:02:03'; UPDATE slots SET created_at='2020-01-01 01:02:03'`); err != nil {
		t.Fatal(err)
	}
	first, err := q.AccountHistoryPage(context.Background(), "alice", false, 1, "")
	if err != nil || first.NextCursor == nil {
		t.Fatal(first, err)
	}
	if first.Resources[0].Transfer == nil || first.Resources[0].Transfer.ID != "alice-same" {
		t.Fatal("wrong tied first row", first)
	}
	for _, test := range []struct {
		actor string
		all   bool
	}{{"bob", false}, {"admin", true}} {
		if _, err = q.AccountHistoryPage(context.Background(), test.actor, test.all, 1, *first.NextCursor); !errors.Is(err, ErrInvalidPage) {
			t.Fatal("cross-scope cursor accepted", test, err)
		}
	}
	seen := map[string]bool{}
	after := ""
	for i := 0; i < 5; i++ {
		page, e := q.AccountHistoryPage(context.Background(), "alice", false, 1, after)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range page.Resources {
			key := "slot:"
			id := ""
			if item.Transfer != nil {
				key = "transfer:"
				id = item.Transfer.ID
			} else {
				id = item.Slot.ID
			}
			if item.OwnerID != "alice" || seen[key+id] {
				t.Fatal("scope or duplicate failure", item)
			}
			seen[key+id] = true
		}
		if page.NextCursor == nil {
			break
		}
		after = *page.NextCursor
	}
	if len(seen) != 4 {
		t.Fatal("tie cursor lost row", seen)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(*first.NextCursor)
	var cursor map[string]any
	if err = json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	cursor["extra"] = true
	extra, _ := json.Marshal(cursor)
	for _, bad := range []string{*first.NextCursor + "=", strings.Repeat("a", 513), "!", base64.RawURLEncoding.EncodeToString(extra), base64.RawURLEncoding.EncodeToString(append(raw, ' '))} {
		if _, err = q.AccountHistoryPage(context.Background(), "alice", false, 1, bad); !errors.Is(err, ErrInvalidPage) {
			t.Fatal("bad cursor accepted", bad, err)
		}
	}
}
func TestHistorySummaryReadsAreIndependentOfCanonicalPayloadScans(t *testing.T) {
	q := historyFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateSlot("inbox", until, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("send", until, 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "send", 60); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("file", 60, true); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE transfers SET status='complete' WHERE id='send'`); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("send", []byte("opaque")); err != nil {
		t.Fatal(err)
	}
	page, err := q.AccountHistoryPage(context.Background(), "alice", false, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Resources {
		if item.Transfer != nil {
			if item.Summary.State != "ready" || *item.Summary.FileCount != 1 || *item.Summary.CompletedFiles != 1 || *item.Summary.TotalSize != 60 || !item.HasManifest {
				t.Fatal(item)
			}
		}
	}
	if _, err = q.db.Exec(`DROP TABLE files`); err != nil {
		t.Fatal(err)
	}
	if _, err = q.AccountHistoryPage(context.Background(), "alice", false, 50, ""); err != nil {
		t.Fatal("history scanned payload table", err)
	}
	if _, err = q.db.Exec(`DELETE FROM admin_resource_totals WHERE kind='transfer'; UPDATE admin_resource_totals SET inbox_known=0 WHERE kind='slot'`); err != nil {
		t.Fatal(err)
	}
	page, err = q.AccountHistoryPage(context.Background(), "alice", false, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Resources {
		if item.Summary.State != "updating" || item.Summary.FileCount != nil || item.Summary.CompletedFiles != nil || item.Summary.TotalSize != nil {
			t.Fatal("invented unknown counters", item)
		}
	}
	if _, err = q.db.Exec(`DROP TABLE admin_resource_totals`); err != nil {
		t.Fatal(err)
	}
	if _, err = q.AccountHistoryPage(context.Background(), "alice", false, 50, ""); err == nil {
		t.Fatal("unavailable counter query succeeded")
	}
}
func TestHistoryPageCancellationAndSnapshotAuthorization(t *testing.T) {
	q := historyFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.AccountHistoryPage(ctx, "alice", false, 50, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, test := range []struct {
		actor string
		all   bool
	}{{"alice", true}, {"admin", false}, {"missing", false}} {
		if _, err := q.AccountHistoryPage(context.Background(), test.actor, test.all, 50, ""); !errors.Is(err, ErrHistoryAccess) {
			t.Fatal(test, err)
		}
	}
	if _, err := q.db.Exec(`UPDATE users SET disabled=1 WHERE id='alice'`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AccountHistoryPage(context.Background(), "alice", false, 50, ""); !errors.Is(err, ErrHistoryAccess) {
		t.Fatal("disabled owner read history", err)
	}
}
func TestHistoryCandidateQueriesUseBoundedIndexedSeeks(t *testing.T) {
	q := historyFixture(t)
	for _, kind := range []string{"transfer", "slot"} {
		for _, owner := range []string{"", "alice"} {
			cursor := &historyCursor{Created: "2026-01-01", ID: "child", Kind: "transfer"}
			query, args := historyCandidatesQuery(kind, owner, cursor, 50)
			if strings.Contains(query, "slot_transfers") || args[len(args)-1] != 51 {
				t.Fatal("raw candidate query filters before limit", query, args)
			}
			rows, err := q.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
			if err != nil {
				t.Fatal(err)
			}
			plan := ""
			for rows.Next() {
				var a, b, c int
				var detail string
				if err = rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				plan += detail
			}
			rows.Close()
			expected := "admin_" + kind + "s_history"
			if owner != "" {
				expected = "admin_" + kind + "s_owner_history"
			}
			if !strings.Contains(plan, "SEARCH "+kind+"s USING COVERING INDEX "+expected) || strings.Contains(plan, "TEMP B-TREE") {
				t.Fatal("history seek scanned or sorted source", plan)
			}
		}
	}
}
