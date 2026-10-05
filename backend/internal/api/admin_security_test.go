package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/pquerna/otp/totp"
)

func adminProofLogin(t *testing.T, env *testEnv, proof map[string]string, want int) (map[string]any, http.Header, string) {
	t.Helper()
	body := map[string]string{"username": "administrator", "password": "administrator test password", "session_type": "web"}
	for key, value := range proof {
		body[key] = value
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", env.url("/api/v1/auth/login"), bytes.NewReader(raw))
	req.Header.Set("Origin", env.server.URL)
	req.Header.Set("Content-Type", "application/json")
	res, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("admin login want%d got%d", want, res.StatusCode)
	}
	var result map[string]any
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	token := ""
	for _, cookie := range res.Cookies() {
		if cookie.Name == "psst_session" && cookie.Value != "" {
			token = cookie.Value
		}
	}
	return result, res.Header, token
}
func enrollSecurityFixture(t *testing.T, env *testEnv) (string, []string) {
	t.Helper()
	begin := authRequest(t, env, "POST", "/admin/security/enrollment", env.authToken, map[string]any{}, 201)
	secret := begin["secret"].(string)
	expires, err := time.Parse(time.RFC3339, begin["expires_at"].(string))
	if err != nil || !strings.HasSuffix(begin["expires_at"].(string), "Z") || time.Until(expires) > 5*time.Minute {
		t.Fatal("invalid enrollment expiry", err)
	}
	if !strings.HasPrefix(begin["otpauth_url"].(string), "otpauth://totp/") {
		t.Fatal("invalid provisioning URL")
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	confirmed := authRequest(t, env, "POST", "/admin/security/enrollment/confirm", env.authToken, map[string]string{"code": code}, 200)
	if confirmed["reauthentication_required"] != true {
		t.Fatal("factor confirmation did not require sign-in")
	}
	raw := confirmed["recovery_codes"].([]any)
	codes := make([]string, len(raw))
	for i, value := range raw {
		codes[i] = value.(string)
	}
	if len(codes) != 10 {
		t.Fatal("recovery count")
	}
	return secret, codes
}
func TestAdministratorFactorLoginNoSessionUntilProof(t *testing.T) {
	env := setupAuthFixture(t, false)
	initial := authRequest(t, env, "GET", "/admin/security", env.authToken, nil, 200)
	if initial["enabled"] != false || initial["recent_until"] == nil {
		t.Fatal(initial)
	}
	secret, _ := enrollSecurityFixture(t, env)
	authRequest(t, env, "GET", "/auth/me", env.authToken, nil, 401)
	missing, headers, token := adminProofLogin(t, env, nil, 401)
	if missing["code"] != "administrator_factor_required" || token != "" || headers.Get("Set-Cookie") != "" {
		t.Fatal("factor challenge issued session")
	}
	bad, _, token := adminProofLogin(t, env, map[string]string{"code": "invalid"}, 401)
	if bad["code"] != "administrator_factor_invalid" || token != "" {
		t.Fatal("bad factor accepted")
	}
	nextCode, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	device, headers, token := adminProofLogin(t, env, map[string]string{"session_type": "device", "code": nextCode}, 403)
	if device["code"] != "admin_transfer_forbidden" || device["token"] != nil || token != "" || headers.Get("Set-Cookie") != "" {
		t.Fatal("native admin session issued")
	}
	result, headers, token := adminProofLogin(t, env, map[string]string{"code": nextCode}, 200)
	if result["token"] != nil || token == "" || !strings.Contains(headers.Get("Set-Cookie"), "HttpOnly") || !strings.Contains(headers.Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatal("web credential contract violated")
	}
	state := authRequest(t, env, "GET", "/admin/security", token, nil, 200)
	if state["enabled"] != true || state["recovery_codes_remaining"] != float64(10) || state["recent_until"] == nil {
		t.Fatal(state)
	}
	replay, _, _ := adminProofLogin(t, env, map[string]string{"code": nextCode}, 401)
	if replay["code"] != "administrator_factor_invalid" {
		t.Fatal(replay)
	}
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatal("security response cacheable")
	}
}
func TestAdministratorRecoveryRegenerationAndDisable(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, codes := enrollSecurityFixture(t, env)
	_, _, token := adminProofLogin(t, env, map[string]string{"recovery_code": codes[0]}, 200)
	state := authRequest(t, env, "GET", "/admin/security", token, nil, 200)
	if state["recovery_codes_remaining"] != float64(9) {
		t.Fatal(state)
	}
	regenerated := authRequest(t, env, "POST", "/admin/security/recovery-codes", token, nil, 200)
	if regenerated["reauthentication_required"] != true {
		t.Fatal("regeneration did not revoke sessions")
	}
	authRequest(t, env, "GET", "/auth/me", token, nil, 401)
	previous, _, _ := adminProofLogin(t, env, map[string]string{"recovery_code": codes[1]}, 401)
	if previous["code"] != "administrator_factor_invalid" {
		t.Fatal(previous)
	}
	newCode := regenerated["recovery_codes"].([]any)[0].(string)
	_, _, fresh := adminProofLogin(t, env, map[string]string{"recovery_code": newCode}, 200)
	authRequest(t, env, "DELETE", "/admin/security/factor", fresh, nil, 204)
	authRequest(t, env, "GET", "/auth/me", fresh, nil, 401)
	_, _, passwordOnly := adminProofLogin(t, env, nil, 200)
	state = authRequest(t, env, "GET", "/admin/security", passwordOnly, nil, 200)
	if state["enabled"] != false || state["recovery_codes_remaining"] != float64(0) {
		t.Fatal(state)
	}
}
func TestAdministratorRecentAuthenticationProtectsHighImpactRoutes(t *testing.T) {
	env := setupAuthFixture(t, false)
	transfer := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	if _, err := env.db.Exec(`UPDATE sessions SET recent_until=0 WHERE user_id='fixture-admin'`); err != nil {
		t.Fatal(err)
	}
	routes := []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/admin/resource-policy", map[string]any{}}, {"PATCH", "/admin/traffic-policy", map[string]any{}}, {"PATCH", "/admin/settings", map[string]int64{"max_file_size": 1 << 20}},
		{"POST", "/admin/users", map[string]string{"username": "new-user", "password": "long enough password"}}, {"PATCH", "/admin/users/fixture-user", map[string]bool{"disabled": true}}, {"POST", "/admin/users/fixture-user/shutdown", nil},
		{"PATCH", "/admin/incident-state", map[string]bool{"public_transfers_paused": true}}, {"PATCH", "/admin/traffic/settings", map[string]any{"allowance_bytes": 100, "cycle_start_day": 1, "basis": "combined"}},
		{"POST", "/auth/password", map[string]string{"current_password": "administrator test password", "password": "new administrator password"}}, {"DELETE", "/auth/sessions/nonexistent", nil},
		{"DELETE", "/transfers/" + transfer, nil}, {"DELETE", "/slots/" + slot, nil}, {"POST", "/admin/security/enrollment", nil}, {"DELETE", "/admin/security/factor", nil}, {"POST", "/admin/security/recovery-codes", nil},
	}
	for _, route := range routes {
		result := authRequest(t, env, route.method, route.path, env.authToken, route.body, 403)
		if result["code"] != "recent_authentication_required" {
			t.Fatal(route.path, result)
		}
	}
	if err := env.queries.SetTransfersPaused(true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/security", "/admin/users", "/admin/resource-policy", "/admin/traffic-policy", "/admin/incident-state", "/auth/me"} {
		authRequest(t, env, "GET", path, env.authToken, nil, 200)
	}
	invalid := authRequest(t, env, "POST", "/admin/security/reauth", env.authToken, map[string]string{"password": "wrong"}, 401)
	if invalid["code"] != "administrator_authentication_invalid" {
		t.Fatal(invalid)
	}
	authRequest(t, env, "GET", "/auth/me", env.authToken, nil, 200)
	fresh := authRequest(t, env, "POST", "/admin/security/reauth", env.authToken, map[string]string{"password": "administrator test password"}, 200)
	if !strings.HasSuffix(fresh["recent_until"].(string), "Z") {
		t.Fatal("recent timestamp not UTC")
	}
	authRequest(t, env, "PATCH", "/admin/incident-state", env.authToken, map[string]bool{"public_transfers_paused": false}, 200)
	user, err := env.queries.UserByID("fixture-user")
	if err != nil || user.Disabled {
		t.Fatal("blocked mutation executed")
	}
	if _, err = env.queries.GetTransfer(transfer); err != nil {
		t.Fatal("blocked deletion executed")
	}
}
func TestAdministratorEnrollmentCancellationAndStaleCookieLogin(t *testing.T) {
	env := setupAuthFixture(t, false)
	authRequest(t, env, "POST", "/admin/security/enrollment", env.authToken, nil, 201)
	if _, err := env.db.Exec(`UPDATE sessions SET recent_until=0 WHERE user_id='fixture-admin'`); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "DELETE", "/admin/security/enrollment", env.authToken, nil, 204)
	body := `{"username":"administrator","password":"administrator test password","session_type":"web"}`
	req, _ := http.NewRequest("POST", env.url("/api/v1/auth/login"), strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "psst_session", Value: env.authToken})
	req.Header.Set("Origin", env.server.URL)
	res, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("stale cookie blocked explicit login", res.StatusCode)
	}
	authRequest(t, env, "POST", "/auth/logout", env.authToken, nil, 204)
	authRequest(t, env, "GET", "/auth/me", env.authToken, nil, 401)
}
func TestAdministratorWrongPasswordDoesNotRevealLockOrRole(t *testing.T) {
	env := setupAuthFixture(t, false)
	unknown, _, _ := adminProofLogin(t, env, map[string]string{"username": "unknown-admin", "password": "wrong"}, 401)
	for range 5 {
		wrong, headers, _ := adminProofLogin(t, env, map[string]string{"password": "wrong"}, 401)
		if wrong["error"] != unknown["error"] || wrong["code"] != nil || headers.Get("X-Psst-Error-Code") != "" {
			t.Fatal("wrong password disclosed administrator state")
		}
	}
	locked, headers, token := adminProofLogin(t, env, nil, 429)
	if locked["code"] != "administrator_authentication_locked" || locked["retry_at"] == nil || headers.Get("Retry-After") == "" || token != "" {
		t.Fatal("persistent lock absent")
	}
}
func TestAdministratorSecurityStrictBodiesAndOwnerAccess(t *testing.T) {
	env := setupAuthFixture(t, false)
	for _, path := range []string{"/admin/security", "/admin/security/enrollment"} {
		method := "GET"
		if strings.HasSuffix(path, "enrollment") {
			method = "POST"
		}
		authRequest(t, env, method, path, env.userToken, nil, 403)
	}
	for _, body := range []string{`{"password":"administrator test password","password":"different"}`, `{"password":null}`, `{"password":"administrator test password"} {}`, `{"password":"administrator test password","extra":true}`, `{"password":"administrator test password","code":"123456","recovery_code":"abc"}`} {
		rawAuthorized(t, env, "POST", "/admin/security/reauth", env.authToken, []byte(body), 400)
	}
	req, _ := http.NewRequest("POST", env.url("/api/v1/admin/security/enrollment"), strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: "psst_session", Value: env.authToken})
	res, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("cookie mutation bypassed same-origin guard")
	}
}

type securityHeldBody struct {
	io.ReadCloser
	entered, release chan struct{}
	first            bool
}

func (b *securityHeldBody) Read(p []byte) (int, error) {
	if !b.first {
		b.first = true
		close(b.entered)
		<-b.release
	}
	return b.ReadCloser.Read(p)
}
func TestAdministratorPasswordMutationRejectsRevokedSessionAfterBodyWait(t *testing.T) {
	env := setupAuthFixture(t, false)
	before, err := env.queries.UserByID("fixture-admin")
	if err != nil {
		t.Fatal(err)
	}
	body := &securityHeldBody{ReadCloser: io.NopCloser(strings.NewReader(`{"current_password":"administrator test password","password":"replacement administrator password"}`)), entered: make(chan struct{}), release: make(chan struct{})}
	router := api.NewServer(config.Config{AuthAllowInsecureHTTP: true, RateLimitGlobal: 1000, RateLimitBurst: 1000}, env.queries, fixtureStore(t, env)).Router()
	request := httptest.NewRequest("POST", "http://example.test/api/v1/auth/password", body)
	request.Header.Set("Authorization", "Bearer "+env.authToken)
	record := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); router.ServeHTTP(record, request) }()
	select {
	case <-body.entered:
	case <-time.After(time.Second):
		t.Fatal("request did not reach body read")
	}
	if err = env.queries.ResetAdminFactor("administrator"); err != nil {
		t.Fatal(err)
	}
	close(body.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("delayed request did not finish")
	}
	if record.Code != 401 || !strings.Contains(record.Body.String(), "administrator_authentication_changed") {
		t.Fatal(record.Code, record.Body.String())
	}
	after, _ := env.queries.UserByID("fixture-admin")
	if !bytes.Equal(before.PasswordHash, after.PasswordHash) {
		t.Fatal("stale session replaced password")
	}
}
