package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAbuseContactAdminAuthorizationPublicOptInAndIndependence(t *testing.T) {
	env := setupAuthFixture(t, false)
	config := authRequest(t, env, "GET", "/config", "", nil, 200)
	initialSize := config["max_file_size"]
	if config["abuse_contact_email"] != "" {
		t.Fatal("contact enabled by default", config)
	}
	for _, path := range []string{"/admin/abuse-contact"} {
		for _, method := range []string{"GET", "PATCH"} {
			authRequest(t, env, method, path, "", map[string]string{"email": "abuse@example.com"}, 401)
			authRequest(t, env, method, path, env.userToken, map[string]string{"email": "abuse@example.com"}, 403)
		}
	}
	for _, email := range []string{"abuse@example.com", "security+contact@example.org", ""} {
		out := authRequest(t, env, "PATCH", "/admin/abuse-contact", env.authToken, map[string]string{"email": email}, 200)
		if out["email"] != email {
			t.Fatal(out)
		}
		admin := authRequest(t, env, "GET", "/admin/abuse-contact", env.authToken, nil, 200)
		public := authRequest(t, env, "GET", "/config", "", nil, 200)
		if admin["email"] != email || public["abuse_contact_email"] != email || public["max_file_size"] != initialSize {
			t.Fatal(admin, public)
		}
	}
	if _, err := env.db.Exec(`UPDATE sessions SET recent_until=0 WHERE user_id='fixture-admin'`); err != nil {
		t.Fatal(err)
	}
	out := authRequest(t, env, "PATCH", "/admin/abuse-contact", env.authToken, map[string]string{"email": "blocked@example.com"}, 403)
	if out["code"] != "recent_authentication_required" {
		t.Fatal(out)
	}
	public := authRequest(t, env, "GET", "/config", "", nil, 200)
	if public["abuse_contact_email"] != "" {
		t.Fatal("rejected mutation changed public contact")
	}
}
func TestAbuseContactStrictBodyAndNoSecretEcho(t *testing.T) {
	env := setupAuthFixture(t, false)
	for _, body := range []string{`{}`, `null`, `{"email":null}`, `{"email":1}`, `{"email":"abuse@example.com","extra":"secret"}`, `{"email":"abuse@example.com"}{}`, `{"email":"mailto:secret@example.com"}`, `{"email":"secret@example.com?bcc=other@example.com"}`, `{"email":"secret\r\nBcc:other@example.com"}`, `{"email":"` + strings.Repeat("a", 1100) + `@example.com"}`} {
		req, _ := http.NewRequest("PATCH", env.url("/api/v1/admin/abuse-contact"), strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+env.authToken)
		req.Header.Set("Content-Type", "application/json")
		response, err := env.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 400 || strings.Contains(string(raw), "secret") {
			t.Fatal(response.StatusCode, string(raw))
		}
	}
	req, _ := http.NewRequest("GET", env.url("/api/v1/admin/abuse-contact"), nil)
	req.Header.Set("Authorization", "Bearer "+env.authToken)
	response, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("contact cached")
	}
	var out map[string]any
	if err = json.NewDecoder(response.Body).Decode(&out); err != nil || out["email"] != "" {
		t.Fatal(out, err)
	}
}
