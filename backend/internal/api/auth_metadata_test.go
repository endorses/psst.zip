package api_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestAuthMetadataHTTPPairingCapacityAndReplacement(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, token := addAccount(t, env, "bounded", "user")
	first := ""
	for i := 0; i < database.MaxAuthPendingPairingsPerUser; i++ {
		pair := authRequest(t, env, "POST", "/auth/pairings", token, nil, 201)
		if i == 0 {
			first = pair["id"].(string)
		}
	}
	failure := authRequest(t, env, "POST", "/auth/pairings", token, nil, 429)
	if failure["code"] != "pairing_capacity" || failure["code"] == nil {
		t.Fatal(failure)
	}
	authRequest(t, env, "POST", "/auth/pairings", token, map[string]string{"replace_id": first}, 201)
}
func TestAuthMetadataHTTPBoundedLegacySessionView(t *testing.T) {
	env := setupAuthFixture(t, false)
	u, token := addAccount(t, env, "bounded", "user")
	me := authRequest(t, env, "GET", "/auth/me", token, nil, 200)
	current := me["session_id"].(string)
	tx, err := env.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	for i := 0; i < 80; i++ {
		id := fmt.Sprint("legacy-session-", i)
		if _, err = tx.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES(?,?,?,?,?,?)`, id, u.ID, []byte(id), "Browser", now.Add(time.Duration(i)*time.Millisecond), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	response := authRequest(t, env, "GET", "/auth/sessions", token, nil, 200)
	if response["sessions_limited"] != true || response["total_active_sessions"] != float64(33) || response["total_active_sessions_exact"] != false || response["session_limit"] != float64(32) {
		t.Fatal(response)
	}
	list := response["sessions"].([]any)
	if len(list) != 32 {
		t.Fatal("unbounded sessions", len(list))
	}
	found := false
	for _, raw := range list {
		s := raw.(map[string]any)
		if s["id"] == current && s["current"] == true {
			found = true
		}
		if s["token"] != nil || s["token_hash"] != nil {
			t.Fatal("session credential leaked")
		}
	}
	if !found {
		t.Fatal("current omitted")
	}
}
func TestAuthMetadataHTTPAccountCapacity(t *testing.T) {
	env := setupAuthFixture(t, false)
	count, err := env.queries.UserCount()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := env.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := count; i < database.MaxAuthUsers; i++ {
		id := fmt.Sprint("full-", i)
		if _, err = tx.Exec(`INSERT INTO users(id,username,role,password_hash,disabled) VALUES(?,?,'user',?,1)`, id, id, []byte("hash")); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	response := authRequest(t, env, "POST", "/admin/users", env.authToken, map[string]string{"username": "overflow", "password": "a-valid-password"}, 409)
	if response["code"] != "account_capacity" {
		t.Fatal(response)
	}
}
