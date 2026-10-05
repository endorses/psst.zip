package database

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAdminResourceMissingSummaryKeepsRecoveryMetadataAndEvents(t *testing.T) {
	q, _ := resourceFixture(t)
	if err := q.CreateUser(User{ID: "owner", Username: "Owner", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if err := q.CreateSlot("inbox", until, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("child", until, 0, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer("inbox", "child"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("file", "child", 100); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("file", 10, false); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("child", []byte("opaque")); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"transfer", "slot"} {
		id := "child"
		if kind == "slot" {
			id = "inbox"
		}
		item, err := q.AdminResource(kind, id)
		if err != nil || !item.TotalsAvailable || item.FileCount != 1 || item.ReservedBytes != 106 {
			t.Fatalf("existing summary not available: %+v %v", item, err)
		}
	}
	if _, err := q.db.Exec(`DELETE FROM admin_resource_totals WHERE resource_id IN ('child','inbox')`); err != nil {
		t.Fatal(err)
	}
	// Recovery actions remain possible even before counter reconstruction.
	if err := q.RevokeTransfer("child"); err != nil {
		t.Fatal(err)
	}
	if err := q.RevokeSlotQueued("inbox", SecurityEvent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE users SET disabled=1 WHERE id='owner'`); err != nil {
		t.Fatal(err)
	}
	for _, event := range []SecurityEvent{
		{Kind: "transfer.revoked", TargetType: "transfer", TargetID: "child"},
		{Kind: "slot.revoked", TargetType: "slot", TargetID: "inbox"},
		{Kind: "account.shutdown", TargetType: "user", TargetID: "owner"},
	} {
		event.Origin, event.Outcome = "administrator", "succeeded"
		if err := q.RecordSecurityEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"transfer", "slot"} {
		id := "child"
		if kind == "slot" {
			id = "inbox"
		}
		item, err := q.AdminResource(kind, id)
		if err != nil || item.ID != id || item.Status != "revoked" || item.OwnerID == nil || *item.OwnerID != "owner" || item.OwnerUsername == nil || *item.OwnerUsername != "Owner" || !item.OwnerDisabled || item.Cleanup.State != "pending" {
			t.Fatalf("missing summary hid canonical recovery metadata: %+v %v", item, err)
		}
		if item.TotalsAvailable || item.FileCount != 0 || item.ChildTransferCount != 0 || item.ReservedBytes != 0 || item.OccupiedBytesEstimate != 0 || item.ManifestBytes != 0 {
			t.Fatalf("missing summary presented as complete: %+v", item)
		}
		if kind == "transfer" && (item.ParentSlotID == nil || *item.ParentSlotID != "inbox") {
			t.Fatalf("missing summary hid parent: %+v", item)
		}
		encoded, err := json.Marshal(item)
		if err != nil || !strings.Contains(string(encoded), `"totals_available":false`) {
			t.Fatal("missing explicit JSON availability", string(encoded), err)
		}
	}
	page, err := q.AdminResourceEvents("transfer", "child", 0, 100, time.Now())
	if err != nil || len(page.Events) != 3 {
		t.Fatalf("missing summary hid direct/parent/owner audit context: %+v %v", page, err)
	}
	for _, filter := range []AdminResourceFilter{{}, {OwnerID: "owner", Status: "revoked"}} {
		first, err := q.AdminResources(filter, 1, "")
		if err != nil || len(first.Resources) != 1 || first.NextCursor == nil || first.Resources[0].TotalsAvailable {
			t.Fatalf("missing summary broke bounded list: %+v %v", first, err)
		}
		second, err := q.AdminResources(filter, 1, *first.NextCursor)
		if err != nil || len(second.Resources) != 1 || second.NextCursor != nil || second.Resources[0].TotalsAvailable || first.Resources[0].Type == second.Resources[0].Type {
			t.Fatalf("missing summary broke list continuation: %+v %v", second, err)
		}
	}
}

func TestAdminResourceOptionalSummaryUsesIndexedBoundedLookups(t *testing.T) {
	q, _ := resourceFixture(t)
	for _, kind := range []string{"transfer", "slot"} {
		for _, suffix := range []string{"history", "owner_history", "status_history", "owner_status_history"} {
			index := "admin_" + kind + "s_" + suffix
			query := adminResourceSelect(kind, index) + ` WHERE (r.created_at,r.id)<(?,?)`
			args := []any{"2026-10-04 01:00:00", "cursor"}
			if strings.Contains(suffix, "owner") {
				query += ` AND r.owner_id=?`
				args = append(args, "owner")
			}
			if strings.Contains(suffix, "status") {
				query += ` AND r.status=?`
				args = append(args, "pending")
			}
			query += ` ORDER BY r.created_at DESC,r.id DESC LIMIT 101`
			rows, err := q.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
			if err != nil {
				t.Fatal(err)
			}
			ordered, summary := false, false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					closeFixture(t, rows)
					t.Fatal(err)
				}
				if strings.Contains(detail, "USE TEMP B-TREE") || strings.HasPrefix(detail, "SCAN ") {
					closeFixture(t, rows)
					t.Fatalf("unbounded optional-summary query: %s", detail)
				}
				ordered = ordered || strings.Contains(detail, "SEARCH r USING INDEX "+index)
				summary = summary || strings.Contains(detail, "SEARCH c USING INDEX sqlite_autoindex_admin_resource_totals_1") && strings.Contains(detail, "LEFT-JOIN")
			}
			err = rows.Err()
			closeFixture(t, rows)
			if err != nil || !ordered || !summary {
				t.Fatalf("missing bounded canonical/summary seek: %s ordered=%v summary=%v err=%v", index, ordered, summary, err)
			}
		}
	}
}
