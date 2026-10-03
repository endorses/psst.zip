package api_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestHealthIdentifiesAPIWithoutCreatingResources(t *testing.T) {
	env := setup(t)
	target := env.url("/api/v1/health")
	resp := request(t, env, http.MethodGet, target, nil, http.StatusOK)
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("health Content-Type = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("health Cache-Control = %q", got)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"service": "psst.zip", "api_version": float64(1)}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("health body = %v, want %v", body, want)
	}
	request(t, env, http.MethodPost, target, nil, http.StatusMethodNotAllowed)
	var count int
	if err := env.db.QueryRow("SELECT (SELECT COUNT(*) FROM transfers) + (SELECT COUNT(*) FROM slots)").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("health check created %d resources", count)
	}
}
