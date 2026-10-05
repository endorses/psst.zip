package api_test

import (
	"bytes"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func budgetReadyFile(t *testing.T, env *testEnv, size int) (string, string) {
	t.Helper()
	id := authRequest(t, env, "POST", "/transfers", env.userToken, map[string]int{"max_downloads": 10}, 201)["id"].(string)
	file := uuid.NewString()
	if err := env.queries.CreateFile(file, id, int64(size)); err != nil {
		t.Fatal(err)
	}
	if err := fixtureStore(t, env).Save(id+"/"+file, bytes.NewReader(bytes.Repeat([]byte{'x'}, size))); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.UpdateFileOffset(file, int64(size), true); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.SaveManifest(id, []byte("manifest")); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.CompleteTransfer(id); err != nil {
		t.Fatal(err)
	}
	return id, file
}
func enableBudget(t *testing.T, env *testEnv, n int64, basis string) {
	t.Helper()
	authRequest(t, env, "PATCH", "/admin/traffic-policy", env.authToken, map[string]any{"enforcement_enabled": true, "server_budget_bytes": n, "default_account_budget_bytes": n, "basis": basis}, 200)
}
func TestTrafficBudgetBeforeDownloadAttemptAndRecovery(t *testing.T) {
	env := setupAuthFixture(t, false)
	id, file := budgetReadyFile(t, env, 4)
	enableBudget(t, env, 4, "outbound")
	target := "/transfers/" + id + "/files/" + file
	rawAuthorized(t, env, "GET", target, "", nil, 200)
	req, _ := http.NewRequest("GET", env.url("/api/v1"+target), nil)
	res, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 429 || res.Header.Get("X-Psst-Error-Code") != "traffic_budget_exhausted" || res.Header.Get("X-Psst-Retry-At") == "" || res.Header.Get("Retry-After") == "" || !bytes.Contains(data, []byte("retry_at")) {
		t.Fatalf("%d %v %s", res.StatusCode, res.Header, data)
	}
	f, err := env.queries.GetFile(file)
	if err != nil || f.DownloadCount != 1 {
		t.Fatal(f, err)
	}
	rawAuthorized(t, env, "GET", "/transfers/"+id+"/manifest", "", nil, 429)
	probe := authRequest(t, env, "GET", "/transfers/"+id+"/traffic-status", "", nil, 200)
	if probe["state"] != "exhausted" || len(probe) != 2 {
		t.Fatal(probe)
	}
	probe = authRequest(t, env, "GET", "/transfers/"+id+"/traffic-status?direction=upload", "", nil, 200)
	if probe["state"] != "ready" {
		t.Fatal(probe)
	}
	for _, path := range []string{"/health", "/config", "/admin/traffic-policy", "/auth/traffic-usage", "/transfers/" + id} {
		token := env.authToken
		if strings.HasPrefix(path, "/auth/") {
			token = env.userToken
		}
		authRequest(t, env, "GET", path, token, nil, 200)
	}
	authRequest(t, env, "DELETE", "/transfers/"+id, env.userToken, nil, 204)
	snapshot, err := env.queries.TrafficBudgetSnapshot("fixture-user", time.Now())
	if err != nil || snapshot.Usage.ChargedBytes != 4 {
		t.Fatal(snapshot, err)
	}
}
func TestTrafficBudgetPartialResponseAndManifestRetries(t *testing.T) {
	env := setupAuthFixture(t, false)
	id, file := budgetReadyFile(t, env, 128<<10)
	enableBudget(t, env, 70<<10, "outbound")
	res, err := env.server.Client().Get(env.url("/api/v1/transfers/" + id + "/files/" + file))
	if err == nil {
		_, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if readErr == nil {
			t.Fatal("partial response appeared complete")
		}
	}
	snapshot, err := env.queries.TrafficBudgetSnapshot("fixture-user", time.Now())
	if err != nil || snapshot.Usage.ObservedDownloadedBytes != 70<<10 || snapshot.Usage.ReservedDownloadedBytes != 0 {
		t.Fatal(snapshot, err)
	}
	authRequest(t, env, "PATCH", "/admin/traffic-policy", env.authToken, map[string]any{"enforcement_enabled": false}, 200)
	for range 2 {
		rawAuthorized(t, env, "GET", "/transfers/"+id+"/manifest", "", nil, 200)
	}
	snapshot, err = env.queries.TrafficBudgetSnapshot("fixture-user", time.Now())
	if err != nil || snapshot.Usage.ObservedDownloadedBytes != (70<<10)+16 {
		t.Fatal(snapshot, err)
	}
}
func TestTrafficBudgetExactUploadAndGuestOwnerAttribution(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	id, capability := child["id"].(string), child["delete_token"].(string)
	envelope := fixtureReceiveEnvelope()
	enableBudget(t, env, int64(len(envelope)), "combined")
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", capability, envelope, 204)
	snapshot, err := env.queries.TrafficBudgetSnapshot("fixture-user", time.Now())
	if err != nil || snapshot.Usage.ObservedUploadedBytes != int64(len(envelope)) || snapshot.Usage.RemainingBytes != 0 {
		t.Fatal(snapshot, err)
	}
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", capability, envelope, 429)
	for _, path := range []string{"/slots/" + slot + "/traffic-status", "/transfers/" + id + "/traffic-status?direction=upload"} {
		probe := authRequest(t, env, "GET", path, capability, nil, 200)
		if probe["state"] != "exhausted" {
			t.Fatal(probe)
		}
	}
	rawAuthorized(t, env, "GET", "/transfers/"+id+"/traffic-status", "", nil, 401)
	authRequest(t, env, "POST", "/transfers/"+id+"/complete", capability, nil, 204)
	authRequest(t, env, "DELETE", "/transfers/"+id, env.userToken, nil, 204)
	snapshot, err = env.queries.TrafficBudgetSnapshot("fixture-user", time.Now())
	if err != nil || snapshot.Usage.ChargedBytes != int64(len(envelope)) {
		t.Fatal(snapshot, err)
	}
}
func TestTrafficBudgetAccountOverridesAndSafePublicConfig(t *testing.T) {
	env := setupAuthFixture(t, false)
	enableBudget(t, env, 100, "combined")
	path := "/admin/users/fixture-user/traffic-policy"
	authRequest(t, env, "GET", path, env.userToken, nil, 403)
	value := authRequest(t, env, "PATCH", path, env.authToken, map[string]any{"account_budget_bytes": 10}, 200)
	if value["effective_budget_bytes"] != float64(10) {
		t.Fatal(value)
	}
	own := authRequest(t, env, "GET", "/auth/traffic-usage", env.userToken, nil, 200)
	if own["usage"].(map[string]any)["budget_bytes"] != float64(10) {
		t.Fatal(own)
	}
	for _, body := range []string{`{}`, `{"account_budget_bytes":0}`, `{"account_budget_bytes":101}`, `{"account_budget_bytes":null,"account_budget_bytes":5}`, `{"unknown":4}`} {
		rawAuthorized(t, env, "PATCH", path, env.authToken, []byte(body), 400)
	}
	value = authRequest(t, env, "PATCH", path, env.authToken, map[string]any{"account_budget_bytes": nil}, 200)
	if value["account_budget_bytes"] != nil || value["effective_budget_bytes"] != float64(100) {
		t.Fatal(value)
	}
	cfg := authRequest(t, env, "GET", "/config", "", nil, 200)
	policy := cfg["traffic_policy"].(map[string]any)
	if policy["enforcement_enabled"] != true || policy["usage"] != nil || cfg["usage"] != nil {
		t.Fatal(cfg)
	}
}
func TestTrafficBudgetDisabledLoginPreservesPublicDownload(t *testing.T) {
	env := setupAuthFixture(t, false)
	id, file := budgetReadyFile(t, env, 4)
	enableBudget(t, env, 100, "outbound")
	authRequest(t, env, "PATCH", "/admin/users/fixture-user", env.authToken, map[string]bool{"disabled": true}, 200)
	rawAuthorized(t, env, "GET", "/transfers/"+id+"/files/"+file, "", nil, 200)
	probe := authRequest(t, env, "GET", "/transfers/"+id+"/traffic-status", "", nil, 200)
	if probe["state"] != "ready" {
		t.Fatal(probe)
	}
}
func TestTrafficBudgetAccountingFailureFailsClosed(t *testing.T) {
	env := setupAuthFixture(t, false)
	id, file := budgetReadyFile(t, env, 4)
	if _, err := env.db.Exec(`DROP TABLE traffic_leases`); err != nil {
		t.Fatal(err)
	}
	body := rawAuthorized(t, env, "GET", "/transfers/"+id+"/files/"+file, "", nil, 503)
	if !bytes.Contains(body, []byte("traffic_accounting_unavailable")) {
		t.Fatal(string(body))
	}
	f, err := env.queries.GetFile(file)
	if err != nil || f.DownloadCount != 0 {
		t.Fatal(f, err)
	}
	authRequest(t, env, "GET", "/health", "", nil, 200)
}

func TestTrafficBudgetPartialTusRetainsCheckpointAndManualResume(t *testing.T) {
	env := setupAuthFixture(t, false)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	file := uuid.NewString()
	if err := env.queries.CreateFile(file, id, 200); err != nil {
		t.Fatal(err)
	}
	enableBudget(t, env, 100, "combined")
	target := env.url("/api/v1/transfers/" + id + "/files/" + file)
	req := patchRequest(target, strings.Repeat("a", 200), 0, false)
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	res, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 429 || res.Header.Get("X-Psst-Error-Code") != "traffic_budget_exhausted" {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	f, err := env.queries.GetFile(file)
	if err != nil || f.UploadOffset != 100 || f.UploadComplete {
		t.Fatal(f, err)
	}
	head, _ := http.NewRequest("HEAD", target, nil)
	head.Header.Set("Authorization", "Bearer "+env.userToken)
	head.Header.Set("Tus-Resumable", "1.0.0")
	res, err = env.server.Client().Do(head)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Upload-Offset") != "100" {
		t.Fatal(res.StatusCode, res.Header)
	}
	enableBudget(t, env, 200, "combined")
	req = patchRequest(target, strings.Repeat("a", 100), 100, false)
	req.Header.Set("Authorization", "Bearer "+env.userToken)
	res, err = env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	f, err = env.queries.GetFile(file)
	if err != nil || f.UploadOffset != 200 || !f.UploadComplete {
		t.Fatal(f, err)
	}
	usage, err := env.queries.TrafficBudgetSnapshot("fixture-user", time.Now())
	if err != nil || usage.Usage.ObservedUploadedBytes != 200 || usage.Usage.ReservedUploadedBytes != 0 {
		t.Fatal(usage, err)
	}
}
func TestTrafficBudgetSettlementFailureStopsFurtherPayloads(t *testing.T) {
	env := setupAuthFixture(t, false)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	if _, err := env.db.Exec(`CREATE TRIGGER reject_settle BEFORE INSERT ON traffic_owner_days BEGIN SELECT RAISE(ABORT,'injected settlement failure'); END`); err != nil {
		t.Fatal(err)
	}
	body := rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", env.userToken, []byte("opaque"), 503)
	if !bytes.Contains(body, []byte("traffic_accounting_unavailable")) {
		t.Fatal(string(body))
	}
	snapshot, err := env.queries.TrafficBudgetSnapshot("fixture-user", time.Now())
	if err != nil || snapshot.Usage.ReservedUploadedBytes != 65536 || snapshot.Usage.ObservedUploadedBytes != 0 {
		t.Fatal(snapshot, err)
	}
	if _, err = env.db.Exec(`DROP TRIGGER reject_settle`); err != nil {
		t.Fatal(err)
	}
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", env.userToken, []byte("opaque"), 503)
	state := authRequest(t, env, "GET", "/auth/traffic-usage", env.userToken, nil, 200)
	if state["state"] != "unavailable" {
		t.Fatal(state)
	}
	authRequest(t, env, "DELETE", "/transfers/"+id, env.userToken, nil, 204)
}
