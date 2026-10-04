package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/cleanup"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

type testEnv struct {
	server    *httptest.Server
	db        *sql.DB
	queries   *database.Queries
	dataDir   string
	authToken string
	userToken string
}

func setup(t *testing.T) *testEnv { return setupAuthFixture(t, true) }

func setupAuthFixture(t *testing.T, authorized bool) *testEnv {
	t.Helper()

	dir := t.TempDir()
	dbPath := dir + "/test.db"
	storagePath := dir + "/files"

	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	fs, err := store.NewDiskStore(storagePath)
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}

	cfg := config.Config{
		MaxStreamsPerAccount: 64, MaxStreamsPerIP: 64, MaxStreamsPerTransfer: 64, MaxStreamsPerSlot: 64,
		ListenAddr:             ":0",
		AuthAllowInsecureHTTP:  true,
		StoragePath:            storagePath,
		DBPath:                 dbPath,
		MaxFileSize:            100 * 1024 * 1024,
		DefaultExpiry:          24 * time.Hour,
		CORSOrigin:             "*",
		RateLimitGlobal:        1000, // High limits for tests
		RateLimitCreation:      1000,
		RateLimitBurst:         2000,
		RateLimitCreationBurst: 2000,
		MaxManifestSize:        10 * 1024 * 1024,
		MaxFilesPerTransfer:    100,
	}

	queries := database.NewQueries(db)
	u := database.User{ID: "fixture-user", Username: "fixture", Role: "user", PasswordHash: []byte("fixture-hash")}
	if err := queries.CreateUser(u, false); err != nil {
		t.Fatal(err)
	}
	token := "fixture-session-token"
	hash := sha256.Sum256([]byte(token))
	if err := queries.CreateSession(database.Session{ID: "fixture-session", UserID: u.ID, DeviceName: "Tests", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, hash[:], u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	admin := database.User{ID: "fixture-admin", Username: "administrator", Role: "admin", PasswordHash: []byte("fixture-admin-hash")}
	if err := queries.CreateUser(admin, false); err != nil {
		t.Fatal(err)
	}
	adminToken := "fixture-admin-token"
	adminHash := sha256.Sum256([]byte(adminToken))
	if err := queries.CreateSession(database.Session{ID: "admin-session", UserID: admin.ID, DeviceName: "Admin tests", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, adminHash[:], admin.PasswordHash); err != nil {
		t.Fatal(err)
	}
	srv := api.NewServer(cfg, queries, fs)
	handler := srv.Router()
	if authorized {
		handler = authenticatedFixture(handler, token)
	}
	ts := httptest.NewServer(handler)

	t.Cleanup(func() {
		ts.Close()
		db.Close()
	})

	return &testEnv{server: ts, db: db, queries: queries, dataDir: dir, authToken: adminToken, userToken: token}
}

func (e *testEnv) url(path string) string {
	return e.server.URL + path
}

// --- Transfer flow tests ---

func TestCreateTransfer(t *testing.T) {
	env := setup(t)

	resp, err := http.Post(env.url("/api/v1/transfers"), "application/json",
		strings.NewReader(`{"expires_in_seconds": 3600}`))
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var result api.CreateTransferResponse
	json.NewDecoder(resp.Body).Decode(&result)

	if result.ID == "" {
		t.Fatal("expected non-empty transfer ID")
	}
	if result.ExpiresAt.IsZero() {
		t.Fatal("expected non-zero expiry")
	}
}

func TestCreateTransferNoBody(t *testing.T) {
	env := setup(t)

	resp, err := http.Post(env.url("/api/v1/transfers"), "", nil)
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestGetTransfer(t *testing.T) {
	env := setup(t)

	// Create a transfer.
	resp, _ := http.Post(env.url("/api/v1/transfers"), "application/json", nil)
	var created api.CreateTransferResponse
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Get it.
	resp, err := http.Get(env.url("/api/v1/transfers/" + created.ID))
	if err != nil {
		t.Fatalf("get transfer: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var tr api.TransferResponse
	json.NewDecoder(resp.Body).Decode(&tr)
	if tr.ID != created.ID {
		t.Fatalf("expected ID %s, got %s", created.ID, tr.ID)
	}
	if tr.Status != "pending" {
		t.Fatalf("expected status pending, got %s", tr.Status)
	}
}

func TestGetTransferNotFound(t *testing.T) {
	env := setup(t)

	// Use a valid UUID that doesn't exist.
	resp, err := http.Get(env.url("/api/v1/transfers/00000000-0000-0000-0000-000000000000"))
	if err != nil {
		t.Fatalf("get transfer: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetTransferInvalidID(t *testing.T) {
	env := setup(t)

	resp, err := http.Get(env.url("/api/v1/transfers/not-a-uuid"))
	if err != nil {
		t.Fatalf("get transfer: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestFullTransferFlow(t *testing.T) {
	env := setup(t)
	client := env.server.Client()

	// 1. Create transfer.
	resp, _ := http.Post(env.url("/api/v1/transfers"), "application/json", nil)
	var created api.CreateTransferResponse
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	transferID := created.ID

	// 2. Create a file upload via tus.
	fileData := []byte("hello encrypted world")
	tusCreateReq, _ := http.NewRequest("POST", env.url("/api/v1/transfers/"+transferID+"/files"), nil)
	tusCreateReq.Header.Set("Tus-Resumable", "1.0.0")
	tusCreateReq.Header.Set("Upload-Length", fmt.Sprintf("%d", len(fileData)))
	resp, err := client.Do(tusCreateReq)
	if err != nil {
		t.Fatalf("tus create: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}
	fileID := path.Base(resp.Header.Get("Location"))
	resp.Body.Close()
	if fileID == "" {
		t.Fatal("expected file ID in Location header")
	}

	// 3. Upload the file data via tus PATCH.
	patchReq, _ := http.NewRequest("PATCH",
		env.url("/api/v1/transfers/"+transferID+"/files/"+fileID),
		bytes.NewReader(fileData))
	patchReq.Header.Set("Tus-Resumable", "1.0.0")
	patchReq.Header.Set("Upload-Offset", "0")
	patchReq.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, err = client.Do(patchReq)
	if err != nil {
		t.Fatalf("tus patch: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// 4. Upload manifest.
	manifest := []byte(`{"encrypted": "manifest data"}`)
	resp, err = http.Post(env.url("/api/v1/transfers/"+transferID+"/manifest"),
		"application/octet-stream", bytes.NewReader(manifest))
	if err != nil {
		t.Fatalf("upload manifest: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// 5. Complete transfer.
	resp, err = http.Post(env.url("/api/v1/transfers/"+transferID+"/complete"), "", nil)
	if err != nil {
		t.Fatalf("complete transfer: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// 6. Get transfer — should be complete.
	resp, _ = http.Get(env.url("/api/v1/transfers/" + transferID))
	var tr api.TransferResponse
	json.NewDecoder(resp.Body).Decode(&tr)
	resp.Body.Close()
	if tr.Status != "complete" {
		t.Fatalf("expected status complete, got %s", tr.Status)
	}
	if tr.FileCount != 1 {
		t.Fatalf("expected 1 file, got %d", tr.FileCount)
	}
	if !tr.HasManifest {
		t.Fatal("expected has_manifest to be true")
	}

	// 7. Download the file.
	resp, err = http.Get(env.url("/api/v1/transfers/" + transferID + "/files/" + fileID))
	if err != nil {
		t.Fatalf("download file: %v", err)
	}
	downloaded, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(downloaded) != string(fileData) {
		t.Fatalf("downloaded data mismatch: got %q", string(downloaded))
	}

	// 8. Download the manifest.
	resp, err = http.Get(env.url("/api/v1/transfers/" + transferID + "/manifest"))
	if err != nil {
		t.Fatalf("download manifest: %v", err)
	}
	manifestData, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(manifestData) != string(manifest) {
		t.Fatalf("manifest data mismatch: got %q", string(manifestData))
	}
}

func TestTusResumeUpload(t *testing.T) {
	env := setup(t)
	client := env.server.Client()

	// Create transfer.
	resp, _ := http.Post(env.url("/api/v1/transfers"), "application/json", nil)
	var created api.CreateTransferResponse
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	transferID := created.ID

	// Full data.
	fullData := []byte("abcdefghij0123456789")

	// Create tus upload.
	tusReq, _ := http.NewRequest("POST", env.url("/api/v1/transfers/"+transferID+"/files"), nil)
	tusReq.Header.Set("Tus-Resumable", "1.0.0")
	tusReq.Header.Set("Upload-Length", fmt.Sprintf("%d", len(fullData)))
	resp, _ = client.Do(tusReq)
	fileID := path.Base(resp.Header.Get("Location"))
	resp.Body.Close()

	// Upload first 10 bytes.
	chunk1 := fullData[:10]
	patchReq, _ := http.NewRequest("PATCH",
		env.url("/api/v1/transfers/"+transferID+"/files/"+fileID),
		bytes.NewReader(chunk1))
	patchReq.Header.Set("Tus-Resumable", "1.0.0")
	patchReq.Header.Set("Upload-Offset", "0")
	patchReq.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, _ = client.Do(patchReq)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("first patch: expected 204, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Upload-Offset") != "10" {
		t.Fatalf("expected offset 10, got %s", resp.Header.Get("Upload-Offset"))
	}
	resp.Body.Close()

	// Check offset via HEAD.
	headReq, _ := http.NewRequest("HEAD",
		env.url("/api/v1/transfers/"+transferID+"/files/"+fileID), nil)
	resp, _ = client.Do(headReq)
	if resp.Header.Get("Upload-Offset") != "10" {
		t.Fatalf("HEAD offset expected 10, got %s", resp.Header.Get("Upload-Offset"))
	}
	resp.Body.Close()

	// Upload remaining bytes.
	chunk2 := fullData[10:]
	patchReq2, _ := http.NewRequest("PATCH",
		env.url("/api/v1/transfers/"+transferID+"/files/"+fileID),
		bytes.NewReader(chunk2))
	patchReq2.Header.Set("Tus-Resumable", "1.0.0")
	patchReq2.Header.Set("Upload-Offset", "10")
	patchReq2.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, _ = client.Do(patchReq2)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second patch: expected 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Complete and download.
	resp, _ = http.Post(env.url("/api/v1/transfers/"+transferID+"/complete"), "", nil)
	resp.Body.Close()

	resp, _ = http.Get(env.url("/api/v1/transfers/" + transferID + "/files/" + fileID))
	downloaded, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(downloaded) != string(fullData) {
		t.Fatalf("resumed upload data mismatch: got %q, want %q", string(downloaded), string(fullData))
	}
}

func TestCompleteTransferWithIncompleteFiles(t *testing.T) {
	env := setup(t)
	client := env.server.Client()

	// Create transfer.
	resp, _ := http.Post(env.url("/api/v1/transfers"), "application/json", nil)
	var created api.CreateTransferResponse
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Create a file upload but don't upload any data.
	tusReq, _ := http.NewRequest("POST", env.url("/api/v1/transfers/"+created.ID+"/files"), nil)
	tusReq.Header.Set("Tus-Resumable", "1.0.0")
	tusReq.Header.Set("Upload-Length", "100")
	resp, _ = client.Do(tusReq)
	resp.Body.Close()

	// Try to complete — should fail.
	resp, _ = http.Post(env.url("/api/v1/transfers/"+created.ID+"/complete"), "", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestDownloadLimit(t *testing.T) {
	env := setup(t)
	client := env.server.Client()

	// Create transfer with max 1 download.
	resp, _ := http.Post(env.url("/api/v1/transfers"), "application/json",
		strings.NewReader(`{"max_downloads": 1}`))
	var created api.CreateTransferResponse
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	transferID := created.ID

	// Upload a file.
	fileData := []byte("limited download")
	tusReq, _ := http.NewRequest("POST", env.url("/api/v1/transfers/"+transferID+"/files"), nil)
	tusReq.Header.Set("Tus-Resumable", "1.0.0")
	tusReq.Header.Set("Upload-Length", fmt.Sprintf("%d", len(fileData)))
	resp, _ = client.Do(tusReq)
	fileID := path.Base(resp.Header.Get("Location"))
	resp.Body.Close()

	patchReq, _ := http.NewRequest("PATCH",
		env.url("/api/v1/transfers/"+transferID+"/files/"+fileID),
		bytes.NewReader(fileData))
	patchReq.Header.Set("Tus-Resumable", "1.0.0")
	patchReq.Header.Set("Upload-Offset", "0")
	patchReq.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, _ = client.Do(patchReq)
	resp.Body.Close()

	// Complete.
	resp, _ = http.Post(env.url("/api/v1/transfers/"+transferID+"/complete"), "", nil)
	resp.Body.Close()

	// First download — should work.
	resp, _ = http.Get(env.url("/api/v1/transfers/" + transferID + "/files/" + fileID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first download: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Second download — should be denied.
	resp, _ = http.Get(env.url("/api/v1/transfers/" + transferID + "/files/" + fileID))
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("second download: expected 410, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- Slot flow tests ---

func TestCreateSlot(t *testing.T) {
	env := setup(t)

	resp, err := http.Post(env.url("/api/v1/slots"), "application/json", nil)
	if err != nil {
		t.Fatalf("create slot: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var result api.CreateSlotResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.ID == "" {
		t.Fatal("expected non-empty slot ID")
	}
}

func TestGetSlot(t *testing.T) {
	env := setup(t)

	// Create slot.
	resp, _ := http.Post(env.url("/api/v1/slots"), "application/json", nil)
	var created api.CreateSlotResponse
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Get slot.
	resp, err := http.Get(env.url("/api/v1/slots/" + created.ID))
	if err != nil {
		t.Fatalf("get slot: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var slot api.SlotResponse
	json.NewDecoder(resp.Body).Decode(&slot)
	if slot.ID != created.ID {
		t.Fatalf("expected ID %s, got %s", created.ID, slot.ID)
	}
	if slot.Status != "waiting" {
		t.Fatalf("expected status waiting, got %s", slot.Status)
	}
}

func TestSlotTransferFlow(t *testing.T) {
	env := setup(t)

	// Create slot.
	resp, _ := http.Post(env.url("/api/v1/slots"), "application/json", strings.NewReader(fixtureSlotJSON))
	var slot api.CreateSlotResponse
	json.NewDecoder(resp.Body).Decode(&slot)
	resp.Body.Close()

	// Create a transfer under the slot.
	resp, err := http.Post(env.url("/api/v1/slots/"+slot.ID+"/transfers"), "application/json", nil)
	if err != nil {
		t.Fatalf("create slot transfer: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}
	var transfer api.CreateTransferResponse
	json.NewDecoder(resp.Body).Decode(&transfer)
	resp.Body.Close()

	if transfer.ID == "" {
		t.Fatal("expected non-empty transfer ID")
	}

	// Get slot — should have the transfer linked.
	resp, _ = http.Get(env.url("/api/v1/slots/" + slot.ID))
	var slotResp api.SlotResponse
	json.NewDecoder(resp.Body).Decode(&slotResp)
	resp.Body.Close()

	if len(slotResp.Transfers) != 1 {
		t.Fatalf("expected 1 transfer, got %d", len(slotResp.Transfers))
	}
	if slotResp.Transfers[0].TransferID != transfer.ID {
		t.Fatalf("expected transfer %s, got %s", transfer.ID, slotResp.Transfers[0].TransferID)
	}
}

// --- Expiry/cleanup test ---

func TestCleanupExpiredTransfers(t *testing.T) {
	env := setup(t)

	// Create a transfer that expires immediately.
	resp, _ := http.Post(env.url("/api/v1/transfers"), "application/json",
		strings.NewReader(`{"expires_in_seconds": 1}`))
	var created api.CreateTransferResponse
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Directly set expires_at in the past.
	_, err := env.db.Exec("UPDATE transfers SET expires_at = ? WHERE id = ?",
		time.Now().Add(-1*time.Hour), created.ID)
	if err != nil {
		t.Fatalf("update expires_at: %v", err)
	}

	// Run the same worker that production starts, including its immediate sweep.
	fs, err := store.NewDiskStore(env.dataDir + "/files")
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.Save(created.ID+"/blob", strings.NewReader("expired bytes")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cleanup.NewWorker(env.queries, fs, time.Hour).Run(ctx)
	if _, err := os.Stat(env.dataDir + "/files/" + created.ID); !os.IsNotExist(err) {
		t.Fatalf("expired files still exist: %v", err)
	}

	// Transfer should be gone.
	resp, _ = http.Get(env.url("/api/v1/transfers/" + created.ID))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after cleanup, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTusOptions(t *testing.T) {
	env := setup(t)
	client := env.server.Client()

	// Create transfer first.
	resp, _ := http.Post(env.url("/api/v1/transfers"), "application/json", nil)
	var created api.CreateTransferResponse
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	req, _ := http.NewRequest("OPTIONS", env.url("/api/v1/transfers/"+created.ID+"/files"), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Tus-Version") != "1.0.0" {
		t.Fatalf("expected Tus-Version 1.0.0, got %s", resp.Header.Get("Tus-Version"))
	}
}

// Existing lifecycle suites exercise resource behavior using an authenticated
// owner. Auth security tests use setupAuthFixture(false), the unwrapped router.
func authenticatedFixture(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" && (r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodHead) {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		next.ServeHTTP(w, r)
	})
}
