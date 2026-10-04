package api_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func ownedTransfer(t *testing.T, env *testEnv, endpoint string) api.CreateTransferResponse {
	t.Helper()
	response := request(t, env, http.MethodPost, endpoint, nil, http.StatusCreated)
	var result api.CreateTransferResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(result.DeleteToken)
	if err != nil || len(raw) != 32 {
		t.Fatalf("invalid deletion token: %v", err)
	}
	return result
}

func ownedSlot(t *testing.T, env *testEnv) api.CreateSlotResponse {
	t.Helper()
	response := request(t, env, http.MethodPost, env.url("/api/v1/slots"), nil, http.StatusCreated)
	var result api.CreateSlotResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(result.DeleteToken)
	if err != nil || len(raw) != 32 {
		t.Fatalf("invalid slot deletion token: %v", err)
	}
	return result
}

func deleteRequest(target, token string) *http.Request {
	req, _ := http.NewRequest(http.MethodDelete, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func deleteResource(t *testing.T, env *testEnv, target, token string, status int) {
	t.Helper()
	response, err := env.server.Client().Do(deleteRequest(target, token))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("DELETE want %d, got %d: %s", status, response.StatusCode, body)
	}
}

func alternateServer(t *testing.T, env *testEnv, fs store.FileStore, legacy bool) *testEnv {
	t.Helper()
	cfg := config.Config{
		DefaultExpiry: time.Hour, MaxFileSize: 1024 * 1024, RateLimitGlobal: 1000,
		RateLimitBurst: 2000, RateLimitCreation: 1000, RateLimitCreationBurst: 2000,
		AllowLegacyDeletion:   legacy,
		AuthAllowInsecureHTTP: true,
	}
	server := httptest.NewServer(authenticatedFixture(api.NewServer(cfg, env.queries, fs).Router(), env.userToken))
	t.Cleanup(server.Close)
	result := *env
	result.server = server
	return &result
}

func fixtureStore(t *testing.T, env *testEnv) *store.DiskStore {
	t.Helper()
	fs, err := store.NewDiskStore(env.dataDir + "/files")
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestTransferDeletionRequiresOwnerTokenAndDoesNotLeakIt(t *testing.T) {
	env := setup(t)
	owned := ownedTransfer(t, env, env.url("/api/v1/transfers"))
	target := env.url("/api/v1/transfers/" + owned.ID)
	row, err := env.queries.GetTransfer(owned.ID)
	hash := sha256.Sum256([]byte(owned.DeleteToken))
	if err != nil || !bytes.Equal(row.DeleteTokenHash, hash[:]) {
		t.Fatalf("expected only token hash: %+v %v", row, err)
	}
	response := request(t, env, http.MethodGet, target, nil, http.StatusOK)
	body, _ := io.ReadAll(response.Body)
	if strings.Contains(string(body), "delete_token") || strings.Contains(string(body), owned.DeleteToken) {
		t.Fatal("GET leaked deletion credential")
	}
	file := newUpload(t, env, owned.ID, 4)
	patch(t, env, file, "pa", 0, http.StatusNoContent, false)
	deleteResource(t, env, target, "", http.StatusForbidden)
	deleteResource(t, env, target, "wrong", http.StatusForbidden)
	deleteResource(t, env, target, owned.DeleteToken, http.StatusNoContent)
	for _, endpoint := range []string{target, target + "/manifest", file} {
		request(t, env, http.MethodGet, endpoint, nil, http.StatusNotFound)
	}
	request(t, env, http.MethodHead, file, nil, http.StatusNotFound)
	patch(t, env, file, "rt", 2, http.StatusNotFound, false)
	deleteResource(t, env, target, owned.DeleteToken, http.StatusNotFound)
	deleteResource(t, env, env.url("/api/v1/transfers/not-a-uuid"), "", http.StatusBadRequest)
	if _, err := os.Stat(env.dataDir + "/files/" + owned.ID); !os.IsNotExist(err) {
		t.Fatalf("deleted transfer payload remains: %v", err)
	}
}

func TestSlotDeletionRevokesAllChildrenAndClosesEvents(t *testing.T) {
	env := setup(t)
	slot := ownedSlot(t, env)
	target := env.url("/api/v1/slots/" + slot.ID)
	row, err := env.queries.GetSlot(slot.ID)
	hash := sha256.Sum256([]byte(slot.DeleteToken))
	if err != nil || !bytes.Equal(row.DeleteTokenHash, hash[:]) {
		t.Fatalf("slot hash not stored: %+v %v", row, err)
	}
	first := ownedTransfer(t, env, target+"/transfers")
	second := ownedTransfer(t, env, target+"/transfers")
	file := newUpload(t, env, first.ID, 1)
	patch(t, env, file, "x", 0, http.StatusNoContent, false)
	finish(t, env, first.ID)
	partial := newUpload(t, env, second.ID, 4)
	patch(t, env, partial, "pa", 0, http.StatusNoContent, false)
	response := request(t, env, http.MethodGet, target, nil, http.StatusOK)
	body, _ := io.ReadAll(response.Body)
	for _, secret := range []string{"delete_token", slot.DeleteToken, first.DeleteToken, second.DeleteToken} {
		if strings.Contains(string(body), secret) {
			t.Fatal("slot GET leaked deletion credentials")
		}
	}
	deleteResource(t, env, target, first.DeleteToken, http.StatusForbidden)
	deleteResource(t, env, env.url("/api/v1/transfers/"+first.ID), slot.DeleteToken, http.StatusForbidden)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target+"/events", nil)
	events, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	scanner := bufio.NewScanner(events.Body)
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}
	deleteResource(t, env, target, slot.DeleteToken, http.StatusNoContent)
	for scanner.Scan() {
	}
	if scanner.Err() != nil || ctx.Err() != nil {
		t.Fatalf("SSE failed to close on deletion: %v %v", scanner.Err(), ctx.Err())
	}
	request(t, env, http.MethodGet, target, nil, http.StatusNotFound)
	request(t, env, http.MethodGet, target+"/events", nil, http.StatusNotFound)
	request(t, env, http.MethodPost, target+"/transfers", nil, http.StatusNotFound)
	for _, child := range []api.CreateTransferResponse{first, second} {
		request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+child.ID), nil, http.StatusNotFound)
		if _, err := os.Stat(env.dataDir + "/files/" + child.ID); !os.IsNotExist(err) {
			t.Fatalf("child payload remains: %v", err)
		}
	}
}

func TestLegacyDeletionRequiresExplicitConfiguration(t *testing.T) {
	env := setup(t)
	fs := fixtureStore(t, env)
	enabled := alternateServer(t, env, fs, true)
	for _, kind := range []string{"transfers", "slots"} {
		id := uuid.NewString()
		var err error
		if kind == "transfers" {
			err = env.queries.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil)
		} else {
			err = env.queries.CreateSlot(id, time.Now().Add(time.Hour), nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		deleteResource(t, env, env.url("/api/v1/"+kind+"/"+id), "", http.StatusForbidden)
		deleteResource(t, enabled, enabled.url("/api/v1/"+kind+"/"+id), "", http.StatusNoContent)
	}
	protected := ownedTransfer(t, enabled, enabled.url("/api/v1/transfers"))
	deleteResource(t, enabled, enabled.url("/api/v1/transfers/"+protected.ID), "", http.StatusForbidden)
}

type failingDeleteStore struct {
	store.FileStore
	fail atomic.Bool
}

func (s *failingDeleteStore) DeleteAll(prefix string) error {
	if s.fail.Load() {
		return errors.New("simulated disk cleanup failure")
	}
	return s.FileStore.DeleteAll(prefix)
}

func TestFailedDeletionKeepsRevocationAndTokenUntilCleanupRetry(t *testing.T) {
	env := setup(t)
	fs := &failingDeleteStore{FileStore: fixtureStore(t, env)}
	fs.fail.Store(true)
	broken := alternateServer(t, env, fs, false)
	slot := ownedSlot(t, broken)
	slotURL := broken.url("/api/v1/slots/" + slot.ID)
	child := ownedTransfer(t, broken, slotURL+"/transfers")
	file := newUpload(t, broken, child.ID, 1)
	patch(t, broken, file, "x", 0, http.StatusNoContent, false)
	finish(t, broken, child.ID)
	deleteResource(t, broken, slotURL, slot.DeleteToken, http.StatusServiceUnavailable)
	savedSlot, err := env.queries.GetSlot(slot.ID)
	if err != nil || savedSlot.Status != "revoked" || len(savedSlot.DeleteTokenHash) != 32 {
		t.Fatalf("lost retryable slot: %+v %v", savedSlot, err)
	}
	savedChild, err := env.queries.GetTransfer(child.ID)
	if err != nil || savedChild.Status != "revoked" || len(savedChild.DeleteTokenHash) != 32 {
		t.Fatalf("lost retryable child: %+v %v", savedChild, err)
	}
	base := broken.url("/api/v1/transfers/" + child.ID)
	for _, target := range []string{slotURL, slotURL + "/events", base, base + "/manifest", file} {
		request(t, broken, http.MethodGet, target, nil, http.StatusGone)
	}
	for _, endpoint := range []string{"/files", "/manifest", "/complete", "/downloaded"} {
		request(t, broken, http.MethodPost, base+endpoint, nil, http.StatusGone)
	}
	request(t, broken, http.MethodHead, file, nil, http.StatusGone)
	patch(t, broken, file, "x", 0, http.StatusGone, false)
	request(t, broken, http.MethodPost, slotURL+"/transfers", nil, http.StatusGone)
	deleteResource(t, broken, slotURL, "", http.StatusForbidden)
	deleteResource(t, broken, slotURL, slot.DeleteToken, http.StatusServiceUnavailable)
	// The ordinary cleanup worker uses the same removal path and retries tombstones.
	fs.fail.Store(false)
	sweep(t, env)
	request(t, env, http.MethodGet, env.url("/api/v1/slots/"+slot.ID), nil, http.StatusNotFound)
	request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+child.ID), nil, http.StatusNotFound)
	if _, err := os.Stat(env.dataDir + "/files/" + child.ID); !os.IsNotExist(err) {
		t.Fatalf("cleanup failed: %v", err)
	}
}

type blockingUploadStore struct {
	store.FileStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockingUploadStore) SaveAt(key string, reader io.Reader, offset int64) (int64, error) {
	close(s.entered)
	<-s.release
	return s.FileStore.SaveAt(key, reader, offset)
}

func TestDeletionWaitsForActiveUploadAcrossServerInstances(t *testing.T) {
	env := setup(t)
	fs := fixtureStore(t, env)
	blocked := &blockingUploadStore{FileStore: fs, entered: make(chan struct{}), release: make(chan struct{})}
	uploader := alternateServer(t, env, blocked, false)
	owner := ownedTransfer(t, env, env.url("/api/v1/transfers"))
	uploadURL := newUpload(t, uploader, owner.ID, 4)
	uploadDone := make(chan int, 1)
	go func() {
		response, err := uploader.server.Client().Do(patchRequest(uploadURL, "data", 0, false))
		if err != nil {
			uploadDone <- 0
			return
		}
		response.Body.Close()
		uploadDone <- response.StatusCode
	}()
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not enter storage")
	}
	deletionDone := make(chan int, 1)
	go func() {
		response, err := env.server.Client().Do(deleteRequest(env.url("/api/v1/transfers/"+owner.ID), owner.DeleteToken))
		if err != nil {
			deletionDone <- 0
			return
		}
		response.Body.Close()
		deletionDone <- response.StatusCode
	}()
	select {
	case status := <-deletionDone:
		close(blocked.release)
		t.Fatalf("deletion raced active upload: %d", status)
	case <-time.After(30 * time.Millisecond):
	}
	close(blocked.release)
	if status := <-uploadDone; status != http.StatusNoContent {
		t.Fatalf("upload status %d", status)
	}
	if status := <-deletionDone; status != http.StatusNoContent {
		t.Fatalf("delete status %d", status)
	}
	if _, err := os.Stat(env.dataDir + "/files/" + owner.ID + "/" + path.Base(uploadURL)); !os.IsNotExist(err) {
		t.Fatalf("orphan upload remains: %v", err)
	}
	request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+owner.ID), nil, http.StatusNotFound)
}

func TestConcurrentSlotCreationAndDeletionLeavesNoChildren(t *testing.T) {
	env := setup(t)
	slot := ownedSlot(t, env)
	target := env.url("/api/v1/slots/" + slot.ID)
	start := make(chan struct{})
	statuses := make(chan int, 12)
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			response, err := env.server.Client().Post(target+"/transfers", "", nil)
			if err != nil {
				statuses <- 0
				return
			}
			response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	close(start)
	deleteResource(t, env, target, slot.DeleteToken, http.StatusNoContent)
	group.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusCreated && status != http.StatusNotFound && status != http.StatusGone {
			t.Fatalf("racing create: %d", status)
		}
	}
	var count int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM transfers").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("slot deletion left %d orphan children", count)
	}
}

func TestDeletionPreflightAllowsAuthorization(t *testing.T) {
	env := setup(t)
	req, _ := http.NewRequest(http.MethodOptions, env.url("/api/v1/transfers/00000000-0000-0000-0000-000000000000"), nil)
	req.Header.Set("Origin", "https://web.example")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	response, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent || !strings.Contains(response.Header.Get("Access-Control-Allow-Methods"), "DELETE") || !strings.Contains(response.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("deletion preflight failed: %d %v", response.StatusCode, response.Header)
	}
}
