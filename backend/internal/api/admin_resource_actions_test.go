package api_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestAdminResourceRevokeQueuesAndCancelsInboxChildren(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)["id"].(string)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	unregister, err := store.RegisterScopedStream(child, cancel, env.queries.StreamNamespace()+"\x00slot:"+slot)
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()
	path := "/admin/resources/slot/" + slot + "/revoke"
	authRequest(t, env, "POST", path, "", map[string]any{}, 401)
	authRequest(t, env, "POST", path, env.userToken, map[string]any{}, 403)
	result := authRequest(t, env, "POST", path, env.authToken, map[string]any{}, 202)
	if result["state"] != "pending" {
		t.Fatal(result)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("inbox child stream was not canceled")
	}
	transfer, err := env.queries.GetTransfer(child)
	if err != nil || transfer.Status != "revoked" {
		t.Fatalf("child not revoked: %+v %v", transfer, err)
	}
	inbox, err := env.queries.GetSlot(slot)
	if err != nil || inbox.Status != "revoked" {
		t.Fatalf("inbox not retained revoked: %+v %v", inbox, err)
	}
	for _, resource := range []struct{ kind, id string }{{"slot", slot}, {"transfer", child}} {
		status, err := env.queries.ResourceCleanup(resource.kind, resource.id)
		if err != nil || status.State == "none" {
			t.Fatalf("revocation not queued: %+v %v", status, err)
		}
	}
	authRequest(t, env, "GET", "/slots/"+slot+"/availability", "", nil, 410)
	overview := authRequest(t, env, "GET", "/admin/cleanup", env.authToken, nil, 200)
	if overview["pending_count"].(float64) < 2 {
		t.Fatal(overview)
	}
	authRequest(t, env, "GET", "/admin/cleanup", env.userToken, nil, 403)
	authRequest(t, env, "POST", "/admin/resources/slot/"+slot+"/cleanup", env.authToken, map[string]any{}, 202)
}

func TestAdminResourceCleanupRejectsActiveResourcesAndStaleAuthority(t *testing.T) {
	env := setupAuthFixture(t, false)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	root := "/admin/resources/transfer/" + id
	result := authRequest(t, env, "POST", root+"/cleanup", env.authToken, map[string]any{}, 409)
	if result["code"] != "cleanup_not_eligible" {
		t.Fatal(result)
	}
	authRequest(t, env, "POST", root+"/revoke", env.authToken, map[string]any{"url": "must-not-be-accepted"}, 400)
	authRequest(t, env, "POST", "/admin/resources/file/"+id+"/revoke", env.authToken, map[string]any{}, 400)
	authRequest(t, env, "POST", "/admin/resources/transfer/invalid/revoke", env.authToken, map[string]any{}, 400)
	authRequest(t, env, "POST", "/admin/resources/transfer/"+uuid.NewString()+"/revoke", env.authToken, map[string]any{}, 404)
	if _, err := env.db.Exec(`UPDATE sessions SET recent_until=0 WHERE user_id='fixture-admin'`); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "POST", root+"/revoke", env.authToken, map[string]any{}, 403)
	transfer, err := env.queries.GetTransfer(id)
	if err != nil || transfer.Status != "pending" {
		t.Fatalf("unauthorized cleanup changed resource: %+v %v", transfer, err)
	}
}

func TestAdminInboxRevokeStopsAnActualChildDownload(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)["id"].(string)
	file := uuid.NewString()
	size := int64(32 << 20)
	if err := env.queries.CreateFile(file, child, size); err != nil {
		t.Fatal(err)
	}
	if err := fixtureStore(t, env).Save(child+"/"+file, io.LimitReader(zeroReader{}, size)); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.UpdateFileOffset(file, size, true); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.CompleteTransfer(child); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("GET", env.url("/api/v1/transfers/"+child+"/files/"+file), nil)
	request.Header.Set("Authorization", "Bearer "+env.userToken)
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 || !store.HasReaders(child) {
		t.Fatal("child download not active", response.StatusCode)
	}
	authRequest(t, env, "POST", "/admin/resources/slot/"+slot+"/revoke", env.authToken, map[string]any{}, 202)
	deadline := time.Now().Add(2 * time.Second)
	for store.HasReaders(child) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if store.HasReaders(child) {
		t.Fatal("revocation did not stop the actual child response")
	}
	// Metadata stays until asynchronous cleanup; denial already applies.
	authRequest(t, env, "GET", "/transfers/"+child+"/files/"+file, env.userToken, nil, 410)
	status, err := env.queries.ResourceCleanup("transfer", child)
	if err != nil || status.State == "none" {
		t.Fatal(status, err)
	}
}
