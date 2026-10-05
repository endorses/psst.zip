package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func historyChangeRequest(t *testing.T, h http.Handler, query, token string, status int) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	r := httptest.NewRequest("GET", "https://example.test/api/v1/auth/history/changes"+query, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("history changes status %d want %d: %s", w.Code, status, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out, w
}
func TestHistorySyncHTTPContractAndAuthorization(t *testing.T) {
	h, q, db, _ := guestCapacityAPI(t)
	inboxPageSession(t, q, "private-owner", "owner-session")
	snapshot := historyRequest(t, h, "", "owner-session", 200)
	cursor := snapshot["sync_cursor"].(string)
	empty, w := historyChangeRequest(t, h, "?cursor="+url.QueryEscape(cursor), "owner-session", 200)
	if empty["version"] != float64(1) || empty["generation"] != snapshot["generation"] || len(empty["changes"].([]any)) != 0 || empty["has_more"] != false || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid empty response", empty)
	}
	if err := q.CreateTransfer("new-send", time.Now().Add(time.Hour), 0, nil, "private-owner"); err != nil {
		t.Fatal(err)
	}
	changed, w := historyChangeRequest(t, h, "?cursor="+url.QueryEscape(cursor), "owner-session", 200)
	if w.Body.Len() > 1024*1024 || len(changed["changes"].([]any)) != 1 {
		t.Fatal("unbounded/uncoalesced changes", changed)
	}
	event := changed["changes"].([]any)[0].(map[string]any)
	resource := event["resource"].(map[string]any)
	if event["kind"] != "transfer" || event["action"] != "upsert" || event["id"] != "new-send" || resource["revision"] != event["revision"] || resource["summary"] == nil || len(resource["files"].([]any)) != 0 || resource["owner_id"] != nil {
		t.Fatal("invalid compact upsert", event)
	}
	historyChangeRequest(t, h, "?cursor="+url.QueryEscape(cursor), "", 401)
	for _, query := range []string{"", "?cursor=bad", "?cursor=" + url.QueryEscape(cursor) + "&limit=101", "?cursor=" + url.QueryEscape(cursor) + "&limit=01", "?cursor=" + url.QueryEscape(cursor) + "&extra=1", "?cursor=" + url.QueryEscape(cursor) + "&cursor=" + url.QueryEscape(cursor)} {
		historyChangeRequest(t, h, query, "owner-session", 400)
	}
	if _, err := db.Exec(`UPDATE history_sync_state SET generation='00000000-0000-0000-0000-000000000000' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_, w = historyChangeRequest(t, h, "?cursor="+url.QueryEscape(cursor), "owner-session", 409)
	if w.Header().Get("X-Psst-Error-Code") != "history_sync_reset_required" {
		t.Fatal("missing typed reset", w.Header())
	}
	if _, err := db.Exec(`UPDATE users SET disabled=1 WHERE id='private-owner'`); err != nil {
		t.Fatal(err)
	}
	historyChangeRequest(t, h, "?cursor="+url.QueryEscape(cursor), "owner-session", 401)
}
func TestHistorySyncConfigCapabilityAndQuietResponse(t *testing.T) {
	h, q, _, _ := guestCapacityAPI(t)
	inboxPageSession(t, q, "private-owner", "owner-session")
	r := httptest.NewRequest("GET", "https://example.test/api/v1/config", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var config map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || config["history_sync_version"] != float64(1) {
		t.Fatal("missing capability", config)
	}
	snapshot := historyRequest(t, h, "", "owner-session", 200)
	cursor := snapshot["sync_cursor"].(string)
	for i := 0; i < 6; i++ {
		page, w := historyChangeRequest(t, h, "?cursor="+url.QueryEscape(cursor), "owner-session", 200)
		if w.Body.Len() > 1024 || page["next_cursor"] != cursor || len(page["changes"].([]any)) != 0 {
			t.Fatal("quiet response is not small/stable", w.Body.String())
		}
	}
}

func TestHistorySyncWorstCaseValidTitlesStayUnderResponseCap(t *testing.T) {
	h, q, _, _ := guestCapacityAPI(t)
	inboxPageSession(t, q, "private-owner", "owner-session")
	snapshot := historyRequest(t, h, "", "owner-session", 200)
	cursor := snapshot["sync_cursor"].(string)
	// 200 four-byte characters reach the schema's 800-byte maximum. JSON escaping
	// can expand it further, so measure the actual serialized envelope.
	title := strings.Repeat("😀", 200)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("%08x-3333-4333-8333-333333333333", i)
		if err := q.CreateTransferWithTitle(id, time.Now().Add(time.Hour), 2147483647, nil, "private-owner", &title); err != nil {
			t.Fatal(err)
		}
	}
	for batch := 0; batch < 10; batch++ {
		out, w := historyChangeRequest(t, h, "?limit=100&cursor="+url.QueryEscape(cursor), "owner-session", 200)
		if w.Body.Len() > 1024*1024 {
			t.Fatal("valid max batch exceeded response cap", w.Body.Len())
		}
		for _, raw := range out["changes"].([]any) {
			row := raw.(map[string]any)
			fact := row["resource"].(map[string]any)
			if fact["title"] != title {
				t.Fatal("title truncated or altered")
			}
		}
		if !out["has_more"].(bool) {
			return
		}
		cursor = out["next_cursor"].(string)
	}
	t.Fatal("bounded catchup did not finish")
}
