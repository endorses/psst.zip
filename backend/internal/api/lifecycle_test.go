package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/cleanup"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func request(t *testing.T, env *testEnv, method, target string, body io.Reader, status int) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != status {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: want %d, got %d: %s", method, target, status, resp.StatusCode, data)
	}
	return resp
}

func newTransfer(t *testing.T, env *testEnv, limit int) string {
	t.Helper()
	resp := request(t, env, "POST", env.url("/api/v1/transfers"), strings.NewReader(fmt.Sprintf(`{"max_downloads":%d}`, limit)), http.StatusCreated)
	var result api.CreateTransferResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.ID
}

// Follow Location exactly as an HTTP/tus client does, resolving it against the
// creation URL. A bare blob UUID used to resolve to the wrong route.
func newUpload(t *testing.T, env *testEnv, transfer string, size int) string {
	t.Helper()
	creationURL := env.url("/api/v1/transfers/" + transfer + "/files")
	req, _ := http.NewRequest("POST", creationURL, nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", strconv.Itoa(size))
	resp, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create upload: %d", resp.StatusCode)
	}
	base, _ := url.Parse(creationURL)
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	resolved := base.ResolveReference(location).String()
	if !strings.HasPrefix(resolved, creationURL+"/") {
		t.Fatalf("Location resolves to wrong path: %s", resolved)
	}
	return resolved
}

func patchRequest(target, data string, offset int, chunked bool) *http.Request {
	req, _ := http.NewRequest("PATCH", target, strings.NewReader(data))
	if chunked {
		req.ContentLength = -1
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.Itoa(offset))
	return req
}

func patch(t *testing.T, env *testEnv, target, data string, offset, status int, chunked bool) {
	t.Helper()
	resp, err := env.server.Client().Do(patchRequest(target, data, offset, chunked))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PATCH: want %d, got %d: %s", status, resp.StatusCode, b)
	}
}

func finish(t *testing.T, env *testEnv, id string) {
	t.Helper()
	manifest := []byte("encrypted manifest")
	if protocol, err := env.queries.TransferReceiveProtocol(id); err != nil {
		t.Fatal(err)
	} else if protocol == 2 {
		manifest = fixtureReceiveEnvelope()
	}
	request(t, env, "POST", env.url("/api/v1/transfers/"+id+"/manifest"), bytes.NewReader(manifest), http.StatusNoContent)
	request(t, env, "POST", env.url("/api/v1/transfers/"+id+"/complete"), nil, http.StatusNoContent)
}

func sweep(t *testing.T, env *testEnv) {
	t.Helper()
	fs, err := store.NewDiskStore(env.dataDir + "/files")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cleanup.NewWorker(env.queries, fs, time.Hour).Run(ctx)
}

func TestTusBoundsOwnershipAndConcurrentOffsets(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	other := newTransfer(t, env, 0)
	target := newUpload(t, env, id, 4)
	fileID := path.Base(target)
	wrongOwner := env.url("/api/v1/transfers/" + other + "/files/" + fileID)
	request(t, env, "HEAD", wrongOwner, nil, http.StatusNotFound)
	patch(t, env, wrongOwner, "abcd", 0, http.StatusNotFound, false)
	patch(t, env, target, "abcde", 0, http.StatusRequestEntityTooLarge, false)
	patch(t, env, target, "abcde", 0, http.StatusRequestEntityTooLarge, true)
	resp := request(t, env, "HEAD", target, nil, http.StatusOK)
	if resp.Header.Get("Upload-Offset") != "0" {
		t.Fatal("rejected PATCH advanced offset")
	}
	info, err := os.Stat(env.dataDir + "/files/" + id + "/" + fileID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 4 {
		t.Fatalf("oversized body persisted: %d bytes", info.Size())
	}

	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := env.server.Client().Do(patchRequest(target, "ab", 0, false))
			if err != nil {
				statuses <- 0
				return
			}
			defer resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusNoContent] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("concurrent offset results: %v", counts)
	}
	patch(t, env, target, "cd", 2, http.StatusNoContent, false)
	finish(t, env, id)
	got, err := io.ReadAll(request(t, env, "GET", target, nil, http.StatusOK).Body)
	if err != nil || string(got) != "abcd" {
		t.Fatalf("retry data: %q, %v", got, err)
	}
}

func TestTransferLifecycleAndExpiryBeforeCleanup(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	target := newUpload(t, env, id, 1)
	patch(t, env, target, "x", 0, http.StatusNoContent, false)
	base := env.url("/api/v1/transfers/" + id)
	request(t, env, "GET", target, nil, http.StatusConflict)
	request(t, env, "GET", base+"/manifest", nil, http.StatusConflict)
	finish(t, env, id)
	for _, endpoint := range []string{"/files", "/manifest", "/complete"} {
		request(t, env, "POST", base+endpoint, nil, http.StatusConflict)
	}
	patch(t, env, target, "x", 0, http.StatusConflict, false)
	if _, err := env.db.Exec("UPDATE transfers SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Minute), id); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{base, base + "/manifest", target} {
		request(t, env, "GET", endpoint, nil, http.StatusGone)
	}
	request(t, env, "HEAD", target, nil, http.StatusGone)
	for _, endpoint := range []string{"/files", "/manifest", "/complete"} {
		request(t, env, "POST", base+endpoint, nil, http.StatusGone)
	}
	patch(t, env, target, "x", 0, http.StatusGone, false)
}

func TestMultiFileDownloadQuotaAndCleanup(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 1)
	first, second := newUpload(t, env, id, 1), newUpload(t, env, id, 1)
	patch(t, env, first, "a", 0, http.StatusNoContent, false)
	patch(t, env, second, "b", 0, http.StatusNoContent, false)
	finish(t, env, id)
	request(t, env, "GET", first, nil, http.StatusOK)
	request(t, env, "GET", first, nil, http.StatusGone)
	sweep(t, env)
	// Exhausting one file must not delete the other files in a transfer.
	request(t, env, "GET", env.url("/api/v1/transfers/"+id+"/manifest"), nil, http.StatusOK)
	request(t, env, "GET", second, nil, http.StatusOK)
	tr, err := env.queries.GetTransfer(id)
	if err != nil || tr.DownloadCount != 1 {
		t.Fatalf("complete set count: %+v, %v", tr, err)
	}
	sweep(t, env)
	request(t, env, "GET", second, nil, http.StatusGone)
	if _, err := os.Stat(env.dataDir + "/files/" + id); !os.IsNotExist(err) {
		t.Fatalf("exhausted files remain: %v", err)
	}
}

func TestConcurrentDownloadQuota(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 1)
	target := newUpload(t, env, id, 1)
	patch(t, env, target, "a", 0, http.StatusNoContent, false)
	finish(t, env, id)
	statuses := make(chan int, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := env.server.Client().Get(target)
			if err != nil {
				statuses <- 0
				return
			}
			defer resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusGone] != 7 {
		t.Fatalf("concurrent quota results: %v", counts)
	}
}

func TestSlotCompletionEventsAndLifetime(t *testing.T) {
	env := setup(t)
	response := request(t, env, "POST", env.url("/api/v1/slots"), strings.NewReader(`{"expires_in_seconds":60,"receive_protocol":2,"recipient_public_key":"`+fixtureRecipientKey+`"}`), http.StatusCreated)
	var slot api.CreateSlotResponse
	if err := json.NewDecoder(response.Body).Decode(&slot); err != nil {
		t.Fatal(err)
	}
	slotURL := env.url("/api/v1/slots/" + slot.ID)
	empty := request(t, env, "GET", slotURL, nil, http.StatusOK)
	var emptySlot api.SlotResponse
	if err := json.NewDecoder(empty.Body).Decode(&emptySlot); err != nil {
		t.Fatal(err)
	}
	if emptySlot.Transfers == nil {
		t.Fatal("empty slot transfers must be []")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", slotURL+"/events", nil)
	events, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	scanner := bufio.NewScanner(events.Body)
	// Consume the initial connection event, ensuring subscription is installed.
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}
	resp := request(t, env, "POST", slotURL+"/transfers", strings.NewReader(`{"expires_in_seconds":3600}`), http.StatusCreated)
	var transfer api.CreateTransferResponse
	if err := json.NewDecoder(resp.Body).Decode(&transfer); err != nil {
		t.Fatal(err)
	}
	if transfer.ExpiresAt.After(slot.ExpiresAt) {
		t.Fatal("transfer outlives slot")
	}
	target := newUpload(t, env, transfer.ID, 1)
	patch(t, env, target, "x", 0, http.StatusNoContent, false)
	finish(t, env, transfer.ID)
	found := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"event":"transfer_complete"`) && strings.Contains(scanner.Text(), transfer.ID) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing completion event: %v", scanner.Err())
	}
	if _, err := env.db.Exec("UPDATE slots SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Minute), slot.ID); err != nil {
		t.Fatal(err)
	}
	request(t, env, "GET", slotURL, nil, http.StatusGone)
	request(t, env, "GET", slotURL+"/events", nil, http.StatusGone)
	request(t, env, "POST", slotURL+"/transfers", nil, http.StatusGone)
	sweep(t, env)
	request(t, env, "GET", slotURL, nil, http.StatusNotFound)
}

func TestCleanupComparesTimezoneAwareExpiry(t *testing.T) {
	env := setup(t)
	expired, live := newTransfer(t, env, 0), newTransfer(t, env, 0)
	// Local clock values deliberately sort in the opposite order to actual time.
	past := time.Now().Add(-time.Minute).In(time.FixedZone("EAST", 12*60*60))
	future := time.Now().Add(time.Minute).In(time.FixedZone("WEST", -12*60*60))
	for id, expiry := range map[string]time.Time{expired: past, live: future} {
		if _, err := env.db.Exec("UPDATE transfers SET expires_at = ? WHERE id = ?", expiry, id); err != nil {
			t.Fatal(err)
		}
	}
	sweep(t, env)
	request(t, env, "GET", env.url("/api/v1/transfers/"+expired), nil, http.StatusNotFound)
	request(t, env, "GET", env.url("/api/v1/transfers/"+live), nil, http.StatusOK)
}

func TestUploadLimitAndBrowserPreflight(t *testing.T) {
	env := setup(t)
	id := newTransfer(t, env, 0)
	base := env.url("/api/v1/transfers/" + id)
	req, _ := http.NewRequest("POST", base+"/files", nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", strconv.Itoa(100*1024*1024+1))
	resp, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload size limit: %d", resp.StatusCode)
	}
	for _, target := range []string{env.url("/api/v1/transfers"), base + "/manifest", base + "/files/file-id"} {
		req, _ := http.NewRequest("OPTIONS", target, nil)
		req.Header.Set("Origin", "https://web.example")
		req.Header.Set("Access-Control-Request-Method", "PATCH")
		req.Header.Set("Access-Control-Request-Headers", "content-type,tus-resumable,upload-offset")
		resp, err := env.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent || !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "PATCH") {
			t.Fatalf("preflight %s: %d %v", target, resp.StatusCode, resp.Header)
		}
	}
}
