package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func assertLinkError(t *testing.T, env *testEnv, method, route, code string, body io.Reader, status int) {
	t.Helper()
	response := request(t, env, method, env.url("/api/v1"+route), body, status)
	var got map[string]string
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != code || response.Header.Get("X-Psst-Error-Code") != code || got["error"] == "" {
		t.Fatalf("inconsistent link error: header=%q body=%v", response.Header.Get("X-Psst-Error-Code"), got)
	}
}

func TestCreationPolicyErrorsAreStable(t *testing.T) {
	env := setup(t)
	for _, field := range []struct{ route, name string }{{"/transfers", "max_downloads"}, {"/slots", "max_files"}, {"/transfers", "expires_in_seconds"}, {"/slots", "expires_in_seconds"}} {
		bad := []string{"-1", "9223372036854775808", "1.5", `"2"`, "null", "true", "[]"}
		if field.name == "expires_in_seconds" {
			bad = append(bad, "9223372037")
		} else {
			bad = append(bad, "2147483648")
		}
		for _, value := range bad {
			assertLinkError(t, env, "POST", field.route, "invalid_link_policy", strings.NewReader(`{"`+field.name+`":`+value+`}`), 400)
		}
		assertLinkError(t, env, "POST", field.route, "invalid_link_policy", strings.NewReader(`{"`+field.name+`":`), 400)
	}
	var count int
	if err := env.db.QueryRow(`SELECT (SELECT COUNT(*) FROM transfers)+(SELECT COUNT(*) FROM slots)`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid policy created resources", count)
	}
}

func TestExpiredAndRevokedLinksHaveDistinctStableErrors(t *testing.T) {
	env := setup(t)
	for _, status := range []string{"expired", "revoked", "revoked and expired"} {
		slot, child, standalone := uuid.NewString(), uuid.NewString(), uuid.NewString()
		until := time.Now().Add(time.Hour)
		if err := env.queries.CreateReceiveSlot(slot, until, nil, "fixture-user", 2, fixtureRecipientKey, 0); err != nil {
			t.Fatal(err)
		}
		if err := env.queries.CreateSlotTransfer(slot, child, until, 0, nil); err != nil {
			t.Fatal(err)
		}
		if err := env.queries.CreateTransfer(standalone, until, 0, nil, "fixture-user"); err != nil {
			t.Fatal(err)
		}
		code := "link_expired"
		for _, resource := range []struct{ table, id string }{{"slots", slot}, {"transfers", standalone}} {
			statement := "UPDATE " + resource.table + " SET expires_at='2000-01-01' WHERE id=?"
			if status != "expired" {
				statement = "UPDATE " + resource.table + " SET status='revoked' WHERE id=?"
				code = "resource_revoked"
			}
			if _, err := env.db.Exec(statement, resource.id); err != nil {
				t.Fatal(err)
			}
			if status == "revoked and expired" {
				if _, err := env.db.Exec("UPDATE "+resource.table+" SET expires_at='2000-01-01' WHERE id=?", resource.id); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, route := range []struct{ method, path string }{
			{"GET", "/transfers/" + standalone},
			{"GET", "/transfers/" + standalone + "/manifest"},
			{"POST", "/transfers/" + standalone + "/manifest"},
			{"GET", "/slots/" + slot},
			{"GET", "/slots/" + slot + "/inbox"},
			{"GET", "/slots/" + slot + "/availability"},
			{"GET", "/slots/" + slot + "/events"},
			{"POST", "/slots/" + slot + "/transfers"},
			{"GET", "/slots/" + slot + "/transfers/" + child + "/membership"},
		} {
			assertLinkError(t, env, route.method, route.path, code, nil, 410)
		}
	}
}

func TestRangeDownloadConsumesWholeResponseAttempt(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 1)
	target := newUpload(t, env, id, 4)
	patch(t, env, target, "data", 0, http.StatusNoContent, false)
	finish(t, env, id)
	for _, status := range []int{200, 410} {
		req, err := http.NewRequest("GET", target, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Range", "bytes=1-2")
		resp, err := env.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
		if resp.StatusCode != status {
			t.Fatalf("range response: want %d got %d: %s", status, resp.StatusCode, data)
		}
		if status == 200 && (string(data) != "data" || resp.Header.Get("Content-Range") != "") {
			t.Fatal("introduced range replay behavior", resp.Header, string(data))
		}
		if status == 410 && resp.Header.Get("X-Psst-Error-Code") != "download_limit" {
			t.Fatal("wrong exhaustion code", resp.Header, string(data))
		}
	}
	transfer, err := env.queries.GetTransfer(id)
	if err != nil {
		t.Fatal(err)
	}
	if transfer.DownloadCount != 1 || transfer.DownloadedAt.Valid {
		t.Fatal("range attempt was replenished or treated as delivered", transfer)
	}
}
