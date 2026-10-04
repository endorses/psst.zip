package api_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func inboxPageSession(t *testing.T, q *database.Queries, user, token string) {
	t.Helper()
	hash := sha256.Sum256([]byte(token))
	if err := q.CreateSession(database.Session{ID: token, UserID: user, DeviceName: "Tests", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, hash[:], []byte("hash")); err != nil {
		t.Fatal(err)
	}
}
func inboxPageRequest(t *testing.T, h http.Handler, path, token string, status int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://example.test/api/v1/slots/"+guestCapacitySlot+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != status {
		t.Fatalf("%s status %d want %d: %s", path, w.Code, status, w.Body.String())
	}
	if status == 200 && w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("owner inbox response cacheable")
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return out
}
func TestInboxPagesHTTPContractLegacyBoundAndAuthorization(t *testing.T) {
	for _, count := range []int{0, 100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			h, q, _, _ := guestCapacityAPI(t)
			inboxPageSession(t, q, "private-owner", "owner-session")
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("child-%03d", i)
				if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil, "private-owner"); err != nil {
					t.Fatal(err)
				}
				if err := q.LinkSlotTransfer(guestCapacitySlot, id); err != nil {
					t.Fatal(err)
				}
			}
			inboxPageRequest(t, h, "/inbox", "", 401)
			if err := q.CreateUser(database.User{ID: "other", Username: "other", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
				t.Fatal(err)
			}
			inboxPageSession(t, q, "other", "other-session")
			inboxPageRequest(t, h, "/inbox", "other-session", 403)
			out := inboxPageRequest(t, h, "/inbox?limit=50", "owner-session", 200)
			if out["paginated"] != true {
				t.Fatal(out)
			}
			if _, exists := out["completed_files"]; exists {
				t.Fatal("new endpoint must use summary completion only", out)
			}
			if _, exists := out["next_cursor"]; !exists {
				t.Fatal("missing explicit continuation", out)
			}
			summary := out["summary"].(map[string]any)
			if summary["state"] != "ready" || summary["completed_files"] != float64(0) || summary["file_count"] != float64(0) || summary["total_size"] != float64(0) {
				t.Fatal(summary)
			}
			seen := map[string]bool{}
			for {
				transfers := out["transfers"].([]any)
				if len(transfers) > 50 {
					t.Fatal("unbounded page", len(transfers))
				}
				for _, entry := range transfers {
					item := entry.(map[string]any)
					id := item["transfer_id"].(string)
					if seen[id] {
						t.Fatal("duplicate", id)
					}
					seen[id] = true
				}
				if out["next_cursor"] == nil {
					break
				}
				out = inboxPageRequest(t, h, "/inbox?limit=50&after="+out["next_cursor"].(string), "owner-session", 200)
			}
			if len(seen) != count {
				t.Fatal("missing children", len(seen), count)
			}
			legacyStatus := 200
			if count > 100 {
				legacyStatus = 409
			}
			legacy := inboxPageRequest(t, h, "", "owner-session", legacyStatus)
			if count > 100 {
				if len(legacy) != 2 || legacy["code"] != "inbox_pagination_required" || legacy["error"] != "inbox requires paginated access" {
					t.Fatal("legacy leaked partial history", legacy)
				}
			} else if len(legacy["transfers"].([]any)) != count || legacy["completed_files"] != float64(0) {
				t.Fatal("legacy response changed", legacy)
			}
		})
	}
}
func TestInboxPagesHTTPUpdatingErrorsAndEmptyContinuation(t *testing.T) {
	h, q, db, _ := guestCapacityAPI(t)
	inboxPageSession(t, q, "private-owner", "owner-session")
	for _, id := range []string{"a", "b", "c"} {
		if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil, "private-owner"); err != nil {
			t.Fatal(err)
		}
		if err := q.LinkSlotTransfer(guestCapacitySlot, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE transfers SET pending_expires_at='2000-01-01' WHERE id IN ('a','b')`); err != nil {
		t.Fatal(err)
	}
	out := inboxPageRequest(t, h, "/inbox?limit=2", "owner-session", 200)
	if len(out["transfers"].([]any)) != 0 || out["next_cursor"] == nil {
		t.Fatal("filtered prefix hides live suffix", out)
	}
	out = inboxPageRequest(t, h, "/inbox?limit=2&after="+out["next_cursor"].(string), "owner-session", 200)
	if len(out["transfers"].([]any)) != 1 || out["next_cursor"] != nil {
		t.Fatal(out)
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=-1", "?limit=01", "?limit=1&limit=2", "?after=!", "?after=" + strings.Repeat("a", 513)} {
		inboxPageRequest(t, h, "/inbox"+query, "owner-session", 400)
	}
	if _, err := db.Exec(`UPDATE admin_resource_totals SET inbox_known=0 WHERE kind='slot'`); err != nil {
		t.Fatal(err)
	}
	out = inboxPageRequest(t, h, "/inbox", "owner-session", 200)
	summary := out["summary"].(map[string]any)
	if len(summary) != 4 || summary["state"] != "updating" || summary["completed_files"] != nil || summary["file_count"] != nil || summary["total_size"] != nil {
		t.Fatal("unknown summary fabricated", summary)
	}
	if _, exists := out["completed_files"]; exists {
		t.Fatal("fabricated legacy completion", out)
	}
	if legacy := inboxPageRequest(t, h, "", "owner-session", 503); legacy["code"] != "inbox_summary_updating" {
		t.Fatal(legacy)
	}
	for i := 0; i < 101; i++ {
		if err := q.CreateFile(fmt.Sprint(i), "c", 1); err != nil {
			t.Fatal(err)
		}
	}
	bad := inboxPageRequest(t, h, "/inbox", "owner-session", 409)
	if len(bad) != 2 || bad["code"] != "transfer_file_limit_exceeded" {
		t.Fatal("oversized child leaked partial metadata", bad)
	}
	if _, err := db.Exec(`UPDATE slots SET expires_at='2000-01-01' WHERE id=?`, guestCapacitySlot); err != nil {
		t.Fatal(err)
	}
	inboxPageRequest(t, h, "/inbox", "owner-session", 410)
}

func TestInboxPagesFailClosedWhenPayloadMetadataUnavailable(t *testing.T) {
	h, q, db, _ := guestCapacityAPI(t)
	inboxPageSession(t, q, "private-owner", "owner-session")
	if err := q.CreateTransfer("private-child", time.Now().Add(time.Hour), 0, nil, "private-owner"); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer(guestCapacitySlot, "private-child"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE files`); err != nil {
		t.Fatal(err)
	}
	// A populated page still needs its bounded canonical child metadata probe.
	// Its failure must not turn the child into an apparently empty submission.
	for _, suffix := range []string{"", "/inbox"} {
		response := inboxPageRequest(t, h, suffix, "owner-session", 503)
		if len(response) != 1 || response["error"] != "inbox unavailable" {
			t.Fatal("failed file read disclosed partial owner metadata", response)
		}
	}
}
