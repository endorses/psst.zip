package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func rawAuthorized(t *testing.T, env *testEnv, method, path, token string, body []byte, status int) []byte {
	t.Helper()
	request, _ := http.NewRequest(method, env.url("/api/v1"+path), bytes.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("%s %s expected %d got %d: %s", method, path, status, response.StatusCode, data)
	}
	return data
}
func TestPrivateInboxRejectsOtherSendersAndAllowsOwner(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, owner := addAccount(t, env, "inbox-owner", "user")
	_, other := addAccount(t, env, "other-user", "user")
	slot := authRequest(t, env, "POST", "/slots", owner, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	id := child["id"].(string)
	capability := child["delete_token"].(string)
	second := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	otherCapability := second["delete_token"].(string)
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", capability, fixtureReceiveEnvelope(), 204)
	// A complete, downloadable blob gives the access checks a valid target.
	file := uuid.NewString()
	if err := env.queries.CreateFileWithQuota(file, id, 4, 1024); err != nil {
		t.Fatal(err)
	}
	if err := fixtureStore(t, env).Save(id+"/"+file, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.UpdateFileOffset(file, 4, true); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "POST", "/transfers/"+id+"/complete", capability, nil, 204)
	paths := []string{"/slots/" + slot, "/slots/" + slot + "/events", "/transfers/" + id, "/transfers/" + id + "/manifest", "/transfers/" + id + "/files/" + file}
	for _, token := range []string{"", capability, otherCapability, other, env.authToken} {
		status := 403
		if token == "" || token == capability || token == otherCapability {
			status = 401
		}
		for _, path := range paths {
			rawAuthorized(t, env, "GET", path, token, nil, status)
		}
		rawAuthorized(t, env, "POST", "/transfers/"+id+"/downloaded", token, nil, status)
	}
	for _, path := range []string{paths[0], paths[2], paths[3], paths[4]} {
		rawAuthorized(t, env, "GET", path, owner, nil, 200)
	}
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/downloaded", owner, nil, 204)
	availability := rawAuthorized(t, env, "GET", "/slots/"+slot+"/availability", "", nil, 200)
	if bytes.Contains(availability, []byte(id)) || bytes.Contains(availability, []byte(`"transfers":`)) || bytes.Contains(availability, []byte("reserved_files")) {
		t.Fatalf("availability leaked inbox: %s", availability)
	}
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", capability, fixtureReceiveEnvelope(), 409)
}
func TestLegacyInboxRemainsOwnerReadableButSubmissionClosed(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, nil, 201)["id"].(string)
	id := uuid.NewString()
	if err := env.queries.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil, "fixture-user"); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.LinkSlotTransfer(slot, id); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.SaveManifest(id, []byte("legacy encrypted bytes")); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.CompleteTransfer(id); err != nil {
		t.Fatal(err)
	}
	rawAuthorized(t, env, "GET", "/transfers/"+id+"/manifest", env.userToken, nil, 200)
	rawAuthorized(t, env, "GET", "/transfers/"+id+"/manifest", "", nil, 401)
	result := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 403)
	if result["code"] != "legacy_receive_disabled" {
		t.Fatal(result)
	}
	authRequest(t, env, "POST", "/transfers/"+id+"/files", env.userToken, nil, 403)
	availability := authRequest(t, env, "GET", "/slots/"+slot+"/availability", "", nil, 200)
	if availability["available"] != false || availability["receive_protocol"] != float64(1) {
		t.Fatal(availability)
	}
}
func TestReceiveManifestVersionAndSizeBound(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	id := child["id"].(string)
	token := child["delete_token"].(string)
	for _, body := range [][]byte{[]byte("legacy"), fixtureReceiveEnvelope()[:115], append([]byte("WRONGHDR"), make([]byte, 108)...)} {
		rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", token, body, 400)
	}
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", token, append([]byte("PSSTRCV2"), make([]byte, 1048576)...), 413)
	authRequest(t, env, "POST", "/transfers/"+id+"/complete", token, nil, 400)
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", token, fixtureReceiveEnvelope(), 204)
	authRequest(t, env, "POST", "/transfers/"+id+"/complete", token, nil, 204)
}
func TestPerLinkPolicyValidationAndFileCounters(t *testing.T) {
	env := setupAuthFixture(t, false)
	for _, bad := range []string{"-1", "2147483648", "1.5", `"2"`, "null"} {
		rawAuthorized(t, env, "POST", "/transfers", env.userToken, []byte(`{"max_downloads":`+bad+`}`), 400)
		rawAuthorized(t, env, "POST", "/slots", env.userToken, []byte(`{"max_files":`+bad+`}`), 400)
	}
	for _, good := range []int{0, 1, 2147483647} {
		authRequest(t, env, "POST", "/transfers", env.userToken, map[string]int{"max_downloads": good}, 201)
	}
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", map[string]int{"max_downloads": 1}, 400)
	for _, key := range []string{"", fixtureRecipientKey + "=", "bad", strings.Repeat("A", 43)} {
		authRequest(t, env, "POST", "/slots", env.userToken, map[string]any{"receive_protocol": 2, "recipient_public_key": key}, 400)
	}
	id := authRequest(t, env, "POST", "/transfers", env.userToken, map[string]int{"max_downloads": 2}, 201)["id"].(string)
	for i := 0; i < 2; i++ {
		file := fmt.Sprint("file-", i)
		if err := env.queries.CreateFile(file, id, 4); err != nil {
			t.Fatal(err)
		}
		if err := env.queries.UpdateFileOffset(file, 4, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.queries.CompleteTransfer(id); err != nil {
		t.Fatal(err)
	}
	if _, err := env.queries.ReserveFileDownload(id, "file-0"); err != nil {
		t.Fatal(err)
	}
	response := authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)
	encoded, _ := json.Marshal(response["files"])
	if !bytes.Contains(encoded, []byte(`"remaining_downloads":1`)) || !bytes.Contains(encoded, []byte(`"remaining_downloads":2`)) {
		t.Fatalf("file counters: %s", encoded)
	}
}

func TestUploadStatusCapabilityIsScopedAndMinimal(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	id := child["id"].(string)
	token := child["delete_token"].(string)
	other := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	for _, bad := range []string{"", env.userToken, env.authToken, other["delete_token"].(string)} {
		authRequest(t, env, "GET", "/transfers/"+id+"/upload-status", bad, nil, 403)
	}
	status := authRequest(t, env, "GET", "/transfers/"+id+"/upload-status", token, nil, 200)
	if len(status) != 2 || status["id"] != id || status["status"] != "pending" {
		t.Fatalf("nonminimal status: %v", status)
	}
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", token, fixtureReceiveEnvelope(), 204)
	authRequest(t, env, "POST", "/transfers/"+id+"/complete", token, nil, 204)
	status = authRequest(t, env, "GET", "/transfers/"+id+"/upload-status", token, nil, 200)
	if status["status"] != "complete" {
		t.Fatal("cannot resolve completed upload", status)
	}
	authRequest(t, env, "GET", "/transfers/"+id, token, nil, 401)
}

func TestReceiveAllowanceExhaustionStillAllowsAllocatedUploadAndOwnerRead(t *testing.T) {
	env := setupAuthFixture(t, false)
	policy := fixtureSlotPolicy()
	policy["max_files"] = 1
	slot := authRequest(t, env, "POST", "/slots", env.userToken, policy, 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	base := "/transfers/" + child["id"].(string)
	token := child["delete_token"].(string)
	headers := map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": "4"}
	_, created := policyRequest(t, env, "bearer", token, "POST", base+"/files", "", headers, 201)
	target := strings.TrimPrefix(created.Get("Location"), "/api/v1")
	data, _ := policyRequest(t, env, "bearer", token, "POST", base+"/files", "", headers, 403)
	if !bytes.Contains(data, []byte(`"code":"receive_file_limit"`)) {
		t.Fatalf("wrong allowance error: %s", data)
	}
	authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 403)
	policyRequest(t, env, "bearer", token, "HEAD", target, "", nil, 200)
	headers["Upload-Offset"] = "0"
	headers["Content-Type"] = "application/offset+octet-stream"
	policyRequest(t, env, "bearer", token, "PATCH", target, "data", headers, 204)
	rawAuthorized(t, env, "POST", base+"/manifest", token, fixtureReceiveEnvelope(), 204)
	authRequest(t, env, "POST", base+"/complete", token, nil, 204)
	owner := authRequest(t, env, "GET", "/slots/"+slot, env.userToken, nil, 200)
	for _, field := range []string{"max_files", "reserved_files", "completed_files"} {
		if owner[field] != float64(1) {
			t.Fatalf("owner %s: %v", field, owner)
		}
	}
	if owner["remaining_files"] != float64(0) {
		t.Fatal(owner)
	}
	rawAuthorized(t, env, "GET", target, env.userToken, nil, 200)
	availability := authRequest(t, env, "GET", "/slots/"+slot+"/availability", "", nil, 200)
	if availability["available"] != false || availability["remaining_files"] != float64(0) {
		t.Fatal(availability)
	}
}

func TestPublicAvailabilityHidesActivityAndOwnerCountersFailClosed(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	if _, err := env.db.Exec(`UPDATE slots SET status='has_uploads' WHERE id=?`, slot); err != nil {
		t.Fatal(err)
	}
	public := authRequest(t, env, "GET", "/slots/"+slot+"/availability", "", nil, 200)
	if public["status"] != "waiting" {
		t.Fatalf("public availability exposed activity: %v", public)
	}
	owner := authRequest(t, env, "GET", "/slots/"+slot, env.userToken, nil, 200)
	if owner["status"] != "has_uploads" {
		t.Fatal("owner lost actual state", owner)
	}
	// An empty inbox now reads maintained totals without scanning payload rows.
	// Account history also reads maintained totals without scanning payload rows.
	if _, err := env.db.Exec(`DROP TABLE files`); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "GET", "/slots/"+slot, env.userToken, nil, 200)
	authRequest(t, env, "GET", "/slots/"+slot+"/inbox", env.userToken, nil, 200)
	authRequest(t, env, "GET", "/auth/resources", env.userToken, nil, 200)
	// Damage the actual owner-summary dependency, rather than requiring an
	// unrelated schema probe on each empty page. Neither route may invent zero
	// totals or serialize partial slot/history data after a failed counter read.
	if _, err := env.db.Exec(`DROP TABLE admin_resource_totals`); err != nil {
		t.Fatal(err)
	}
	history := authRequest(t, env, "GET", "/auth/resources", env.userToken, nil, 503)
	if len(history) != 1 || history["error"] != "history unavailable" {
		t.Fatal("failed history counter read disclosed partial metadata", history)
	}
	for _, suffix := range []string{"", "/inbox"} {
		response := authRequest(t, env, "GET", "/slots/"+slot+suffix, env.userToken, nil, 503)
		if len(response) != 1 || response["error"] != "inbox unavailable" {
			t.Fatal("failed summary read disclosed partial owner metadata", response)
		}
	}
}
