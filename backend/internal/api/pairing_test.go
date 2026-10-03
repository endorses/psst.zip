package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestPairingTrackingIssuerIsolationAndCancellation(t *testing.T) {
	env := setupAuthFixture(t, false)
	user, owner := addAccount(t, env, "alice", "user")
	_, other := addAccount(t, env, "bob", "user")
	secondToken := "second-browser-token"
	hash := sha256.Sum256([]byte(secondToken))
	if err := env.queries.CreateSession(database.Session{ID: "second-browser", UserID: user.ID, DeviceName: "Browser", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, hash[:], user.PasswordHash); err != nil {
		t.Fatal(err)
	}
	pair := authRequest(t, env, "POST", "/auth/pairings", owner, nil, 201)
	path := "/auth/pairings/" + pair["id"].(string)
	status := authRequest(t, env, "GET", path, owner, nil, 200)
	if status["status"] != "pending" {
		t.Fatalf("status %v", status)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), pair["code"].(string)) || strings.Contains(string(raw), "token") || status["code"] != nil {
		t.Fatal("pairing status exposed credential")
	}
	authRequest(t, env, "GET", path, "", nil, 401)
	for _, token := range []string{other, secondToken, env.authToken} {
		authRequest(t, env, "GET", path, token, nil, 404)
		authRequest(t, env, "DELETE", path, token, nil, 404)
		authRequest(t, env, "POST", "/auth/pairings", token, map[string]string{"replace_id": pair["id"].(string)}, 404)
	}
	authRequest(t, env, "DELETE", path, owner, nil, 204)
	authRequest(t, env, "DELETE", path, owner, nil, 204)
	if got := authRequest(t, env, "GET", path, owner, nil, 200)["status"]; got != "canceled" {
		t.Fatalf("status %v", got)
	}
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]any{"code": pair["code"]}, 401)
}

func TestPairingReplacementCompletionAndExpiry(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, owner := addAccount(t, env, "alice", "user")
	old := authRequest(t, env, "POST", "/auth/pairings", owner, nil, 201)
	replacement := authRequest(t, env, "POST", "/auth/pairings", owner, map[string]any{"replace_id": old["id"]}, 201)
	if got := authRequest(t, env, "GET", "/auth/pairings/"+old["id"].(string), owner, nil, 200)["status"]; got != "canceled" {
		t.Fatalf("old status %v", got)
	}
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]any{"code": old["code"]}, 401)
	device := authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]any{"code": replacement["code"], "device_name": "Test phone"}, 200)
	path := "/auth/pairings/" + replacement["id"].(string)
	status := authRequest(t, env, "GET", path, owner, nil, 200)
	if status["status"] != "connected" || status["device_name"] != "Test phone" {
		t.Fatalf("status %v", status)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), device["token"].(string)) {
		t.Fatal("device token leaked")
	}
	authRequest(t, env, "DELETE", path, owner, nil, 409)
	authRequest(t, env, "POST", "/auth/pairings", owner, map[string]any{"replace_id": replacement["id"]}, 409)
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]any{"code": replacement["code"]}, 401)
	authRequest(t, env, "GET", path, device["token"].(string), nil, 404)
	expired := authRequest(t, env, "POST", "/auth/pairings", owner, nil, 201)
	if _, err := env.db.Exec(`UPDATE pairings SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC(), expired["id"]); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.PruneAuthentication(); err != nil {
		t.Fatal(err)
	}
	if got := authRequest(t, env, "GET", "/auth/pairings/"+expired["id"].(string), owner, nil, 200)["status"]; got != "expired" {
		t.Fatalf("expiry %v", got)
	}
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]any{"code": expired["code"]}, 401)
	authRequest(t, env, "POST", "/auth/pairings", owner, map[string]any{"replace_id": expired["id"]}, 201)
	if _, err := env.db.Exec(`UPDATE pairings SET expires_at=? WHERE id=?`, time.Now().Add(-16*time.Minute).UTC(), expired["id"]); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.PruneAuthentication(); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "GET", "/auth/pairings/"+expired["id"].(string), owner, nil, 404)
}

func TestPairingCancelOrReplaceRacesRedemption(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "replace"}[replace], func(t *testing.T) {
			env := setupAuthFixture(t, false)
			_, owner := addAccount(t, env, "alice", "user")
			pair := authRequest(t, env, "POST", "/auth/pairings", owner, nil, 201)
			path := "/auth/pairings/" + pair["id"].(string)
			type result struct {
				kind string
				code int
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			request := func(kind, method, endpoint, token string, body any) {
				data, _ := json.Marshal(body)
				req, _ := http.NewRequest(method, env.url("/api/v1"+endpoint), bytes.NewReader(data))
				req.Header.Set("Content-Type", "application/json")
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				<-start
				resp, err := env.server.Client().Do(req)
				if err != nil {
					results <- result{kind, 0}
					return
				}
				resp.Body.Close()
				results <- result{kind, resp.StatusCode}
			}
			go request("redeem", "POST", "/auth/pairings/redeem", "", map[string]any{"code": pair["code"]})
			if replace {
				go request("revoke", "POST", "/auth/pairings", owner, map[string]any{"replace_id": pair["id"]})
			} else {
				go request("revoke", "DELETE", path, owner, nil)
			}
			close(start)
			codes := map[string]int{}
			for range 2 {
				r := <-results
				codes[r.kind] = r.code
			}
			revokedCode := 204
			if replace {
				revokedCode = 201
			}
			status := authRequest(t, env, "GET", path, owner, nil, 200)["status"]
			switch {
			case codes["redeem"] == 200 && codes["revoke"] == 409:
				if status != "connected" {
					t.Fatalf("status %v", status)
				}
			case codes["redeem"] == 401 && codes["revoke"] == revokedCode:
				if status != "canceled" {
					t.Fatalf("status %v", status)
				}
			default:
				t.Fatalf("non-atomic result %v", codes)
			}
			authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]any{"code": pair["code"]}, 401)
		})
	}
}
