package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestAdminResourceInventoryAccessFiltersAndRedaction(t *testing.T) {
	env := setupAuthFixture(t, false)
	owner := "11111111-1111-4111-8111-111111111111"
	transfer := "22222222-2222-4222-8222-222222222222"
	slot := "33333333-3333-4333-8333-333333333333"
	if err := env.queries.CreateUser(database.User{ID: owner, Username: "resource-owner", Role: "user", PasswordHash: []byte("DO_NOT_ECHO_HASH")}, false); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if err := env.queries.CreateSlot(slot, until, []byte("DO_NOT_ECHO_DELETE"), owner); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.CreateTransfer(transfer, until, 0, []byte("DO_NOT_ECHO_DELETE")); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.LinkSlotTransfer(slot, transfer); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.CreateFile("file", transfer, 100); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.UpdateFileOffset("file", 20, false); err != nil {
		t.Fatal(err)
	}
	manifest := "DO_NOT_ECHO_MANIFEST"
	if err := env.queries.SaveManifest(transfer, []byte(manifest)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/resources", "/admin/resources/transfer/" + transfer, "/admin/resources/transfer/" + transfer + "/events"} {
		authRequest(t, env, "GET", path, "", nil, 401)
		authRequest(t, env, "GET", path, env.userToken, nil, 403)
	}
	page := authRequest(t, env, "GET", "/admin/resources?owner_id="+owner+"&limit=1", env.authToken, nil, 200)
	if len(page["resources"].([]any)) != 1 || page["next_cursor"] == nil {
		t.Fatal(page)
	}
	authRequest(t, env, "GET", "/admin/resources?type=slot&limit=1&after="+page["next_cursor"].(string), env.authToken, nil, 400)
	filtered := authRequest(t, env, "GET", "/admin/resources?type=transfer&status=pending&owner_id="+owner, env.authToken, nil, 200)
	items := filtered["resources"].([]any)
	if len(items) != 1 {
		t.Fatal(filtered)
	}
	detail := authRequest(t, env, "GET", "/admin/resources/transfer/"+transfer, env.authToken, nil, 200)
	if detail["parent_slot_id"] != slot || detail["owner_id"] != owner || detail["owner_username"] != "resource-owner" || detail["file_count"] != float64(1) || detail["reserved_bytes"] != float64(100+len(manifest)) || detail["occupied_bytes_estimate"] != float64(20+len(manifest)) {
		t.Fatal(detail)
	}
	slotDetail := authRequest(t, env, "GET", "/admin/resources/slot/"+slot, env.authToken, nil, 200)
	if slotDetail["child_transfer_count"] != float64(1) {
		t.Fatal(slotDetail)
	}
	for _, value := range []any{page, filtered, detail, slotDetail} {
		raw, _ := json.Marshal(value)
		if strings.Contains(string(raw), "DO_NOT_ECHO") || strings.Contains(string(raw), "delete_token") || strings.Contains(string(raw), "password_hash") || strings.Contains(string(raw), "recipient_public_key") {
			t.Fatal("sensitive resource data", string(raw))
		}
	}
	for _, query := range []string{"type=file", "status=DO_NOT_ECHO", "owner_id=https://host/#key", "limit=0", "limit=101", "limit=2&limit=3", "after=DO_NOT_ECHO", "secret=DO_NOT_ECHO", "type="} {
		body := authRequest(t, env, "GET", "/admin/resources?"+query, env.authToken, nil, 400)
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "DO_NOT_ECHO") {
			t.Fatal("invalid query leaked")
		}
	}
	authRequest(t, env, "GET", "/admin/resources/file/"+transfer, env.authToken, nil, 400)
	authRequest(t, env, "GET", "/admin/resources/transfer/44444444-4444-4444-8444-444444444444", env.authToken, nil, 404)
	req, _ := http.NewRequest("GET", env.url("/api/v1/admin/resources/transfer/"+transfer), nil)
	req.Header.Set("Authorization", "Bearer "+env.authToken)
	response, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("resource metadata cached")
	}
}

func TestAdminResourceRelatedAuditAfterDeletion(t *testing.T) {
	env := setupAuthFixture(t, false)
	created := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)
	id := created["id"].(string)
	authRequest(t, env, "DELETE", "/transfers/"+id, env.authToken, nil, 204)
	authRequest(t, env, "GET", "/admin/resources/transfer/"+id, env.authToken, nil, 404)
	events := authRequest(t, env, "GET", "/admin/resources/transfer/"+id+"/events", env.authToken, nil, 200)
	entries := events["events"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["kind"] != "transfer.revoked" {
		t.Fatal(events)
	}
	for _, query := range []string{"before=-1", "before=secret-token", "before=9223372036854775808", "limit=101", "before=1&before=2"} {
		out := authRequest(t, env, "GET", "/admin/resources/transfer/"+id+"/events?"+query, env.authToken, nil, 400)
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "secret-token") {
			t.Fatal("cursor echoed")
		}
	}
}
