package api_test

import (
	"context"
	"testing"
)

func TestAdminResourceRecoveryRemainsAvailableWithoutSummaries(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)["id"].(string)
	if err := env.queries.CreateFile("recovery-file", child, 100); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.UpdateFileOffset("recovery-file", 20, false); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.SaveManifest(child, []byte("opaque")); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Exec(`DELETE FROM admin_resource_totals WHERE resource_id IN (?,?)`, child, slot); err != nil {
		t.Fatal(err)
	}
	checkIdentity := func(item map[string]any) {
		t.Helper()
		if item["owner_id"] != "fixture-user" || item["owner_username"] != "fixture" {
			t.Fatalf("missing summaries hid real ownership: %+v", item)
		}
		switch item["type"] {
		case "transfer":
			if item["id"] != child || item["parent_slot_id"] != slot {
				t.Fatalf("missing summaries hid child identity: %+v", item)
			}
		case "slot":
			if item["id"] != slot || item["parent_slot_id"] != nil {
				t.Fatalf("missing summaries hid inbox identity: %+v", item)
			}
		default:
			t.Fatalf("unexpected recovery resource: %+v", item)
		}
	}
	checkMissing := func(item map[string]any) {
		t.Helper()
		checkIdentity(item)
		if item["totals_available"] != false {
			t.Fatalf("missing totals presented as complete: %+v", item)
		}
		for _, field := range []string{"file_count", "child_transfer_count", "reserved_bytes", "occupied_bytes_estimate", "manifest_bytes"} {
			if item[field] != float64(0) {
				t.Fatalf("missing total %s was not explicit transport default: %+v", field, item)
			}
		}
	}
	page := authRequest(t, env, "GET", "/admin/resources?limit=1", env.authToken, nil, 200)
	items := page["resources"].([]any)
	if len(items) != 1 || page["next_cursor"] == nil {
		t.Fatal("missing summaries broke first recovery page", page)
	}
	checkMissing(items[0].(map[string]any))
	firstType := items[0].(map[string]any)["type"]
	page = authRequest(t, env, "GET", "/admin/resources?limit=1&after="+page["next_cursor"].(string), env.authToken, nil, 200)
	items = page["resources"].([]any)
	if len(items) != 1 || page["next_cursor"] != nil || items[0].(map[string]any)["type"] == firstType {
		t.Fatal("missing summaries broke recovery continuation", page)
	}
	checkMissing(items[0].(map[string]any))
	for _, resource := range []struct{ kind, id string }{{"transfer", child}, {"slot", slot}} {
		endpoint := "/admin/resources/" + resource.kind + "/" + resource.id
		checkMissing(authRequest(t, env, "GET", endpoint, env.authToken, nil, 200))
		for _, action := range []string{"revoke", "cleanup"} {
			result := authRequest(t, env, "POST", endpoint+"/"+action, env.authToken, map[string]any{}, 202)
			if result["state"] != "pending" || result["id"] != resource.id {
				t.Fatalf("missing summaries blocked %s: %+v", action, result)
			}
		}
		item := authRequest(t, env, "GET", endpoint, env.authToken, nil, 200)
		checkMissing(item)
		if item["status"] != "revoked" || item["cleanup"].(map[string]any)["state"] != "pending" {
			t.Fatal("recovery actions lost lifecycle state", item)
		}
	}
	completed := false
	for range 64 {
		if err := env.queries.RebuildCounterBatch(context.Background(), 64); err != nil {
			t.Fatal(err)
		}
		status, err := env.queries.CounterRebuildStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !status.ScanPending {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatal("bounded counter reconstruction did not complete")
	}
	for _, resource := range []struct{ kind, id string }{{"transfer", child}, {"slot", slot}} {
		item := authRequest(t, env, "GET", "/admin/resources/"+resource.kind+"/"+resource.id, env.authToken, nil, 200)
		checkIdentity(item)
		if item["totals_available"] != true || item["file_count"] != float64(1) || item["reserved_bytes"] != float64(106) || item["occupied_bytes_estimate"] != float64(26) || item["manifest_bytes"] != float64(6) {
			t.Fatalf("reconstructed totals unavailable or incorrect: %+v", item)
		}
		children := float64(0)
		if resource.kind == "slot" {
			children = 1
		}
		if item["child_transfer_count"] != children || item["status"] != "revoked" || item["cleanup"].(map[string]any)["state"] != "pending" {
			t.Fatalf("reconstruction changed lifecycle/child count: %+v", item)
		}
	}
}
