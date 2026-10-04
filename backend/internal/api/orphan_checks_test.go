package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/reconcile"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestOrphanChecksRequireAdminAndRemainPrivate(t *testing.T) {
	env := setupAuthFixture(t, false)
	const endpoint = "/admin/orphan-checks"
	authRequest(t, env, "GET", endpoint, "", nil, http.StatusUnauthorized)
	authRequest(t, env, "GET", endpoint, env.userToken, nil, http.StatusForbidden)
	req, err := http.NewRequest("GET", env.url("/api/v1"+endpoint), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.authToken)
	response, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(response.StatusCode, response.Header)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil || strings.Contains(string(data), env.dataDir) || strings.Contains(string(data), env.authToken) {
		t.Fatal("unsafe orphan status response", err)
	}
	var status map[string]any
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"state": true, "scan_pending": true, "last_scan_completed_at": true,
		"pending_directories": true, "pending_candidates": true, "busy_count": true,
		"failed_count": true, "unsupported_count": true, "saturated": true,
		"unstable": true, "oldest_pending_at": true, "scan_error_code": true,
	}
	for key := range status {
		if !allowed[key] {
			t.Fatalf("unexpected private-detail field %q", key)
		}
	}
	if status["state"] != "pending" || status["scan_pending"] != true {
		t.Fatal("unscanned storage reported as verified", status)
	}
	for _, key := range []string{"pending_directories", "pending_candidates", "busy_count", "failed_count", "unsupported_count"} {
		if status[key] != float64(0) {
			t.Fatalf("invalid initial queued-work count %q: %v", key, status[key])
		}
	}
	if status["saturated"] != false || status["unstable"] != false {
		t.Fatal("invalid initial inventory flags", status)
	}
}

func TestOrphanChecksExposeUnresolvedWorkWithoutEntryNames(t *testing.T) {
	env := setupAuthFixture(t, false)
	root := filepath.Join(env.dataDir, "files")
	fs, err := store.NewDiskStore(root)
	if err != nil {
		t.Fatal(err)
	}
	const privateName = "private-resource#DO_NOT_EXPOSE"
	filename := filepath.Join(root, privateName)
	if err := os.WriteFile(filename, []byte("unmanaged data"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	found := false
	for range 16 {
		if err := reconcile.SweepOrphansAt(context.Background(), env.queries, fs, now); err != nil {
			t.Fatal(err)
		}
		status := authRequest(t, env, "GET", "/admin/orphan-checks", env.authToken, nil, http.StatusOK)
		data, err := json.Marshal(status)
		if err != nil || strings.Contains(string(data), root) || strings.Contains(string(data), privateName) {
			t.Fatal("orphan status disclosed an entry name", err)
		}
		if status["unsupported_count"] == float64(1) {
			if status["state"] != "degraded" || status["scan_pending"] != true {
				t.Fatal("unresolved entry presented as fully checked", status)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("inventory did not expose unresolved work")
	}
	if data, err := os.ReadFile(filename); err != nil || string(data) != "unmanaged data" {
		t.Fatalf("inventory removed unsupported entry: %q %v", data, err)
	}
}
