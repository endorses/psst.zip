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
	"github.com/endorses/psst.zip/backend/internal/store"
)

func TestStreamAdmissionAttributesGuestsAndDownloadsToOwner(t *testing.T) {
	env := setupAuthFixture(t, false)
	_, bob := addAccount(t, env, "admission-bob", "user")
	slot := authRequest(t, env, "POST", "/slots", env.userToken, fixtureSlotPolicy(), 201)["id"].(string)
	child := authRequest(t, env, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
	childID := child["id"].(string)
	capability := child["delete_token"].(string)
	alice := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	bobTransfer := authRequest(t, env, "POST", "/transfers", bob, nil, 201)["id"].(string)
	completed := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	rawAuthorized(t, env, "POST", "/transfers/"+completed+"/manifest", env.userToken, []byte("opaque"), 204)
	authRequest(t, env, "POST", "/transfers/"+completed+"/complete", env.userToken, nil, 204)
	file := uuid.NewString()
	if err := env.queries.CreateFileWithQuota(file, childID, 4, 1024); err != nil {
		t.Fatal(err)
	}
	blocked := &blockingUploadStore{FileStore: fixtureStore(t, env), entered: make(chan struct{}), release: make(chan struct{})}
	cfg := config.Config{AuthAllowInsecureHTTP: true, RateLimitGlobal: 1000, RateLimitBurst: 2000, MaxStreamsPerAccount: 1, MaxStreamsPerIP: 10, MaxStreamsPerTransfer: 10, MaxStreamsPerSlot: 10}
	// Persisted stream policy is authoritative after the environment seed.
	policy, err := env.queries.TrafficPolicy()
	if err != nil {
		t.Fatal(err)
	}
	policy.MaxStreamsPerAccount = 1
	policy.MaxStreamsPerIP = 10
	policy.MaxStreamsPerTransfer = 10
	policy.MaxStreamsPerSlot = 10
	if err = env.queries.SetTrafficPolicy(policy); err != nil {
		t.Fatal(err)
	}
	limited := httptest.NewServer(api.NewServer(cfg, env.queries, blocked).Router())
	t.Cleanup(limited.Close)
	var once sync.Once
	unblock := func() { once.Do(func() { close(blocked.release) }) }
	t.Cleanup(unblock)
	target := limited.URL + "/api/v1/transfers/" + childID + "/files/" + file
	request := patchRequest(target, "data", 0, false)
	request.Header.Set("Authorization", "Bearer "+capability)
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := limited.Client().Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		t.Fatal("upload did not start")
	}
	send := func(method, path, token string, body io.Reader, want int) {
		t.Helper()
		req, _ := http.NewRequest(method, limited.URL+"/api/v1"+path, body)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := limited.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != want {
			data, _ := io.ReadAll(response.Body)
			t.Fatalf("%s want%d got%d: %s", path, want, response.StatusCode, data)
		}
	}
	send("POST", "/transfers/"+alice+"/manifest", env.userToken, strings.NewReader("manifest"), 429)
	send("GET", "/transfers/"+completed+"/manifest", "", nil, 429)
	send("POST", "/transfers/"+bobTransfer+"/manifest", bob, strings.NewReader("manifest"), 204)
	send("GET", "/admin/overview", env.authToken, nil, 200)
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("upload did not finish")
	}
	send("POST", "/transfers/"+alice+"/manifest", env.userToken, strings.NewReader("manifest"), 204)
}

type gatedReadCloser struct {
	io.ReadCloser
	once             sync.Once
	entered, release chan struct{}
}

func (r *gatedReadCloser) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.ReadCloser.Read(p)
}

type gatedDownloadStore struct {
	store.FileStore
	entered, release chan struct{}
}

func (s *gatedDownloadStore) Load(key string) (io.ReadCloser, error) {
	reader, err := s.FileStore.Load(key)
	if err != nil {
		return nil, err
	}
	return &gatedReadCloser{ReadCloser: reader, entered: s.entered, release: s.release}, nil
}
func TestExhaustedCleanupRetainsPayloadUntilFinalReaderCloses(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 1)
	file := newUpload(t, env, id, 4)
	patch(t, env, file, "data", 0, 204, false)
	finish(t, env, id)
	gated := &gatedDownloadStore{FileStore: fixtureStore(t, env), entered: make(chan struct{}), release: make(chan struct{})}
	server := alternateServer(t, env, gated, false)
	var once sync.Once
	unblock := func() { once.Do(func() { close(gated.release) }) }
	t.Cleanup(unblock)
	target := strings.Replace(file, env.server.URL, server.server.URL, 1)
	done := make(chan error, 1)
	go func() {
		response, err := server.server.Client().Get(target)
		if err == nil {
			defer func() { _ = response.Body.Close() }()
			data, readErr := io.ReadAll(response.Body)
			if readErr != nil {
				err = readErr
			} else if !bytes.Equal(data, []byte("data")) {
				err = io.ErrUnexpectedEOF
			}
		}
		done <- err
	}()
	select {
	case <-gated.entered:
	case <-time.After(time.Second):
		t.Fatal("download did not begin")
	}
	sweep(t, env)
	if !store.HasReaders(id) {
		t.Fatal("reader lease missing")
	}
	before, err := env.queries.ResourceUsage("")
	if err != nil || before.ReservedBytes != int64(4+len("encrypted manifest")) {
		t.Fatalf("active reader reservation lost: %+v %v", before, err)
	}
	files, err := env.queries.ListFiles(id)
	if err != nil || len(files) != 1 {
		t.Fatal(err)
	}
	if size, err := fixtureStore(t, env).Size(id + "/" + files[0].ID); err != nil || size != 4 {
		t.Fatalf("cleanup unlinked active payload: %d %v", size, err)
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("final download stalled")
	}
	sweepAfterRetryDue(t, env, "transfer", id)
	if size, err := fixtureStore(t, env).Size(id + "/" + files[0].ID); err != nil || size != 0 {
		t.Fatalf("cleanup did not release closed payload: %d %v", size, err)
	}
	after, err := env.queries.ResourceUsage("")
	if err != nil || after.ReservedBytes != int64(len("encrypted manifest")) || after.OccupiedBytes != after.ReservedBytes {
		t.Fatalf("closed reader reservation not released: %+v %v", after, err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestRevocationCancelsBlockedDownloadBeforeDeletingPayload(t *testing.T) {
	env := setupAuthFixture(t, false)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	file := uuid.NewString()
	size := int64(32 * 1024 * 1024)
	if err := env.queries.CreateFile(file, id, size); err != nil {
		t.Fatal(err)
	}
	if err := fixtureStore(t, env).Save(id+"/"+file, io.LimitReader(zeroReader{}, size)); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.UpdateFileOffset(file, size, true); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.CompleteTransfer(id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", env.url("/api/v1/transfers/"+id+"/files/"+file), nil)
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 || !store.HasReaders(id) {
		t.Fatal("download was not active")
	}
	started := time.Now()
	authRequest(t, env, "DELETE", "/transfers/"+id, env.userToken, nil, 204)
	if time.Since(started) > 2*time.Second {
		t.Fatal("revocation waited for idle timeout instead of interrupting IO")
	}
	if store.HasReaders(id) {
		t.Fatal("deletion returned with reader still open")
	}
	if size, err := fixtureStore(t, env).Size(id + "/" + file); err != nil || size != 0 {
		t.Fatalf("payload not removed: %d %v", size, err)
	}
}
