package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func historyRequest(t *testing.T, h http.Handler, query, token string, status int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://example.test/api/v1/auth/resources"+query, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != status {
		t.Fatalf("history status %d want %d: %s", w.Code, status, w.Body.String())
	}
	if status == 200 && w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("history response cacheable")
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestAccountHistoryDefaultBoundsAndRequiredSummaryContract(t *testing.T) {
	h, q, db, _ := guestCapacityAPI(t)
	inboxPageSession(t, q, "private-owner", "owner-session")
	for i := 0; i < 51; i++ {
		if err := q.CreateTransfer(fmt.Sprintf("transfer-%02d", i), time.Now().Add(time.Hour), 2, nil, "private-owner"); err != nil {
			t.Fatal(err)
		}
	}
	out := historyRequest(t, h, "", "owner-session", 200)
	if out["paginated"] != true || len(out["transfers"].([]any))+len(out["slots"].([]any)) != 50 || out["next_cursor"] == nil {
		t.Fatal("wrong default bounded page", out)
	}
	next := historyRequest(t, h, "?after="+out["next_cursor"].(string), "owner-session", 200)
	if len(next["transfers"].([]any))+len(next["slots"].([]any)) != 2 || next["next_cursor"] != nil {
		t.Fatal("incomplete continuation", next)
	}
	for _, page := range []map[string]any{out, next} {
		for _, kind := range []string{"transfers", "slots"} {
			for _, entry := range page[kind].([]any) {
				item := entry.(map[string]any)
				summary := item["summary"].(map[string]any)
				if len(summary) != 4 || summary["state"] != "ready" || summary["file_count"] != float64(0) || summary["total_size"] != float64(0) || summary["completed_files"] != float64(0) || item["file_count"] != summary["file_count"] || item["total_size"] != summary["total_size"] {
					t.Fatal("invalid ready summary", item)
				}
				if item["owner_id"] != nil {
					t.Fatal("ordinary account history disclosed owner field", item)
				}
				if kind == "slots" {
					if item["completed_files"] != summary["completed_files"] || len(item["transfers"].([]any)) != 0 {
						t.Fatal(item)
					}
				} else {
					if len(item["files"].([]any)) != 0 || item["max_downloads"] != float64(2) {
						t.Fatal(item)
					}
				}
			}
		}
	}
	if _, err := db.Exec(`UPDATE admin_resource_totals SET inbox_known=0`); err != nil {
		t.Fatal(err)
	}
	unknown := historyRequest(t, h, "?limit=100", "owner-session", 200)
	for _, kind := range []string{"transfers", "slots"} {
		for _, entry := range unknown[kind].([]any) {
			item := entry.(map[string]any)
			summary := item["summary"].(map[string]any)
			if summary["state"] != "updating" || summary["file_count"] != nil || summary["completed_files"] != nil || summary["total_size"] != nil {
				t.Fatal("fabricated unknown summary", item)
			}
			for _, field := range []string{"file_count", "total_size"} {
				value, present := item[field]
				if !present || value != nil {
					t.Fatal("required unknown field must be null", field, item)
				}
			}
			if kind == "slots" {
				value, present := item["completed_files"]
				if !present || value != nil {
					t.Fatal(item)
				}
			}
		}
	}
}
func TestAccountHistoryStrictQueriesScopesAndFailedCounters(t *testing.T) {
	h, q, db, _ := guestCapacityAPI(t)
	inboxPageSession(t, q, "private-owner", "owner-session")
	if err := q.CreateTransfer("send", time.Now().Add(time.Hour), 0, nil, "private-owner"); err != nil {
		t.Fatal(err)
	}
	for _, account := range []struct{ id, role string }{{"other", "user"}, {"administrator", "admin"}} {
		if err := q.CreateUser(database.User{ID: account.id, Username: account.id, Role: account.role, PasswordHash: []byte("hash")}, false); err != nil {
			t.Fatal(err)
		}
		inboxPageSession(t, q, account.id, account.id+"-session")
	}
	historyRequest(t, h, "", "", 401)
	historyRequest(t, h, "?all=true", "owner-session", 403)
	historyRequest(t, h, "", "administrator-session", 403)
	other := historyRequest(t, h, "", "other-session", 200)
	if len(other["transfers"].([]any)) != 0 || len(other["slots"].([]any)) != 0 {
		t.Fatal("cross-account history exposed", other)
	}
	own := historyRequest(t, h, "?limit=1", "owner-session", 200)
	all := historyRequest(t, h, "?all=true&limit=1", "administrator-session", 200)
	for _, entry := range append(all["transfers"].([]any), all["slots"].([]any)...) {
		if entry.(map[string]any)["owner_id"] != "private-owner" {
			t.Fatal("admin lost owner attribution", entry)
		}
	}
	historyRequest(t, h, "?limit=1&after="+own["next_cursor"].(string), "other-session", 400)
	historyRequest(t, h, "?all=true&limit=1&after="+own["next_cursor"].(string), "administrator-session", 400)
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=01", "?limit=1&limit=2", "?after=x&after=y", "?all=TRUE", "?all=true&all=false", "?extra=1", "?after=%zz", "?after=broken"} {
		historyRequest(t, h, query, "owner-session", 400)
	}
	// Summary reads do not depend on the files table, even for a send resource.
	if _, err := db.Exec(`DROP TABLE files`); err != nil {
		t.Fatal(err)
	}
	historyRequest(t, h, "", "owner-session", 200)
	if _, err := db.Exec(`DROP TABLE admin_resource_totals`); err != nil {
		t.Fatal(err)
	}
	failed := historyRequest(t, h, "", "owner-session", 503)
	if len(failed) != 1 || failed["error"] != "history unavailable" {
		t.Fatal("partial history on query failure", failed)
	}
}
