package database

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func assertResourceTotals(t *testing.T, q *Queries, kind, id string, files, children, reserved, occupied, manifest int64) {
	t.Helper()
	r, err := q.AdminResource(kind, id)
	if err != nil {
		t.Fatal(err)
	}
	if r.FileCount != files || r.ChildTransferCount != children || r.ReservedBytes != reserved || r.OccupiedBytesEstimate != occupied || r.ManifestBytes != manifest {
		t.Fatalf("%s/%s totals %+v want %d/%d/%d/%d/%d", kind, id, r, files, children, reserved, occupied, manifest)
	}
}
func adminResourceFixture(t *testing.T) (*Queries, string) {
	t.Helper()
	q, path := resourceFixture(t)
	until := time.Now().Add(time.Hour)
	for _, id := range []string{"one", "two"} {
		if err := q.CreateSlot(id, until, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b"} {
		if err := q.CreateTransfer(id, until, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	return q, path
}
func TestAdminResourceTotalsTrackMutationMembershipAndCascades(t *testing.T) {
	q, _ := adminResourceFixture(t)
	if err := q.CreateFile("f", "a", 100); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("f", 40, false); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("a", []byte("opaque")); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer("one", "a"); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "transfer", "a", 1, 0, 106, 46, 6)
	assertResourceTotals(t, q, "slot", "one", 1, 1, 106, 46, 6)
	if err := q.SaveManifest("a", []byte("abc")); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "slot", "one", 1, 1, 103, 43, 3)
	if _, err := q.db.Exec(`UPDATE slot_transfers SET slot_id='two' WHERE slot_id='one' AND transfer_id='a'`); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "slot", "one", 0, 0, 0, 0, 0)
	assertResourceTotals(t, q, "slot", "two", 1, 1, 103, 43, 3)
	if err := q.ReleasePayloads("a"); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "slot", "two", 1, 1, 3, 3, 3)
	if _, err := q.db.Exec(`DELETE FROM slot_transfers WHERE slot_id='two'`); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "slot", "two", 0, 0, 0, 0, 0)
	if err := q.LinkSlotTransfer("two", "a"); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteTransfer("a"); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "slot", "two", 0, 0, 0, 0, 0)
	if err := q.CreateFile("other", "b", 80); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer("one", "b"); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteSlot("one"); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "transfer", "b", 1, 0, 80, 0, 0)
	var leftover int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM admin_resource_totals WHERE (kind='slot' AND resource_id='one') OR (kind='transfer' AND resource_id='a')`).Scan(&leftover); err != nil || leftover != 0 {
		t.Fatal(leftover, err)
	}
}
func TestAdminResourceTotalsAcrossConnectionRestartAndBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for version, migration := range migrations {
		if strings.Contains(migration, "CREATE TABLE admin_resource_totals") {
			break
		}
		if _, err = old.Exec(migration); err != nil {
			t.Fatal(err)
		}
		if version > 0 {
			if _, err = old.Exec(`INSERT INTO schema_migrations(version)VALUES(?)`, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := NewQueries(old)
	if err = q.CreateUser(User{ID: "owner", Username: "owner", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if err = legacyCreateSlot(old, "slot", until, "owner"); err != nil {
		t.Fatal(err)
	}
	if err = legacyCreateTransfer(old, "child", until); err != nil {
		t.Fatal(err)
	}
	if err = q.CreateFile("file", "child", 45); err != nil {
		t.Fatal(err)
	}
	if err = q.UpdateFileOffset("file", 23, false); err != nil {
		t.Fatal(err)
	}
	if err = q.SaveManifest("child", []byte("opaque")); err != nil {
		t.Fatal(err)
	}
	if err = q.LinkSlotTransfer("slot", "child"); err != nil {
		t.Fatal(err)
	}
	closeFixture(t, old)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	q = NewQueries(db)
	assertResourceTotals(t, q, "slot", "slot", 1, 1, 51, 29, 6)
	resource, err := q.AdminResource("transfer", "child")
	if err != nil || resource.OwnerID == nil || *resource.OwnerID != "owner" {
		t.Fatal(resource, err)
	}
	page, err := q.AdminResources(AdminResourceFilter{OwnerID: "owner"}, 50, "")
	if err != nil || len(page.Resources) != 2 {
		t.Fatal(page, err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q2 := NewQueries(other)
	if err = q2.UpdateFileOffset("file", 45, true); err != nil {
		t.Fatal(err)
	}
	if err = q2.SaveManifest("child", []byte("xx")); err != nil {
		t.Fatal(err)
	}
	closeFixture(t, other)
	assertResourceTotals(t, q, "slot", "slot", 1, 1, 47, 47, 2)
}

func TestAdminResourceTotalsMoveFilesAndSharedParentCascades(t *testing.T) {
	q, _ := adminResourceFixture(t)
	for _, slot := range []string{"one", "two"} {
		if err := q.LinkSlotTransfer(slot, "a"); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.LinkSlotTransfer("two", "b"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("moving", "a", 70); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("moving", 30, false); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE files SET transfer_id='b',size=80,upload_offset=40 WHERE id='moving'`); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "slot", "one", 0, 1, 0, 0, 0)
	assertResourceTotals(t, q, "slot", "two", 1, 2, 80, 40, 0)
	if err := q.DeleteTransfer("a"); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "slot", "one", 0, 0, 0, 0, 0)
	assertResourceTotals(t, q, "slot", "two", 1, 1, 80, 40, 0)
	if _, err := q.db.Exec(`DELETE FROM files WHERE id='moving'`); err != nil {
		t.Fatal(err)
	}
	assertResourceTotals(t, q, "slot", "two", 0, 1, 0, 0, 0)
}
func TestAdminResourcesBoundedStablePaginationAndFilters(t *testing.T) {
	q, _ := resourceFixture(t)
	until := time.Now().Add(time.Hour)
	for _, owner := range []string{"alice", "bob"} {
		if err := q.CreateUser(User{ID: owner, Username: owner, Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("id-%02d", i)
		owner := "alice"
		if i%2 == 0 {
			owner = "bob"
		}
		if err := q.CreateTransfer(id, until, 0, nil, owner); err != nil {
			t.Fatal(err)
		}
		if err := q.CreateSlot(id, until, nil, owner); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.db.Exec(`UPDATE transfers SET created_at='2026-10-04 01:00:00';UPDATE slots SET created_at='2026-10-04 01:00:00'`); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	after := ""
	for {
		p, err := q.AdminResources(AdminResourceFilter{}, 7, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Resources) > 7 {
			t.Fatal("unbounded page")
		}
		for _, r := range p.Resources {
			key := r.Type + ":" + r.ID
			if seen[key] {
				t.Fatal("duplicate", key)
			}
			seen[key] = true
		}
		if p.NextCursor == nil {
			break
		}
		after = *p.NextCursor
	}
	if len(seen) != 50 {
		t.Fatal(len(seen))
	}
	p, err := q.AdminResources(AdminResourceFilter{Type: "transfer", OwnerID: "alice", Status: "pending"}, 3, "")
	if err != nil || len(p.Resources) != 3 || p.NextCursor == nil {
		t.Fatal(p, err)
	}
	if _, err = q.AdminResources(AdminResourceFilter{Type: "slot", OwnerID: "alice", Status: "waiting"}, 3, *p.NextCursor); err == nil {
		t.Fatal("cross-filter cursor accepted")
	}
	for _, bad := range []string{"%%%", base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"secret":"not allowed"}`))} {
		if _, err = q.AdminResources(AdminResourceFilter{}, 3, bad); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
	for _, f := range []AdminResourceFilter{{Type: "file"}, {OwnerID: "https://host/#secret"}, {Status: "anything"}} {
		if _, err = q.AdminResources(f, 3, ""); err == nil {
			t.Fatal("invalid filter accepted")
		}
	}
}
func TestAdminResourceEventsIncludeDenialsAndSurviveDeletion(t *testing.T) {
	q, _ := resourceFixture(t)
	if err := q.CreateUser(User{ID: "owner", Username: "Owner", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if err := q.CreateSlot("inbox", until, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("child", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer("inbox", "child"); err != nil {
		t.Fatal(err)
	}
	for _, e := range []SecurityEvent{{Kind: "account.shutdown", TargetType: "user", TargetID: "owner"}, {Kind: "slot.revoked", TargetType: "slot", TargetID: "inbox"}, {Kind: "transfer.revoked", TargetType: "transfer", TargetID: "child"}, {Kind: "account.password_changed", TargetType: "user", TargetID: "owner"}, {Kind: "transfer.revoked", TargetType: "transfer", TargetID: "unrelated"}} {
		e.Origin = "administrator"
		e.Outcome = "succeeded"
		if err := q.RecordSecurityEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	p, err := q.AdminResourceEvents("transfer", "child", 0, 2, time.Now())
	if err != nil || len(p.Events) != 2 || p.NextBefore == nil {
		t.Fatal(p, err)
	}
	next, err := q.AdminResourceEvents("transfer", "child", *p.NextBefore, 2, time.Now())
	if err != nil || len(next.Events) != 1 || next.Events[0].Kind != "account.shutdown" {
		t.Fatal(next, err)
	}
	if err = q.DeleteTransfer("child"); err != nil {
		t.Fatal(err)
	}
	p, err = q.AdminResourceEvents("transfer", "child", 0, 100, time.Now())
	if err != nil || len(p.Events) != 1 || p.Events[0].TargetID != "child" {
		t.Fatal(p, err)
	}
	data, _ := json.Marshal(p)
	if strings.Contains(string(data), "password_changed") {
		t.Fatal(string(data))
	}
}
func TestAdminResourceQueriesUseOrderedIndexes(t *testing.T) {
	q, _ := resourceFixture(t)
	for _, kind := range []string{"transfer", "slot"} {
		for _, suffix := range []string{"history", "owner_history", "status_history", "owner_status_history"} {
			index := "admin_" + kind + "s_" + suffix
			query := adminResourceSelect(kind, index) + ` WHERE 1=1`
			args := []any{}
			if strings.Contains(suffix, "owner") {
				query += ` AND r.owner_id=?`
				args = append(args, "owner")
			}
			if strings.Contains(suffix, "status") {
				query += ` AND r.status=?`
				args = append(args, "pending")
			}
			query += ` AND (r.created_at,r.id)<(?,?) ORDER BY r.created_at DESC,r.id DESC LIMIT 101`
			args = append(args, "2026-10-04 01:00:00", "cursor")
			rows, err := q.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for rows.Next() {
				var a, b, c int
				var detail string
				if err = rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(detail, "USE TEMP B-TREE") {
					t.Fatalf("unbounded sort %s", detail)
				}
				if strings.Contains(detail, index) && !strings.Contains(detail, "SEARCH r") {
					t.Fatalf("cursor does not seek: %s", detail)
				}
				found = found || strings.Contains(detail, index)
			}
			closeFixture(t, rows)
			if !found {
				t.Fatal("missing ordered index", index)
			}
		}
	}
}
