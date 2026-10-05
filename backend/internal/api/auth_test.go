package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"golang.org/x/crypto/bcrypt"
)

func authRequest(t *testing.T, env *testEnv, method, path, token string, body any, status int) map[string]any {
	t.Helper()
	var reader io.Reader
	if raw, ok := body.([]byte); ok {
		reader = bytes.NewReader(raw)
	} else if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, env.url("/api/v1"+path), reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("%s %s want%d got%d: %s", method, path, status, resp.StatusCode, data)
	}
	var out map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("invalid response: %s", data)
		}
	}
	return out
}
func addAccount(t *testing.T, env *testEnv, name, role string) (database.User, string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u := database.User{ID: name, Username: name, Role: role, PasswordHash: hash}
	if err := env.queries.CreateUser(u, false); err != nil {
		t.Fatal(err)
	}
	if role == "admin" {
		return u, adminWebLoginToken(t, env, name, "correct horse battery")
	}

	token := name + "-session"
	h := sha256.Sum256([]byte(token))
	if err := env.queries.CreateSession(database.Session{ID: name + "-session-id", UserID: u.ID, DeviceName: "Phone", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, h[:], hash); err != nil {
		t.Fatal(err)
	}
	return u, token
}
func TestAuthCreationOwnershipAndPublicDownloads(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, alice := addAccount(t, env, "alice", "user")
	_, bob := addAccount(t, env, "bob", "user")
	for _, endpoint := range []string{"/transfers", "/slots"} {
		authRequest(t, env, "POST", endpoint, "", nil, 401)
	}
	created := authRequest(t, env, "POST", "/transfers", alice, nil, 201)
	id := created["id"].(string)
	capability := created["delete_token"].(string)
	authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)
	for _, token := range []string{"", bob, capability} {
		authRequest(t, env, "POST", "/transfers/"+id+"/manifest", token, "encrypted", 403)
	}
	authRequest(t, env, "POST", "/transfers/"+id+"/manifest", alice, "encrypted", 204)
	authRequest(t, env, "POST", "/transfers/"+id+"/complete", alice, nil, 204)
	authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)
	own := authRequest(t, env, "GET", "/auth/resources", alice, nil, 200)
	other := authRequest(t, env, "GET", "/auth/resources", bob, nil, 200)
	if len(own["transfers"].([]any)) != 1 || len(other["transfers"].([]any)) != 0 {
		t.Fatal("resources crossed account boundaries")
	}
	authRequest(t, env, "DELETE", "/transfers/"+id, bob, nil, 403)
	authRequest(t, env, "DELETE", "/transfers/"+id, alice, nil, 204)
}
func TestAuthPublicSlotCapabilityScopedAndDisabledOwner(t *testing.T) {
	env := setupAuthFixture(t, false)
	u, owner := addAccount(t, env, "alice", "user")
	slot := authRequest(t, env, "POST", "/slots", owner, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	id := child["id"].(string)
	token := child["delete_token"].(string)
	authRequest(t, env, "POST", "/transfers/"+id+"/manifest", "", "encrypted", 403)
	authRequest(t, env, "POST", "/transfers/"+id+"/manifest", token, fixtureReceiveEnvelope(), 204)
	authRequest(t, env, "POST", "/transfers", token, nil, 401)
	child2 := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)["id"].(string)
	authRequest(t, env, "POST", "/transfers/"+child2+"/manifest", token, "encrypted", 403)
	authRequest(t, env, "PATCH", "/admin/users/"+u.ID, env.authToken, map[string]any{"disabled": true}, 200)
	authRequest(t, env, "POST", "/slots", owner, nil, 401)
	authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 403)
	authRequest(t, env, "POST", "/transfers/"+id+"/manifest", token, "encrypted", 403)
	authRequest(t, env, "GET", "/slots/"+slot, "", nil, 401)
}
func TestAuthLoginAndCookieOrigin(t *testing.T) {
	env := setupAuthFixture(t, false)
	addAccount(t, env, "alice", "user")
	body := map[string]any{"username": "alice", "password": "correct horse battery", "session_type": "device"}
	response := authRequest(t, env, "POST", "/auth/login", "", body, 200)
	token := response["token"].(string)
	authRequest(t, env, "GET", "/auth/me", token, nil, 200)
	body["session_type"] = "web"
	authRequest(t, env, "POST", "/auth/login", "", body, 403)
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", env.url("/api/v1/auth/login"), bytes.NewReader(raw))
	req.Header.Set("Origin", env.server.URL)
	resp, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["token"]; ok {
		t.Fatal("cookie login leaked token")
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	for _, origin := range []string{"", "https://evil.example", env.server.URL} {
		req, _ := http.NewRequest("POST", env.url("/api/v1/transfers"), nil)
		req.AddCookie(cookies[0])
		req.Header.Set("Origin", origin)
		resp, err := env.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		want := 403
		if origin == env.server.URL {
			want = 201
		}
		if resp.StatusCode != want {
			t.Fatalf("origin %q got%d want%d", origin, resp.StatusCode, want)
		}
	}
	authRequest(t, env, "POST", "/auth/logout", token, nil, 204)
	authRequest(t, env, "GET", "/auth/me", token, nil, 401)
}
func TestAuthPairingSingleUseAndSessionRevocation(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, owner := addAccount(t, env, "alice", "user")
	pairing := authRequest(t, env, "POST", "/auth/pairings", owner, nil, 201)
	code := pairing["code"].(string)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]string{"code": code, "device_name": "Android"})
			resp, err := env.server.Client().Post(env.url("/api/v1/auth/pairings/redeem"), "application/json", bytes.NewReader(raw))
			if err != nil {
				statuses <- 0
				return
			}
			defer func() { _ = resp.Body.Close() }()
			statuses <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	success, denied := 0, 0
	for status := range statuses {
		if status == 200 {
			success++
		}
		if status == 401 {
			denied++
		}
	}
	if success != 1 || denied != 1 {
		t.Fatalf("pairing redeemed %d times, denied %d", success, denied)
	}
	sessions := authRequest(t, env, "GET", "/auth/sessions", owner, nil, 200)["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("sessions: %v", sessions)
	}
	pair2 := authRequest(t, env, "POST", "/auth/pairings", owner, nil, 201)["code"].(string)
	authRequest(t, env, "DELETE", "/auth/sessions/alice-session-id", owner, nil, 204)
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]string{"code": pair2}, 401)
}
func TestAuthAccountManagementResetAndLastAdmin(t *testing.T) {
	env := setupAuthFixture(t, false)
	u, user := addAccount(t, env, "alice", "user")
	authRequest(t, env, "GET", "/admin/users", user, nil, 403)
	authRequest(t, env, "POST", "/admin/users", env.authToken, map[string]any{"username": "newuser", "password": "short"}, 400)
	created := authRequest(t, env, "POST", "/admin/users", env.authToken, map[string]any{"username": "newuser", "password": "long enough password"}, 201)
	encoded, _ := json.Marshal(created)
	if strings.Contains(string(encoded), "hash") || strings.Contains(string(encoded), "long enough password") {
		t.Fatal("password leaked")
	}
	pair := authRequest(t, env, "POST", "/auth/pairings", user, nil, 201)["code"].(string)
	authRequest(t, env, "PATCH", "/admin/users/"+u.ID, env.authToken, map[string]string{"password": "new password is long"}, 200)
	authRequest(t, env, "GET", "/auth/me", user, nil, 401)
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]string{"code": pair}, 401)
	authRequest(t, env, "PATCH", "/admin/users/fixture-admin", env.authToken, map[string]bool{"disabled": true}, 409)
}
func TestAuthPairingExpiryAndSecureDefault(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, user := addAccount(t, env, "alice", "user")
	h := sha256.Sum256([]byte("expired"))
	if err := env.queries.CreatePairing(h[:], "alice", "alice-session-id", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]string{"code": "expired"}, 401)
	cfg := config.Config{RateLimitGlobal: 1000, RateLimitBurst: 1000}
	router := api.NewServer(cfg, env.queries, fixtureStore(t, env)).Router()
	req := httptest.NewRequest("GET", "http://example.test/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+user)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("HTTP accepted %d", rec.Code)
	}
}
func TestAuthSlotLimits(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, owner := addAccount(t, env, "alice", "user")
	authRequest(t, env, "POST", "/slots", owner, map[string]int{"expires_in_seconds": 604801}, 400)
	slot := authRequest(t, env, "POST", "/slots", owner, fixtureSlotPolicy(), 201)["id"].(string)
	for range 20 {
		authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	}
	authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 403)
}

func TestAuthBootstrapRequiresOperatorCredentialsAndDoesNotReset(t *testing.T) {
	env := setupAuthFixture(t, false)
	if _, err := env.db.Exec(`DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{}
	if err := api.NewServer(cfg, env.queries, fixtureStore(t, env)).BootstrapAdmin(); err == nil {
		t.Fatal("empty bootstrap accepted")
	}
	cfg.AdminUsername = "admin"
	cfg.AdminPassword = "initial admin password"
	if err := api.NewServer(cfg, env.queries, fixtureStore(t, env)).BootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	original, err := env.queries.UserByName("admin")
	if err != nil {
		t.Fatal(err)
	}
	cfg.AdminPassword = "replacement password"
	if err := api.NewServer(cfg, env.queries, fixtureStore(t, env)).BootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	current, err := env.queries.UserByName("admin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original.PasswordHash, current.PasswordHash) {
		t.Fatal("startup reset administrator password")
	}
	cfg.PublicURL = "https://example.test/unsafe/path"
	if err := api.NewServer(cfg, env.queries, fixtureStore(t, env)).BootstrapAdmin(); err == nil {
		t.Fatal("invalid canonical origin accepted")
	}
}
func TestAuthAdminAllResourcesExplicit(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, user := addAccount(t, env, "alice", "user")
	authRequest(t, env, "POST", "/transfers", user, nil, 201)
	authRequest(t, env, "GET", "/auth/resources", env.authToken, nil, 403)
	all := authRequest(t, env, "GET", "/auth/resources?all=true", env.authToken, nil, 200)
	if len(all["transfers"].([]any)) != 1 {
		t.Fatal("admin listing scope incorrect")
	}
	authRequest(t, env, "GET", "/auth/resources?all=true", user, nil, 403)
}

func adminWebLoginToken(t *testing.T, env *testEnv, username, password string) string {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"username": username, "password": password, "session_type": "web"})
	req, _ := http.NewRequest("POST", env.url("/api/v1/auth/login"), bytes.NewReader(data))
	req.Header.Set("Origin", env.server.URL)
	req.Header.Set("Content-Type", "application/json")
	res, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("web admin login: %d %s", res.StatusCode, body)
	}
	for _, cookie := range res.Cookies() {
		if cookie.Name == "psst_session" && cookie.Value != "" {
			return cookie.Value
		}
	}
	t.Fatal("web login missing cookie")
	return ""
}
