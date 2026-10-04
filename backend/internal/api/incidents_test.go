package api_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/incidentcli"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestIncidentPausePreservesRecoveryAndBlocksEveryPayloadRoute(t *testing.T) {
	env := setupAuthFixture(t, false)
	ready := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	file := uuid.NewString()
	if err := env.queries.CreateFile(file, ready, 4); err != nil {
		t.Fatal(err)
	}
	if err := fixtureStore(t, env).Save(ready+"/"+file, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	env.queries.UpdateFileOffset(file, 4, true)
	env.queries.SaveManifest(ready, []byte("manifest"))
	env.queries.CompleteTransfer(ready)
	pending := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	upload := uuid.NewString()
	if err := env.queries.CreateFile(upload, pending, 4); err != nil {
		t.Fatal(err)
	}
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	childID, capability := child["id"].(string), child["delete_token"].(string)
	rawAuthorized(t, env, "POST", "/transfers/"+childID+"/manifest", capability, fixtureReceiveEnvelope(), 204)
	authRequest(t, env, "POST", "/transfers/"+childID+"/complete", capability, nil, 204)
	authRequest(t, env, "PATCH", "/admin/incident-state", env.userToken, map[string]bool{"public_transfers_paused": true}, 403)
	for _, bad := range []string{`{}`, `{"public_transfers_paused":null}`, `{"public_transfers_paused":"true"}`, `{"public_transfers_paused":true,"public_transfers_paused":false}`} {
		rawAuthorized(t, env, "PATCH", "/admin/incident-state", env.authToken, []byte(bad), 400)
	}
	authRequest(t, env, "PATCH", "/admin/incident-state", env.authToken, map[string]bool{"public_transfers_paused": true}, 200)
	for _, route := range []struct{ method, path, token string }{
		{"POST", "/transfers", env.userToken}, {"POST", "/slots", env.userToken}, {"POST", "/slots/" + slot + "/transfers", ""},
		{"POST", "/transfers/" + pending + "/files", env.userToken}, {"POST", "/transfers/" + pending + "/manifest", env.userToken},
		{"POST", "/transfers/" + pending + "/complete", env.userToken}, {"PATCH", "/transfers/" + pending + "/files/" + upload, env.userToken},
		{"GET", "/transfers/" + ready + "/manifest", ""}, {"GET", "/transfers/" + ready + "/files/" + file, ""},
		{"GET", "/transfers/" + childID + "/manifest", env.userToken},
	} {
		got := authRequest(t, env, route.method, route.path, route.token, nil, 503)
		if got["code"] != "public_transfers_paused" {
			t.Fatalf("%s %s: %v", route.method, route.path, got)
		}
	}
	for _, route := range []struct{ path, token string }{{"/health", ""}, {"/config", ""}, {"/auth/me", env.userToken}, {"/auth/resources", env.userToken}, {"/admin/incident-state", env.authToken}, {"/admin/resource-policy", env.authToken}, {"/transfers/" + ready, ""}, {"/slots/" + slot, env.userToken}, {"/transfers/" + childID + "/upload-status", capability}} {
		authRequest(t, env, "GET", route.path, route.token, nil, 200)
	}
	cfg := authRequest(t, env, "GET", "/config", "", nil, 200)
	if cfg["public_transfers_paused"] != true {
		t.Fatal(cfg)
	}
	rawAuthorized(t, env, "HEAD", "/transfers/"+pending+"/files/"+upload, env.userToken, nil, 200)
	authRequest(t, env, "DELETE", "/transfers/"+ready, env.userToken, nil, 204)
	authRequest(t, env, "PATCH", "/admin/incident-state", env.authToken, map[string]bool{"public_transfers_paused": false}, 200)
	authRequest(t, env, "GET", "/transfers/"+ready, "", nil, 404)
	rawAuthorized(t, env, "POST", "/transfers/"+pending+"/manifest", env.userToken, []byte("resumed"), 204)
}

func TestIncidentShutdownRevokesCapabilitiesAndCannotRestoreLinks(t *testing.T) {
	env := setupAuthFixture(t, false)
	user, token := addAccount(t, env, "incident-owner", "user")
	standalone := authRequest(t, env, "POST", "/transfers", token, nil, 201)["id"].(string)
	slot := authRequest(t, env, "POST", "/slots", token, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	childID, capability := child["id"].(string), child["delete_token"].(string)
	authRequest(t, env, "POST", "/auth/pairings", token, nil, 201)
	authRequest(t, env, "POST", "/admin/users/"+user.ID+"/shutdown", env.userToken, nil, 403)
	result := authRequest(t, env, "POST", "/admin/users/"+user.ID+"/shutdown", env.authToken, nil, 200)
	if result["revoked_sessions"] != float64(1) || result["revoked_pairings"] != float64(1) || result["revoked_transfers"] != float64(2) || result["revoked_slots"] != float64(1) {
		t.Fatal(result)
	}
	authRequest(t, env, "GET", "/auth/me", token, nil, 401)
	for _, route := range []struct{ method, path, token string }{{"GET", "/transfers/" + standalone, ""}, {"GET", "/slots/" + slot + "/availability", ""}, {"POST", "/transfers/" + childID + "/manifest", capability}, {"HEAD", "/transfers/" + childID + "/files/" + uuid.NewString(), capability}} {
		data := rawAuthorized(t, env, route.method, route.path, route.token, nil, 410)
		if route.method != "HEAD" && !bytes.Contains(data, []byte("resource_revoked")) {
			t.Fatalf("revoked policy missing %s", data)
		}
	}
	authRequest(t, env, "PATCH", "/admin/users/"+user.ID, env.authToken, map[string]bool{"disabled": false}, 200)
	authRequest(t, env, "GET", "/transfers/"+standalone, "", nil, 410)
	rawAuthorized(t, env, "POST", "/transfers/"+childID+"/manifest", capability, fixtureReceiveEnvelope(), 410)
	authRequest(t, env, "GET", "/auth/me", token, nil, 401)
	authRequest(t, env, "DELETE", "/transfers/"+standalone, env.authToken, nil, 204)
	authRequest(t, env, "POST", "/admin/users/fixture-admin/shutdown", env.authToken, nil, 409)
}

type incidentSignalBody struct {
	io.ReadCloser
	entered chan struct{}
	once    *sync.Once
}

func (b *incidentSignalBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	return b.ReadCloser.Read(p)
}
func TestIncidentCLIStopsInFlightManifestAndSurvivesServerRestart(t *testing.T) {
	env := setupAuthFixture(t, false)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	server := api.NewServer(config.Config{DefaultExpiry: time.Hour, AuthAllowInsecureHTTP: true, RateLimitGlobal: 1000, RateLimitBurst: 1000}, env.queries, fixtureStore(t, env))
	entered := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	router := server.Router()
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/manifest") && r.Method == "POST" {
			r.Body = &incidentSignalBody{ReadCloser: r.Body, entered: entered, once: &once}
			defer close(finished)
		}
		router.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunIncidentMonitor(ctx)
	reader, writer := io.Pipe()
	defer writer.Close()
	defer reader.Close()
	request, _ := http.NewRequest("POST", httpServer.URL+"/api/v1/transfers/"+id+"/manifest", reader)
	request.Header.Set("Authorization", "Bearer "+env.userToken)
	done := make(chan error, 1)
	go func() {
		response, err := httpServer.Client().Do(request)
		if response != nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				err = io.ErrUnexpectedEOF
			}
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("manifest did not begin")
	}
	var output bytes.Buffer
	if err := incidentcli.Run(env.dataDir+"/test.db", "pause", &output); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("CLI pause did not cancel blocked manifest")
	}
	// The client transport can still be waiting on its own producer after the
	// server request has stopped; closing that producer completes the client side.
	writer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client did not finish after producer closed")
	}
	manifest, err := env.queries.HasManifest(id)
	if err != nil || manifest {
		t.Fatalf("paused manifest committed %v %v", manifest, err)
	}
	restarted := alternateServer(t, env, fixtureStore(t, env), false)
	authRequest(t, restarted, "POST", "/transfers", env.userToken, nil, 503)
	if err := incidentcli.Run(env.dataDir+"/test.db", "resume", &output); err != nil {
		t.Fatal(err)
	}
	authRequest(t, restarted, "POST", "/transfers", env.userToken, nil, 201)
}

func TestIncidentLoginDisableRemainsDistinctFromLinkShutdown(t *testing.T) {
	env := setupAuthFixture(t, false)
	user, token := addAccount(t, env, "disable-only", "user")
	id := authRequest(t, env, "POST", "/transfers", token, nil, 201)["id"].(string)
	authRequest(t, env, "PATCH", "/admin/users/"+user.ID, env.authToken, map[string]bool{"disabled": true}, 200)
	authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)
	authRequest(t, env, "GET", "/auth/me", token, nil, 401)
	authRequest(t, env, "POST", "/admin/users/"+user.ID+"/shutdown", env.authToken, nil, 200)
	authRequest(t, env, "GET", "/transfers/"+id, "", nil, 410)
}

func TestIncidentPauseAndShutdownCancelBlockedDownloadsBeforeCleanup(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "pause"
		if shutdown {
			name = "shutdown"
		}
		t.Run(name, func(t *testing.T) {
			env := setupAuthFixture(t, false)
			id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
			if shutdown {
				legacySlot := authRequest(t, env, "POST", "/slots", env.userToken, nil, 201)["id"].(string)
				if err := env.queries.LinkSlotTransfer(legacySlot, id); err != nil {
					t.Fatal(err)
				}
				if _, err := env.db.Exec(`UPDATE transfers SET owner_id=NULL WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			}
			file := uuid.NewString()
			size := int64(32 << 20)
			fs := fixtureStore(t, env)
			if err := env.queries.CreateFile(file, id, size); err != nil {
				t.Fatal(err)
			}
			if err := fs.Save(id+"/"+file, io.LimitReader(zeroReader{}, size)); err != nil {
				t.Fatal(err)
			}
			if err := env.queries.UpdateFileOffset(file, size, true); err != nil {
				t.Fatal(err)
			}
			if err := env.queries.CompleteTransfer(id); err != nil {
				t.Fatal(err)
			}
			downloadRequest, _ := http.NewRequest("GET", env.url("/api/v1/transfers/"+id+"/files/"+file), nil)
			downloadRequest.Header.Set("Authorization", "Bearer "+env.userToken)
			response, err := env.server.Client().Do(downloadRequest)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 200 || !store.HasReaders(id) {
				t.Fatal("download was not active")
			}
			if shutdown {
				authRequest(t, env, "POST", "/admin/users/fixture-user/shutdown", env.authToken, nil, 200)
			} else {
				authRequest(t, env, "PATCH", "/admin/incident-state", env.authToken, map[string]bool{"public_transfers_paused": true}, 200)
			}
			deadline := time.Now().Add(2 * time.Second)
			for store.HasReaders(id) && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if store.HasReaders(id) {
				t.Fatal("incident did not interrupt blocked IO")
			}
			usage, err := env.queries.ResourceUsage("")
			if err != nil || usage.ReservedBytes != size {
				t.Fatalf("payload reservation released before cleanup %+v %v", usage, err)
			}
			if !shutdown {
				authRequest(t, env, "GET", "/transfers/"+id, "", nil, 200)
				actual, err := fs.Size(id + "/" + file)
				if err != nil || actual != size {
					t.Fatalf("pause deleted payload %d %v", actual, err)
				}
			} else {
				server := api.NewServer(config.Config{}, env.queries, fs)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go server.RunIncidentMonitor(ctx)
				deadline = time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := env.queries.GetTransfer(id); err != nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if _, err := env.queries.GetTransfer(id); err == nil {
					t.Fatal("incident cleanup did not remove revoked payload")
				}
				actual, err := fs.Size(id + "/" + file)
				if err != nil || actual != 0 {
					t.Fatalf("cleanup left payload %d %v", actual, err)
				}
				usage, err := env.queries.ResourceUsage("")
				if err != nil || usage.ReservedBytes != 0 {
					t.Fatalf("cleanup did not release reservation %+v %v", usage, err)
				}
			}
		})
	}
}

func TestIncidentPauseInterruptsGuestBlobUploadAndAllowsExplicitResume(t *testing.T) {
	env := setupAuthFixture(t, false)
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	id, capability := child["id"].(string), child["delete_token"].(string)
	file := uuid.NewString()
	if err := env.queries.CreateFileWithQuota(file, id, 4, 1024); err != nil {
		t.Fatal(err)
	}
	server := api.NewServer(config.Config{AuthAllowInsecureHTTP: true, RateLimitGlobal: 1000, RateLimitBurst: 1000}, env.queries, fixtureStore(t, env))
	entered, finished := make(chan struct{}), make(chan struct{})
	var once sync.Once
	router := server.Router()
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" && strings.Contains(r.URL.Path, "/files/") {
			r.Body = &incidentSignalBody{ReadCloser: r.Body, entered: entered, once: &once}
			defer close(finished)
		}
		router.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	target := httpServer.URL + "/api/v1/transfers/" + id + "/files/" + file
	request, _ := http.NewRequest("PATCH", target, reader)
	request.ContentLength = 4
	request.Header.Set("Authorization", "Bearer "+capability)
	request.Header.Set("Tus-Resumable", "1.0.0")
	request.Header.Set("Upload-Offset", "0")
	request.Header.Set("Content-Type", "application/offset+octet-stream")
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, _ := httpServer.Client().Do(request)
		if response != nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("guest upload did not begin")
	}
	authRequest(t, env, "PATCH", "/admin/incident-state", env.authToken, map[string]bool{"public_transfers_paused": true}, 200)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("pause did not cancel guest body read")
	}
	writer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client did not stop")
	}
	info, err := env.queries.GetFile(file)
	if err != nil || info.UploadOffset != 0 || info.UploadComplete {
		t.Fatalf("paused upload advanced %+v %v", info, err)
	}
	authRequest(t, env, "PATCH", "/admin/incident-state", env.authToken, map[string]bool{"public_transfers_paused": false}, 200)
	request = patchRequest(env.url("/api/v1/transfers/"+id+"/files/"+file), "data", 0, false)
	request.Header.Set("Authorization", "Bearer "+capability)
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatalf("explicit resumed upload status %d", response.StatusCode)
	}
}
