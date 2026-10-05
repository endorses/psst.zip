package api_test

import (
	"bytes"
	"encoding/json"
	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

func TestSharedTitlesPublicReadsOwnerRenameAndAccountIsolation(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, alice := addAccount(t, env, "title-alice", "user")
	_, bob := addAccount(t, env, "title-bob", "user")
	created := authRequest(t, env, "POST", "/transfers", alice, map[string]any{"title": "  音楽 <script>  "}, 201)
	id := created["id"].(string)
	if created["title"] != "音楽 <script>" {
		t.Fatal(created)
	}
	read := authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)
	if read["title"] != created["title"] {
		t.Fatal("public page cannot see shared title", read)
	}
	for _, entry := range []struct {
		token  string
		status int
	}{{"", 401}, {bob, 404}, {created["delete_token"].(string), 401}, {alice, 200}} {
		authRequest(t, env, "PATCH", "/transfers/"+id+"/title", entry.token, map[string]any{"title": "Renamed"}, entry.status)
	}
	read = authRequest(t, env, "GET", "/transfers/"+id, bob, nil, 200)
	if read["title"] != "Renamed" {
		t.Fatal("rename not shared across sessions", read)
	}
	authRequest(t, env, "PATCH", "/transfers/"+id+"/title", alice, map[string]any{"title": nil}, 200)
	if authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)["title"] != nil {
		t.Fatal("clear not persisted")
	}
	slot := authRequest(t, env, "POST", "/slots", alice, map[string]any{"receive_protocol": 2, "recipient_public_key": fixtureRecipientKey, "title": "Wedding"}, 201)
	sid := slot["id"].(string)
	public := authRequest(t, env, "GET", "/slots/"+sid+"/availability", "", nil, 200)
	if public["title"] != "Wedding" {
		t.Fatal(public)
	}
	authRequest(t, env, "PATCH", "/slots/"+sid+"/title", bob, map[string]any{"title": "wrong"}, 404)
	authRequest(t, env, "PATCH", "/slots/"+sid+"/title", alice, map[string]any{"title": "Photos"}, 200)
	inbox := authRequest(t, env, "GET", "/slots/"+sid+"/inbox", alice, nil, 200)
	if inbox["title"] != "Photos" {
		t.Fatal(inbox)
	}
	public = authRequest(t, env, "GET", "/slots/"+sid+"/availability", "", nil, 200)
	if public["title"] != "Photos" || public["transfers"] != nil {
		t.Fatal("title availability leaked children", public)
	}
}
func TestSharedTitleValidationAndCookieRenameCSRF(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, alice := addAccount(t, env, "title-cookie", "user")
	id := authRequest(t, env, "POST", "/transfers", alice, nil, 201)["id"].(string)
	for _, invalid := range []any{strings.Repeat("😀", 201), "a\n", "a\u007f", float64(3)} {
		authRequest(t, env, "PATCH", "/transfers/"+id+"/title", alice, map[string]any{"title": invalid}, 400)
	}
	for _, raw := range []string{`{"title":"\ud800"}`, `{"title":"\udc00"}`} {
		authRequest(t, env, "PATCH", "/transfers/"+id+"/title", alice, []byte(raw), 400)
	}
	authRequest(t, env, "PATCH", "/transfers/"+id+"/title", alice, []byte(`{"title":"\ud83d\ude00"}`), 200)
	authRequest(t, env, "PATCH", "/transfers/"+id+"/title", alice, map[string]any{"title": strings.Repeat("😀", 200)}, 200)
	raw, _ := json.Marshal(map[string]any{"username": "title-cookie", "password": "correct horse battery", "session_type": "web"})
	request, _ := http.NewRequest("POST", env.url("/api/v1/auth/login"), bytes.NewReader(raw))
	request.Header.Set("Origin", env.server.URL)
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 || len(response.Cookies()) != 1 {
		t.Fatal(response.Status)
	}
	for _, origin := range []string{"", "https://evil.example", env.server.URL} {
		request, _ = http.NewRequest("PATCH", env.url("/api/v1/transfers/"+id+"/title"), strings.NewReader(`{"title":"Cookie rename"}`))
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(response.Cookies()[0])
		renamed, err := env.server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = renamed.Body.Close()
		want := 403
		if origin == env.server.URL {
			want = 200
		}
		if renamed.StatusCode != want {
			t.Fatal(origin, renamed.Status)
		}
	}
}
func TestExhaustedPublicAndHistoryStateAllowsManifestAndFinalReceipt(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 1)
	first := newUpload(t, env, id, 4)
	second := newUpload(t, env, id, 4)
	patch(t, env, first, "data", 0, http.StatusNoContent, false)
	patch(t, env, second, "more", 0, http.StatusNoContent, false)
	finish(t, env, id)
	response := request(t, env, http.MethodGet, first, nil, 200)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if status := authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)["status"]; status != "complete" {
		t.Fatal("partial exhaustion closed link", status)
	}
	response = request(t, env, http.MethodGet, second, nil, 200)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	exhausted := authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)
	if exhausted["status"] != "exhausted" || exhausted["inactive_reason"] != "download_limit" || exhausted["downloaded_at"] != nil {
		t.Fatal(exhausted)
	}
	history := authRequest(t, env, "GET", "/auth/resources?kind=transfer", env.userToken, nil, 200)
	rows := history["transfers"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["status"] != "exhausted" {
		t.Fatal(history)
	}
	request(t, env, http.MethodGet, first, nil, http.StatusGone)
	response = request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+id+"/manifest"), nil, 200)
	_ = response.Body.Close()
	authRequest(t, env, "POST", "/transfers/"+id+"/downloaded", "", nil, 204)
	file, err := env.queries.GetFile(path.Base(first))
	if err != nil || file.DownloadCount != 1 {
		t.Fatal(file, err)
	}
}

func TestInterruptedFinalDownloadClosesWithoutAcknowledging(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 1)
	file := newUpload(t, env, id, 4)
	patch(t, env, file, "data", 0, http.StatusNoContent, false)
	finish(t, env, id)
	server := api.NewServer(config.Config{RateLimitGlobal: 1000, RateLimitBurst: 1000}, env.queries, interruptedFileStore{fixtureStore(t, env)}).Router()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/transfers/"+id+"/files/"+path.Base(file), nil))
	if response.Code != 200 || response.Body.Len() != 0 {
		t.Fatal(response.Code, response.Body.Len())
	}
	snapshot := downloadStatus(t, env, id)
	if snapshot.Status != "exhausted" || snapshot.InactiveReason == nil || snapshot.DownloadedAt != nil {
		t.Fatal(snapshot)
	}
	request(t, env, http.MethodGet, file, nil, http.StatusGone)
	sweep(t, env)
	snapshot = downloadStatus(t, env, id)
	if snapshot.Status != "exhausted" || snapshot.DownloadedAt != nil {
		t.Fatal("cleanup changed delivery state", snapshot)
	}
}
