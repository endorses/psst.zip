package api_test

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func payloadUsage(t *testing.T, env *testEnv) database.ResourceUsage {
	t.Helper()
	usage, err := env.queries.ResourceUsage("fixture-user")
	if err != nil {
		t.Fatal(err)
	}
	return usage
}

func assertPayloadOffset(t *testing.T, env *testEnv, target string, want int) {
	t.Helper()
	response := request(t, env, "HEAD", target, nil, http.StatusOK)
	if got := response.Header.Get("Upload-Offset"); got != strconv.Itoa(want) {
		t.Fatalf("HEAD offset %q, want %d", got, want)
	}
}

func assertPayloadResponse(t *testing.T, env *testEnv, target, want string) {
	t.Helper()
	response := request(t, env, "GET", target, nil, http.StatusOK)
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != want || response.ContentLength != int64(len(want)) {
		t.Fatalf("download %q length=%d, want %q: %v", data, response.ContentLength, want, err)
	}
}

func TestPendingPayloadHeadTruncatesUnacknowledgedTail(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	target := newUpload(t, env, id, 6)
	patch(t, env, target, "ab", 0, http.StatusNoContent, false)
	before := payloadUsage(t, env)
	disk := fixtureStore(t, env)
	key := id + "/" + path.Base(target)
	if n, err := disk.SaveAt(key, strings.NewReader("unacknowledged"), 2); err != nil || n != 14 {
		t.Fatal(n, err)
	}
	assertPayloadOffset(t, env, target, 2)
	if info, err := disk.Inspect(key); err != nil || !info.Exists || info.Size != 2 {
		t.Fatalf("HEAD did not truncate tail: %+v %v", info, err)
	}
	if after := payloadUsage(t, env); after != before {
		t.Fatalf("tail repair changed reservations: before=%+v after=%+v", before, after)
	}
	patch(t, env, target, "cdef", 2, http.StatusNoContent, false)
	finish(t, env, id)
	assertPayloadResponse(t, env, target, "abcdef")
}

func TestPendingPayloadLossRewindsOffsetThenResumes(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "short"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			env := setup(t)
			id := newTransfer(t, env, 0)
			target := newUpload(t, env, id, 6)
			patch(t, env, target, "abcd", 0, http.StatusNoContent, false)
			before := payloadUsage(t, env)
			disk := fixtureStore(t, env)
			key := id + "/" + path.Base(target)
			wantOffset := 2
			if missing {
				wantOffset = 0
				if err := os.Remove(env.dataDir + "/files/" + key); err != nil {
					t.Fatal(err)
				}
			} else if err := disk.Truncate(key, 2); err != nil {
				t.Fatal(err)
			}
			// The client resumes at its previously acknowledged offset; the
			// request must first repair metadata and reject that stale offset.
			patch(t, env, target, "ef", 4, http.StatusConflict, false)
			assertPayloadOffset(t, env, target, wantOffset)
			after := payloadUsage(t, env)
			if after.ReservedBytes != before.ReservedBytes || after.Files != before.Files || after.Transfers != before.Transfers || after.OccupiedBytes != int64(wantOffset) {
				t.Fatalf("rewind refunded reservations or retained wrong offset: before=%+v after=%+v", before, after)
			}
			patch(t, env, target, "abcdef"[wantOffset:], wantOffset, http.StatusNoContent, false)
			finish(t, env, id)
			assertPayloadResponse(t, env, target, "abcdef")
		})
	}
}

func TestPublishedPayloadMismatchRejectsBeforeDownloadReservation(t *testing.T) {
	for _, physical := range []string{"missing", "short", "long"} {
		t.Run(physical, func(t *testing.T) {
			env := setup(t)
			id := newTransfer(t, env, 1)
			target := newUpload(t, env, id, 4)
			patch(t, env, target, "data", 0, http.StatusNoContent, false)
			finish(t, env, id)
			before := payloadUsage(t, env)
			key := id + "/" + path.Base(target)
			payloadPath := env.dataDir + "/files/" + key
			var remaining string
			switch physical {
			case "missing":
				if err := os.Remove(payloadPath); err != nil {
					t.Fatal(err)
				}
			case "short":
				remaining = "da"
				if err := fixtureStore(t, env).Truncate(key, 2); err != nil {
					t.Fatal(err)
				}
			case "long":
				remaining = "dataextra"
				if n, err := fixtureStore(t, env).SaveAt(key, strings.NewReader("extra"), 4); err != nil || n != 5 {
					t.Fatal(n, err)
				}
			}
			for range 2 {
				response := request(t, env, "GET", target, nil, http.StatusServiceUnavailable)
				if response.Header.Get("X-Psst-Error-Code") != "payload_unavailable" {
					t.Fatal("missing stable unavailable response", response.Header)
				}
			}
			file, err := env.queries.GetFile(path.Base(target))
			if err != nil || file.DownloadCount != 0 || file.PayloadDeleted || file.UploadOffset != 4 || !file.UploadComplete {
				t.Fatalf("unavailable publication modified allowance/metadata: %+v %v", file, err)
			}
			if after := payloadUsage(t, env); after != before {
				t.Fatalf("unavailable data refunded quota: before=%+v after=%+v", before, after)
			}
			data, err := os.ReadFile(payloadPath)
			if physical == "missing" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("missing published data recreated", err)
				}
			} else if err != nil || string(data) != remaining {
				t.Fatalf("published data rewritten/deleted: %q %v", data, err)
			}
		})
	}
}

func TestPayloadCleanupTaskDoesNotBypassAvailableFileInspection(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 1)
	first := newUpload(t, env, id, 4)
	second := newUpload(t, env, id, 4)
	patch(t, env, first, "file", 0, http.StatusNoContent, false)
	patch(t, env, second, "data", 0, http.StatusNoContent, false)
	finish(t, env, id)
	assertPayloadResponse(t, env, first, "file")
	// A retained payload-only task must not exempt another file from checks.
	// Current discovery waits for every file's limit; explicitly retain this
	// stale task to exercise recovery independently of discovery invariants.
	if _, err := env.db.Exec(`INSERT INTO cleanup_tasks(kind,resource_id,mode,reason,pending_since) VALUES('transfer',?,'payload','download_limit',unixepoch())`, id); err != nil {
		t.Fatal(err)
	}
	before := payloadUsage(t, env)
	key := id + "/" + path.Base(second)
	disk := fixtureStore(t, env)
	if err := disk.Truncate(key, 2); err != nil {
		t.Fatal(err)
	}
	response := request(t, env, "GET", second, nil, http.StatusServiceUnavailable)
	if response.Header.Get("X-Psst-Error-Code") != "payload_unavailable" {
		t.Fatal(response.Header)
	}
	file, err := env.queries.GetFile(path.Base(second))
	if err != nil || file.DownloadCount != 0 || file.PayloadDeleted || !file.UploadComplete || file.UploadOffset != 4 {
		t.Fatalf("queued cleanup bypass changed available-file metadata: %+v %v", file, err)
	}
	if after := payloadUsage(t, env); after != before {
		t.Fatalf("queued cleanup refunded unavailable data: before=%+v after=%+v", before, after)
	}
	if info, err := disk.Inspect(key); err != nil || !info.Exists || info.Size != 2 {
		t.Fatal("published short payload modified", info, err)
	}
}

func TestPendingPublicationRepairsMissingBytesBeforeRejectingCompletion(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	target := newUpload(t, env, id, 4)
	patch(t, env, target, "data", 0, http.StatusNoContent, false)
	request(t, env, "POST", env.url("/api/v1/transfers/"+id+"/manifest"), strings.NewReader("encrypted manifest"), http.StatusNoContent)
	before := payloadUsage(t, env)
	if err := fixtureStore(t, env).Truncate(id+"/"+path.Base(target), 2); err != nil {
		t.Fatal(err)
	}
	request(t, env, "POST", env.url("/api/v1/transfers/"+id+"/complete"), nil, http.StatusBadRequest)
	file, err := env.queries.GetFile(path.Base(target))
	if err != nil || file.UploadOffset != 2 || file.UploadComplete {
		t.Fatalf("completion did not repair pending bytes: %+v %v", file, err)
	}
	transfer, err := env.queries.GetTransfer(id)
	if err != nil || transfer.Status != "pending" {
		t.Fatalf("incomplete transfer published: %+v %v", transfer, err)
	}
	after := payloadUsage(t, env)
	if after.ReservedBytes != before.ReservedBytes || after.Files != before.Files || after.Transfers != before.Transfers {
		t.Fatalf("failed publication refunded quota: before=%+v after=%+v", before, after)
	}
	assertPayloadOffset(t, env, target, 2)
	patch(t, env, target, "ta", 2, http.StatusNoContent, false)
	request(t, env, "POST", env.url("/api/v1/transfers/"+id+"/complete"), nil, http.StatusNoContent)
	assertPayloadResponse(t, env, target, "data")
}

func TestPayloadWriteSurvivesOffsetCommitFailureWithoutPublishingTail(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	target := newUpload(t, env, id, 4)
	before := payloadUsage(t, env)
	if _, err := env.db.Exec(`CREATE TRIGGER fail_upload_offset BEFORE UPDATE OF upload_offset ON files WHEN NEW.upload_offset>OLD.upload_offset BEGIN SELECT RAISE(ABORT,'injected offset commit failure'); END;`); err != nil {
		t.Fatal(err)
	}
	patch(t, env, target, "data", 0, http.StatusInternalServerError, false)
	disk := fixtureStore(t, env)
	key := id + "/" + path.Base(target)
	if info, err := disk.Inspect(key); err != nil || info.Size != 4 {
		t.Fatalf("test did not create physical/DB mismatch: %+v %v", info, err)
	}
	file, err := env.queries.GetFile(path.Base(target))
	if err != nil || file.UploadOffset != 0 || file.UploadComplete {
		t.Fatal(file, err)
	}
	assertPayloadOffset(t, env, target, 0)
	if info, err := disk.Inspect(key); err != nil || !info.Exists || info.Size != 0 {
		t.Fatalf("failed commit tail survived HEAD: %+v %v", info, err)
	}
	if after := payloadUsage(t, env); after != before {
		t.Fatalf("failed commit changed reservation: before=%+v after=%+v", before, after)
	}
	if _, err := env.db.Exec(`DROP TRIGGER fail_upload_offset`); err != nil {
		t.Fatal(err)
	}
	patch(t, env, target, "data", 0, http.StatusNoContent, false)
	finish(t, env, id)
	assertPayloadResponse(t, env, target, "data")
}

type failingPayloadCheckStore struct {
	store.FileStore
	failInspect  bool
	failTruncate bool
}

func (s failingPayloadCheckStore) Inspect(key string) (store.PayloadInfo, error) {
	if s.failInspect {
		return store.PayloadInfo{}, errors.New("private storage failure detail")
	}
	return s.FileStore.Inspect(key)
}
func (s failingPayloadCheckStore) Truncate(key string, size int64) error {
	if s.failTruncate {
		return errors.New("private storage failure detail")
	}
	return s.FileStore.Truncate(key, size)
}

func TestPayloadCheckFailuresAreSafeAndPreserveQuota(t *testing.T) {
	for _, fault := range []string{"inspect", "truncate"} {
		t.Run(fault, func(t *testing.T) {
			env := setup(t)
			id := newTransfer(t, env, 0)
			target := newUpload(t, env, id, 4)
			patch(t, env, target, "da", 0, http.StatusNoContent, false)
			before := payloadUsage(t, env)
			disk := fixtureStore(t, env)
			key := id + "/" + path.Base(target)
			if n, err := disk.SaveAt(key, strings.NewReader("ta"), 2); n != 2 || err != nil {
				t.Fatal(n, err)
			}
			broken := alternateServer(t, env, failingPayloadCheckStore{FileStore: disk, failInspect: fault == "inspect", failTruncate: fault == "truncate"}, false)
			target = strings.Replace(target, env.server.URL, broken.server.URL, 1)
			response := request(t, broken, "HEAD", target, nil, http.StatusServiceUnavailable)
			if response.Header.Get("X-Psst-Error-Code") != "storage_check_failed" {
				t.Fatal(response.Header)
			}
			patch(t, broken, target, "ta", 2, http.StatusServiceUnavailable, false)
			response = request(t, broken, "POST", broken.url("/api/v1/transfers/"+id+"/complete"), nil, http.StatusServiceUnavailable)
			body, err := io.ReadAll(response.Body)
			if err != nil || response.Header.Get("X-Psst-Error-Code") != "storage_check_failed" || strings.Contains(string(body), "private storage") {
				t.Fatal(string(body), err)
			}
			file, err := env.queries.GetFile(path.Base(target))
			if err != nil || file.UploadOffset != 2 || file.UploadComplete || file.PayloadDeleted {
				t.Fatal(file, err)
			}
			if after := payloadUsage(t, env); after != before {
				t.Fatalf("failed inspection/repair changed quota: before=%+v after=%+v", before, after)
			}
			if info, err := disk.Inspect(key); err != nil || info.Size != 4 {
				t.Fatal("failed repair unexpectedly removed physical bytes", info, err)
			}
		})
	}
}

type appendBeforePayloadLoadStore struct{ store.FileStore }

func (s appendBeforePayloadLoadStore) Load(key string) (io.ReadCloser, error) {
	if _, err := s.FileStore.SaveAt(key, strings.NewReader("uncommitted-tail"), 4); err != nil {
		return nil, err
	}
	return s.FileStore.Load(key)
}

func TestPayloadDownloadBoundsBytesAfterInspectionRace(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	target := newUpload(t, env, id, 4)
	patch(t, env, target, "data", 0, http.StatusNoContent, false)
	finish(t, env, id)
	racing := alternateServer(t, env, appendBeforePayloadLoadStore{fixtureStore(t, env)}, false)
	assertPayloadResponse(t, racing, strings.Replace(target, env.server.URL, racing.server.URL, 1), "data")
}

func TestStorageChecksStatusIsAdminOnlyAndUncached(t *testing.T) {
	env := setupAuthFixture(t, false)
	authRequest(t, env, "GET", "/admin/storage-checks", "", nil, http.StatusUnauthorized)
	authRequest(t, env, "GET", "/admin/storage-checks", env.userToken, nil, http.StatusForbidden)
	req, _ := http.NewRequest("GET", env.url("/api/v1/admin/storage-checks"), nil)
	req.Header.Set("Authorization", "Bearer "+env.authToken)
	response, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(response.StatusCode, response.Header)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || !strings.Contains(string(body), `"scan_pending":true`) || strings.Contains(string(body), env.dataDir) {
		t.Fatalf("unsafe or missing scan status: %s %v", body, err)
	}
}
