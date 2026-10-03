package api_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func downloadStatus(t *testing.T, env *testEnv, id string) api.TransferResponse {
	t.Helper()
	response := request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+id), nil, http.StatusOK)
	var result api.TransferResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDownloadAcknowledgementRequiresAllFilesAndIsIdempotent(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	acknowledgement := env.url("/api/v1/transfers/" + id + "/downloaded")
	request(t, env, http.MethodPost, acknowledgement, nil, http.StatusConflict)
	first, second := newUpload(t, env, id, 1), newUpload(t, env, id, 1)
	patch(t, env, first, "a", 0, http.StatusNoContent, false)
	patch(t, env, second, "b", 0, http.StatusNoContent, false)
	finish(t, env, id)
	request(t, env, http.MethodPost, acknowledgement, nil, http.StatusConflict)
	request(t, env, http.MethodGet, first, nil, http.StatusOK)
	request(t, env, http.MethodPost, acknowledgement, nil, http.StatusConflict)
	request(t, env, http.MethodGet, second, nil, http.StatusOK)
	requested := downloadStatus(t, env, id)
	if requested.DownloadCount != 1 || requested.DownloadedAt != nil {
		t.Fatalf("GET attempts must not acknowledge: %+v", requested)
	}
	request(t, env, http.MethodPost, acknowledgement, nil, http.StatusNoContent)
	acknowledged := downloadStatus(t, env, id)
	if acknowledged.DownloadedAt == nil || acknowledged.DownloadedAt.IsZero() {
		t.Fatalf("missing acknowledgement: %+v", acknowledged)
	}
	if acknowledged.DownloadCount != requested.DownloadCount {
		t.Fatal("acknowledgement changed request count")
	}
	request(t, env, http.MethodPost, acknowledgement, nil, http.StatusNoContent)
	retried := downloadStatus(t, env, id)
	if retried.DownloadedAt == nil || !retried.DownloadedAt.Equal(*acknowledged.DownloadedAt) {
		t.Fatal("retry replaced first acknowledgement timestamp")
	}
	// The wire field is always present: nullable before, RFC3339 after acknowledgement.
	response := request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+id), nil, http.StatusOK)
	var wire map[string]any
	if err := json.NewDecoder(response.Body).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	timestamp, ok := wire["downloaded_at"].(string)
	if !ok {
		t.Fatalf("downloaded_at = %v", wire["downloaded_at"])
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadAcknowledgementRejectsEmptyMissingAndExpiredTransfers(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	endpoint := env.url("/api/v1/transfers/" + id + "/downloaded")
	response := request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+id), nil, http.StatusOK)
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if value, present := body["downloaded_at"]; !present || value != nil {
		t.Fatalf("expected explicit null downloaded_at, got %v", body)
	}
	finish(t, env, id)
	request(t, env, http.MethodPost, endpoint, nil, http.StatusConflict)
	request(t, env, http.MethodPost, env.url("/api/v1/transfers/invalid/downloaded"), nil, http.StatusBadRequest)
	request(t, env, http.MethodPost, env.url("/api/v1/transfers/00000000-0000-0000-0000-000000000000/downloaded"), nil, http.StatusNotFound)
	if _, err := env.db.Exec("UPDATE transfers SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Minute), id); err != nil {
		t.Fatal(err)
	}
	request(t, env, http.MethodPost, endpoint, nil, http.StatusGone)
}

func TestQuotaCleanupPreservesAcknowledgementUntilTTL(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 1)
	target := newUpload(t, env, id, 4)
	patch(t, env, target, "data", 0, http.StatusNoContent, false)
	finish(t, env, id)
	request(t, env, http.MethodGet, target, nil, http.StatusOK)
	// Model quota cleanup winning the race before the recipient sends its report.
	sweep(t, env)
	if _, err := os.Stat(env.dataDir + "/files/" + id); !os.IsNotExist(err) {
		t.Fatalf("quota payload remains: %v", err)
	}
	request(t, env, http.MethodGet, target, nil, http.StatusGone)
	status := downloadStatus(t, env, id)
	if status.DownloadCount != 1 || status.DownloadedAt != nil || status.FileCount != 1 {
		t.Fatalf("lost metadata or invented report: %+v", status)
	}
	request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+id+"/manifest"), nil, http.StatusOK)
	endpoint := env.url("/api/v1/transfers/" + id + "/downloaded")
	request(t, env, http.MethodPost, endpoint, nil, http.StatusNoContent)
	first := downloadStatus(t, env, id)
	sweep(t, env)
	request(t, env, http.MethodPost, endpoint, nil, http.StatusNoContent)
	later := downloadStatus(t, env, id)
	if later.DownloadedAt == nil || !later.DownloadedAt.Equal(*first.DownloadedAt) {
		t.Fatal("cleanup lost acknowledgement")
	}
	if _, err := env.db.Exec("UPDATE transfers SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Minute), id); err != nil {
		t.Fatal(err)
	}
	request(t, env, http.MethodPost, endpoint, nil, http.StatusGone)
	sweep(t, env)
	request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+id), nil, http.StatusNotFound)
	var remaining int
	if err := env.db.QueryRow("SELECT (SELECT COUNT(*) FROM files WHERE transfer_id = ?) + (SELECT COUNT(*) FROM manifests WHERE transfer_id = ?)", id, id).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("TTL did not remove associated metadata")
	}
}

func TestConcurrentDownloadAcknowledgementsKeepFirstTimestamp(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	target := newUpload(t, env, id, 1)
	patch(t, env, target, "x", 0, http.StatusNoContent, false)
	finish(t, env, id)
	request(t, env, http.MethodGet, target, nil, http.StatusOK)
	endpoint := env.url("/api/v1/transfers/" + id + "/downloaded")
	statuses := make(chan int, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			response, err := env.server.Client().Post(endpoint, "", nil)
			if err != nil {
				statuses <- 0
				return
			}
			defer response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	group.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusNoContent {
			t.Fatalf("concurrent acknowledgement returned %d", status)
		}
	}
	first := downloadStatus(t, env, id)
	request(t, env, http.MethodPost, endpoint, nil, http.StatusNoContent)
	later := downloadStatus(t, env, id)
	if first.DownloadedAt == nil || later.DownloadedAt == nil || !first.DownloadedAt.Equal(*later.DownloadedAt) {
		t.Fatal("first timestamp was not preserved")
	}
}

type interruptedFileStore struct{ store.FileStore }

func (s interruptedFileStore) Load(string) (io.ReadCloser, error) {
	return io.NopCloser(interruptedReader{}), nil
}

type interruptedReader struct{}

func (interruptedReader) Read([]byte) (int, error) {
	return 0, errors.New("simulated interrupted storage stream")
}

func TestFailedDownloadStreamsNeverAcknowledge(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	target := newUpload(t, env, id, 4)
	patch(t, env, target, "data", 0, http.StatusNoContent, false)
	finish(t, env, id)
	fs, err := store.NewDiskStore(env.dataDir + "/files")
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewServer(config.Config{RateLimitGlobal: 1000, RateLimitBurst: 1000}, env.queries, interruptedFileStore{fs}).Router()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/transfers/"+id+"/files/"+path.Base(target), nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("expected interrupted body, got %d %q", response.Code, response.Body.String())
	}
	current := downloadStatus(t, env, id)
	if current.DownloadCount != 1 || current.DownloadedAt != nil {
		t.Fatalf("interrupted stream falsely acknowledged: %+v", current)
	}
	// A blob that cannot be opened must not consume a request or create a report.
	other := newTransfer(t, env, 0)
	missing := newUpload(t, env, other, 4)
	patch(t, env, missing, "data", 0, http.StatusNoContent, false)
	finish(t, env, other)
	if err := fs.DeleteAll(other); err != nil {
		t.Fatal(err)
	}
	request(t, env, http.MethodGet, missing, nil, http.StatusInternalServerError)
	request(t, env, http.MethodPost, env.url("/api/v1/transfers/"+other+"/downloaded"), nil, http.StatusConflict)
	if current := downloadStatus(t, env, other); current.DownloadCount != 0 || current.DownloadedAt != nil {
		t.Fatalf("missing blob changed receipt: %+v", current)
	}
}

func TestQuotaCleanupKeepsAlreadyOpenPayloadReadable(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 1)
	target := newUpload(t, env, id, 4)
	patch(t, env, target, "data", 0, http.StatusNoContent, false)
	finish(t, env, id)
	fs, err := store.NewDiskStore(env.dataDir + "/files")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := fs.Load(id + "/" + path.Base(target))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	// Match the handler ordering: open the file, reserve the GET, then stream.
	allowed, err := env.queries.ReserveFileDownload(id, path.Base(target))
	if err != nil || !allowed {
		t.Fatalf("reserve: %v %v", allowed, err)
	}
	sweep(t, env)
	body, err := io.ReadAll(reader)
	if err != nil || string(body) != "data" {
		t.Fatalf("cleanup interrupted open reader: %q %v", body, err)
	}
	if _, err := fs.Load(id + "/" + path.Base(target)); err == nil {
		t.Fatal("cleanup retained quota-exhausted path")
	}
	// The encrypted manifest remains readable after blob cleanup.
	resp := request(t, env, http.MethodGet, env.url("/api/v1/transfers/"+id+"/manifest"), nil, http.StatusOK)
	manifest, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(manifest), "encrypted manifest") {
		t.Fatalf("manifest lost: %s %v", manifest, err)
	}
}
