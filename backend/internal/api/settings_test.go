package api_test

import (
	"bytes"
	"github.com/endorses/psst.zip/backend/internal/database"
	"io"
	"net/http"
	"strconv"
	"testing"
)

func TestAdminFileLimitAuthorizationPersistenceAndEnforcement(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, user := addAccount(t, env, "member", "user")
	defaults := authRequest(t, env, "GET", "/config", "", nil, 200)
	if defaults["max_file_size"] != float64(25*1024*1024) {
		t.Fatal(defaults)
	}
	for _, item := range []struct {
		token string
		code  int
	}{{"", 401}, {user, 403}} {
		authRequest(t, env, "PATCH", "/admin/settings", item.token, map[string]any{"max_file_size": 64 * 1024 * 1024}, item.code)
	}
	for _, value := range []any{0, -1, 1.5, "large", 101 * 1024 * 1024} {
		authRequest(t, env, "PATCH", "/admin/settings", env.authToken, map[string]any{"max_file_size": value}, 400)
	}
	for _, limit := range []int64{64 * 1024 * 1024, 1024 * 1024} {
		authRequest(t, env, "PATCH", "/admin/settings", env.authToken, map[string]any{"max_file_size": limit}, 200)
		public := authRequest(t, env, "GET", "/config", "", nil, 200)
		if public["max_file_size"] != float64(limit) {
			t.Fatal(public)
		}
		// A fresh database connection verifies persisted policy, not an in-memory flag.
		db, err := database.Open(env.dataDir + "/test.db")
		if err != nil {
			t.Fatal(err)
		}
		stored, err := database.NewQueries(db).MaxFileSize()
		_ = db.Close()
		if err != nil || stored != limit {
			t.Fatalf("stored=%d error=%v", stored, err)
		}
		transfer := authRequest(t, env, "POST", "/transfers", user, nil, 201)["id"].(string)
		wire := limit + ((limit+4*1024*1024-1)/(4*1024*1024))*60
		for _, delta := range []int64{0, 1} {
			req, _ := http.NewRequest("POST", env.url("/api/v1/transfers/"+transfer+"/files"), nil)
			req.Header.Set("Authorization", "Bearer "+user)
			req.Header.Set("Tus-Resumable", "1.0.0")
			req.Header.Set("Upload-Length", strconv.FormatInt(wire+delta, 10))
			res, err := env.server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			want := 201
			if delta == 1 {
				want = 413
			}
			if res.StatusCode != want {
				t.Fatalf("length %d got%d want%d", wire+delta, res.StatusCode, want)
			}
		}
	}
}

func TestLoweringLimitKeepsReservedUploadAndDownload(t *testing.T) {
	env := setupAuthFixture(t, false)
	authRequest(t, env, "PATCH", "/admin/settings", env.authToken, map[string]any{"max_file_size": 2 * 1024 * 1024}, 200)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	wire := int64(2*1024*1024 + 60)
	create, _ := http.NewRequest("POST", env.url("/api/v1/transfers/"+id+"/files"), nil)
	create.Header.Set("Authorization", "Bearer "+env.userToken)
	create.Header.Set("Tus-Resumable", "1.0.0")
	create.Header.Set("Upload-Length", strconv.FormatInt(wire, 10))
	response, err := env.server.Client().Do(create)
	if err != nil {
		t.Fatal(err)
	}
	location := response.Header.Get("Location")
	_ = response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatal(response.StatusCode)
	}
	authRequest(t, env, "PATCH", "/admin/settings", env.authToken, map[string]any{"max_file_size": 1024 * 1024}, 200)
	body := bytes.Repeat([]byte{0x43}, int(wire))
	patch, _ := http.NewRequest("PATCH", env.url(location), bytes.NewReader(body))
	patch.Header.Set("Authorization", "Bearer "+env.userToken)
	patch.Header.Set("Tus-Resumable", "1.0.0")
	patch.Header.Set("Upload-Offset", "0")
	patch.Header.Set("Content-Type", "application/offset+octet-stream")
	response, err = env.server.Client().Do(patch)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	authRequest(t, env, "POST", "/transfers/"+id+"/manifest", env.userToken, "encrypted manifest", 204)
	authRequest(t, env, "POST", "/transfers/"+id+"/complete", env.userToken, nil, 204)
	response, err = env.server.Client().Get(env.url(location))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	received, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || !bytes.Equal(body, received) {
		t.Fatalf("reserved upload/download failed: status%d err%v", response.StatusCode, err)
	}
}
