package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/cleanup"
)

func TestResourcePolicyAdminAuthorizationAndAccountUsage(t *testing.T) {
	env := setupAuthFixture(t, false)
	authRequest(t, env, "GET", "/admin/resource-policy", "", nil, 401)
	authRequest(t, env, "GET", "/admin/resource-policy", env.userToken, nil, 403)
	authRequest(t, env, "PATCH", "/admin/resource-policy", env.userToken, map[string]int{"server_files": 2}, 403)
	got := authRequest(t, env, "GET", "/admin/resource-policy", env.authToken, nil, 200)
	policy := got["policy"].(map[string]any)
	if policy["server_storage_bytes"] != float64(10<<30) || policy["pending_upload_seconds"] != float64(86400) {
		t.Fatal(policy)
	}
	for _, raw := range []string{`{"server_files":0}`, `{"server_files":null}`, `{"server_files":2,"server_files":3}`, `{"unknown":4}`, `{"server_files":1.5}`} {
		rawAuthorized(t, env, "PATCH", "/admin/resource-policy", env.authToken, []byte(raw), 400)
	}
	authRequest(t, env, "PATCH", "/admin/resource-policy", env.authToken, map[string]int{"account_transfers": 1}, 200)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	rejected := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 403)
	if rejected["code"] != "resource_limit" {
		t.Fatal(rejected)
	}
	authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)
	usage := authRequest(t, env, "GET", "/auth/usage", env.userToken, nil, 200)["usage"].(map[string]any)
	if usage["transfers"] != float64(1) {
		t.Fatal(usage)
	}
	public := authRequest(t, env, "GET", "/config", "", nil, 200)
	if public["resource_policy"].(map[string]any)["account_transfers"] != float64(1) {
		t.Fatal(public)
	}
	// Lowering a live quota preserves existing records and the recovery lane.
	authRequest(t, env, "GET", "/admin/resource-policy", env.authToken, nil, 200)
	authRequest(t, env, "DELETE", "/transfers/"+id, env.userToken, nil, 204)
	authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)
}
func TestResourceRetentionDefaultsAndPendingDeadline(t *testing.T) {
	env := setupAuthFixture(t, false)
	authRequest(t, env, "PATCH", "/admin/resource-policy", env.authToken, map[string]int{"max_retention_seconds": 120, "pending_upload_seconds": 60}, 200)
	for _, route := range []string{"/transfers", "/slots"} {
		got := authRequest(t, env, "POST", route, env.userToken, nil, 201)
		until, err := time.Parse(time.RFC3339Nano, got["expires_at"].(string))
		if err != nil || time.Until(until) > 121*time.Second {
			t.Fatalf("unclamped expiry %v %v", got, err)
		}
		bad := authRequest(t, env, "POST", route, env.userToken, map[string]int{"expires_in_seconds": 121}, 400)
		if bad["code"] != "retention_limit" {
			t.Fatal(bad)
		}
	}
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	if _, err := env.db.Exec(`UPDATE transfers SET pending_expires_at=datetime('now','-1 second') WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", env.userToken, []byte("payload"), 410)
	authRequest(t, env, "POST", "/transfers/"+id+"/complete", env.userToken, nil, 410)
}
func TestResourceHistoryAndAdminUsersPagination(t *testing.T) {
	env := setupAuthFixture(t, false)
	for i := 0; i < 5; i++ {
		authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)
		authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)
	}
	seen := map[string]bool{}
	after := ""
	for pages := 0; pages < 10; pages++ {
		result := authRequest(t, env, "GET", "/auth/resources?limit=3&after="+after, env.userToken, nil, 200)
		count := 0
		for _, kind := range []string{"transfers", "slots"} {
			for _, raw := range result[kind].([]any) {
				id := raw.(map[string]any)["id"].(string)
				if seen[id] {
					t.Fatalf("duplicate %s", id)
				}
				seen[id] = true
				count++
			}
		}
		if count > 3 {
			t.Fatalf("unbounded page %d", count)
		}
		if result["next_cursor"] == nil {
			break
		}
		after = result["next_cursor"].(string)
	}
	if len(seen) != 10 {
		t.Fatalf("missing resources: %d", len(seen))
	}
	for _, query := range []string{"limit=101", "limit=0", "after=broken"} {
		authRequest(t, env, "GET", "/auth/resources?"+query, env.userToken, nil, 400)
	}
	users := authRequest(t, env, "GET", "/admin/users?limit=1", env.authToken, nil, 200)
	if len(users["users"].([]any)) != 1 || users["next_cursor"] == nil {
		t.Fatal(users)
	}
	next := authRequest(t, env, "GET", "/admin/users?limit=1&after="+users["next_cursor"].(string), env.authToken, nil, 200)
	if len(next["users"].([]any)) != 1 || next["next_cursor"] != nil {
		t.Fatal(next)
	}
}
func TestResourceFailedDeletionKeepsReservationUntilRetry(t *testing.T) {
	env := setupAuthFixture(t, false)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	if err := env.queries.CreateFile("payload", id, 32); err != nil {
		t.Fatal(err)
	}
	fs := fixtureStore(t, env)
	if err := fs.Save(id+"/payload", strings.NewReader(strings.Repeat("x", 32))); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.UpdateFileOffset("payload", 32, true); err != nil {
		t.Fatal(err)
	}
	failing := &failingDeleteStore{FileStore: fs}
	failing.fail.Store(true)
	if err := cleanup.RemoveTransfer(env.queries, failing, id); err == nil {
		t.Fatal("expected disk deletion failure")
	}
	u, err := env.queries.ResourceUsage("")
	if err != nil || u.ReservedBytes != 32 || u.OccupiedBytes != 32 || u.Transfers != 1 {
		t.Fatalf("early refund %+v %v", u, err)
	}
	failing.fail.Store(false)
	if err := cleanup.RemoveTransfer(env.queries, failing, id); err != nil {
		t.Fatal(err)
	}
	u, err = env.queries.ResourceUsage("")
	if err != nil || u.ReservedBytes != 0 || u.OccupiedBytes != 0 || u.Transfers != 0 {
		t.Fatalf("missing refund %+v %v", u, err)
	}
}

func TestResourceHistoryCompactsLargeNestedContents(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	for i := 0; i < 120; i++ {
		id := fmt.Sprintf("child-%03d", i)
		if err := env.queries.CreateSlotTransfer(slot, id, time.Now().Add(time.Hour), 0, nil, 200); err != nil {
			t.Fatal(err)
		}
		if err := env.queries.CreateFileWithQuota("file-"+id, id, 1, 1024); err != nil {
			t.Fatal(err)
		}
		if i < 100 {
			if err := env.queries.UpdateFileOffset("file-"+id, 1, true); err != nil {
				t.Fatal(err)
			}
			if err := env.queries.CompleteTransfer(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	for i := 0; i < 100; i++ {
		if err := env.queries.CreateFile(fmt.Sprintf("standalone-%d", i), id, 1); err != nil {
			t.Fatal(err)
		}
	}
	data := rawAuthorized(t, env, "GET", "/auth/resources?limit=100", env.userToken, nil, 200)
	if len(data) > 4096 {
		t.Fatalf("history expanded nested contents: %d bytes", len(data))
	}
	var response struct {
		Transfers []struct {
			Files     []any `json:"files"`
			FileCount int   `json:"file_count"`
		} `json:"transfers"`
		Slots []struct {
			Transfers []any `json:"transfers"`
			FileCount int   `json:"file_count"`
			Completed int   `json:"completed_files"`
			Reserved  int   `json:"reserved_files"`
			Total     int   `json:"total_size"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Transfers) != 1 || response.Transfers[0].FileCount != 100 || len(response.Transfers[0].Files) != 0 {
		t.Fatalf("transfer summary %s", data)
	}
	if len(response.Slots) != 1 || response.Slots[0].FileCount != 120 || response.Slots[0].Completed != 100 || response.Slots[0].Total != 120 || len(response.Slots[0].Transfers) != 0 {
		t.Fatalf("slot summary %s", data)
	}
	if err := env.queries.DeleteTransfer("child-000"); err != nil {
		t.Fatal(err)
	}
	data = rawAuthorized(t, env, "GET", "/auth/resources", env.userToken, nil, 200)
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.Slots[0].FileCount != 119 || response.Slots[0].Completed != 99 || response.Slots[0].Reserved != 120 {
		t.Fatalf("current and cumulative counters conflated %s", data)
	}
}
func TestResourceHistoryDoesNotReturnPartialSnapshotOnQueryFailure(t *testing.T) {
	env := setupAuthFixture(t, false)
	authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)
	if _, err := env.db.Exec(`DROP TABLE files`); err != nil {
		t.Fatal(err)
	}
	response := authRequest(t, env, "GET", "/auth/resources", env.userToken, nil, 500)
	if response["transfers"] != nil || response["slots"] != nil {
		t.Fatalf("deceptive partial snapshot %v", response)
	}
}
