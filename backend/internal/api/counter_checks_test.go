package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCounterChecksRequireAdminAndRemainPrivate(t *testing.T) {
	env := setupAuthFixture(t, false)
	authRequest(t, env, "GET", "/admin/counter-checks", "", nil, http.StatusUnauthorized)
	authRequest(t, env, "GET", "/admin/counter-checks", env.userToken, nil, http.StatusForbidden)
	req, _ := http.NewRequest("GET", env.url("/api/v1/admin/counter-checks"), nil)
	req.Header.Set("Authorization", "Bearer "+env.authToken)
	response, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(response.StatusCode, response.Header)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil || strings.Contains(string(data), env.dataDir) || strings.Contains(string(data), env.authToken) {
		t.Fatal("unsafe status response", err)
	}
	var status struct {
		State   string `json:"state"`
		Pending bool   `json:"scan_pending"`
		Count   int    `json:"pending_count"`
	}
	if err := json.Unmarshal(data, &status); err != nil || status.State != "pending" || !status.Pending || status.Count < 0 || status.Count > 64 {
		t.Fatalf("invalid initial counter status %s: %v", data, err)
	}
}
