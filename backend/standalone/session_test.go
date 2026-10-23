package standalone

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func sessionForTest(t *testing.T, files int) *Session {
	t.Helper()
	s, err := New(Config{Directory: t.TempDir(), Origin: "http://192.168.1.42:49152", ExpiresAt: time.Now().Add(time.Hour), MaxBytes: 1 << 20, MaxFiles: files, MaxDownloads: 1, MaxStreams: 2})
	if err != nil {
		t.Fatal(err)
	}
	var networkIdentities int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM users WHERE role='admin') + (SELECT COUNT(*) FROM sessions)`).Scan(&networkIdentities); err != nil || networkIdentities != 0 {
		t.Fatalf("standalone bootstrapped network identity: count=%d err=%v", networkIdentities, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func request(h http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://192.168.1.42:49152"+path, bytes.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func requireStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
}
func resourceID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	requireStatus(t, w, http.StatusCreated)
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.ID
}
func seedSend(t *testing.T, s *Session) (string, string) {
	t.Helper()
	h := s.OwnerHandler()
	id := resourceID(t, request(h, "POST", "/api/v1/transfers", []byte(`{"max_downloads":`+strconv.Itoa(s.cfg.MaxDownloads)+`}`), nil))
	root := "/api/v1/transfers/" + id
	create := request(h, "POST", root+"/files", nil, map[string]string{"Upload-Length": "10", "Tus-Resumable": "1.0.0"})
	requireStatus(t, create, 201)
	location := create.Header().Get("Location")
	requireStatus(t, request(h, "PATCH", location, []byte("ciphertext"), map[string]string{"Upload-Offset": "0", "Tus-Resumable": "1.0.0", "Content-Type": "application/offset+octet-stream"}), 204)
	requireStatus(t, request(h, "POST", root+"/manifest", []byte("opaque encrypted manifest"), nil), 204)
	requireStatus(t, request(h, "POST", root+"/complete", nil, nil), 204)
	if err := s.Activate("send", id); err != nil {
		t.Fatal(err)
	}
	return id, location
}

func TestSendSharesHostedProtocolAndAcknowledgements(t *testing.T) {
	s := sessionForTest(t, 2)
	id, file := seedSend(t, s)
	root := "/api/v1/transfers/" + id
	h := s.Handler()
	requireStatus(t, request(h, "POST", root+"/downloaded", nil, nil), 409)
	response := request(h, "GET", file, nil, nil)
	requireStatus(t, response, 200)
	if response.Body.String() != "ciphertext" {
		t.Fatal("ciphertext changed")
	}
	stats, err := s.Stats()
	if err != nil || stats.Downloads != 0 {
		t.Fatalf("GET inferred decryption acknowledgement: %+v %v", stats, err)
	}
	requireStatus(t, request(h, "POST", root+"/downloaded", nil, nil), 204)
	stats, err = s.Stats()
	if err != nil || stats.Downloads != 1 {
		t.Fatalf("acknowledgement missing: %+v %v", stats, err)
	}
	requireStatus(t, request(h, "GET", file, nil, nil), 410)
	// Private account identity and owner creation never become public authority.
	for _, path := range []string{"/api/v1/transfers", "/api/v1/slots", "/api/v1/auth/login", "/api/v1/admin/settings", root + "/manifest", root + "/complete"} {
		requireStatus(t, request(h, "POST", path, []byte(`{}`), nil), 404)
	}
	requireStatus(t, request(h, "GET", "/api/v1/transfers/00000000-0000-0000-0000-000000000000", nil, nil), 404)
	if err := s.Activate("send", id); err == nil {
		t.Fatal("second activation allowed")
	}
}

func TestReceiveRestrictsInboxAndUsesExistingTus(t *testing.T) {
	s := sessionForTest(t, 2)
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	body := []byte(`{"receive_protocol":2,"recipient_public_key":"` + key + `","max_files":2}`)
	slot := resourceID(t, request(s.OwnerHandler(), "POST", "/api/v1/slots", body, nil))
	if err := s.Activate("receive", slot); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	root := "/api/v1/slots/" + slot
	requireStatus(t, request(h, "GET", root+"/availability", nil, nil), 200)
	requireStatus(t, request(h, "GET", root, nil, nil), 404)
	created := request(h, "POST", root+"/transfers", []byte(`{}`), nil)
	id := resourceID(t, created)
	var authority struct {
		Token string `json:"delete_token"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &authority); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + authority.Token, "Upload-Length": "10", "Tus-Resumable": "1.0.0"}
	transfer := "/api/v1/transfers/" + id
	file := request(h, "POST", transfer+"/files", nil, headers)
	requireStatus(t, file, 201)
	location := file.Header().Get("Location")
	headers["Upload-Offset"] = "0"
	headers["Content-Type"] = "application/offset+octet-stream"
	requireStatus(t, request(h, "PATCH", location, []byte("ciphertext"), headers), 204)
	envelope := append([]byte("PSSTRCV2"), bytes.Repeat([]byte{1}, 108)...)
	requireStatus(t, request(h, "POST", transfer+"/manifest", envelope, headers), 204)
	requireStatus(t, request(h, "POST", transfer+"/complete", nil, headers), 204)
	requireStatus(t, request(h, "GET", transfer+"/upload-status", nil, headers), 200)
	for _, path := range []string{transfer, transfer + "/manifest", location, root + "/inbox", root + "/events"} {
		requireStatus(t, request(h, "GET", path, nil, headers), 404)
	}
	owner := request(s.OwnerHandler(), "GET", location, nil, nil)
	requireStatus(t, owner, 200)
	if owner.Body.String() != "ciphertext" {
		t.Fatal("owner payload changed")
	}
	stats, err := s.Stats()
	if err != nil || stats.CompletedFiles != 1 {
		t.Fatalf("completed inbox count %+v %v", stats, err)
	}
}

func TestHostOriginExpiryAndPrivateCleanup(t *testing.T) {
	s := sessionForTest(t, 2)
	_, file := seedSend(t, s)
	for _, header := range []map[string]string{{"Origin": "http://attacker.example"}, {"Origin": "null"}, {"Sec-Fetch-Site": "cross-site"}} {
		requireStatus(t, request(s.Handler(), "GET", file, nil, header), 403)
	}
	r := httptest.NewRequest("GET", "http://attacker.example"+file, nil)
	r.Header.Set("X-Forwarded-Host", "192.168.1.42:49152")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	requireStatus(t, w, 403)
	requireStatus(t, request(s.Handler(), "GET", "/api/v1/config", nil, map[string]string{"Origin": s.cfg.Origin}), 200)
	s.cfg.ExpiresAt = time.Now().Add(-time.Second)
	requireStatus(t, request(s.Handler(), "GET", file, nil, nil), 410)
	dir := s.dir
	parent := s.cfg.Directory
	sentinel := parent + "/received.txt"
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("owned stage retained: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("received output removed: %v", err)
	}
	requireStatus(t, request(s.OwnerHandler(), "GET", "/api/v1/config", nil, nil), 410)
}

func TestConcurrentReservationsCannotExceedByteBudget(t *testing.T) {
	s := sessionForTest(t, 2)
	id := resourceID(t, request(s.OwnerHandler(), "POST", "/api/v1/transfers", []byte(`{"max_downloads":1}`), nil))
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(s.OwnerHandler(), "POST", "/api/v1/transfers/"+id+"/files", nil, map[string]string{"Upload-Length": "700000", "Tus-Resumable": "1.0.0"})
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	successes := 0
	for code := range codes {
		if code == 201 {
			successes++
		} else if code != 403 {
			t.Fatalf("unexpected admission status %d", code)
		}
	}
	if successes != 1 {
		t.Fatalf("byte quota admitted %d overlapping reservations", successes)
	}
}

func TestInvalidConfigurationDoesNotCreateState(t *testing.T) {
	cfg := Config{Directory: t.TempDir(), Origin: "http://127.0.0.1:1234", ExpiresAt: time.Now().Add(time.Hour), MaxBytes: 1 << 20, MaxFiles: 1, MaxDownloads: 1, MaxStreams: 2}
	for _, origin := range []string{"https://example.test/path", "http://user:pass@example.test", "file:///tmp/data", "http://example.test?query"} {
		cfg.Origin = origin
		if s, err := New(cfg); err == nil {
			_ = s.Close()
			t.Fatalf("accepted %s", origin)
		}
	}
	entries, err := os.ReadDir(cfg.Directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid config wrote state: %v %+v", err, entries)
	}
}

// A slow recipient must hold a shared payload lease until its response finishes.
type heldWriter struct {
	*httptest.ResponseRecorder
	entered chan<- struct{}
	release <-chan struct{}
}

func (w heldWriter) Write(p []byte) (int, error) {
	w.entered <- struct{}{}
	<-w.release
	return w.ResponseRecorder.Write(p)
}

func TestPayloadConcurrencyIsFiniteUntilStreamsDrain(t *testing.T) {
	s, err := New(Config{Directory: t.TempDir(), Origin: "http://192.168.1.42:49152", ExpiresAt: time.Now().Add(time.Hour), MaxBytes: 1 << 20, MaxFiles: 2, MaxDownloads: 3, MaxStreams: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	_, file := seedSend(t, s)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	defer func() { close(release); wg.Wait() }()
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("GET", s.cfg.Origin+file, nil)
			s.Handler().ServeHTTP(heldWriter{httptest.NewRecorder(), entered, release}, r)
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("payload never reached response writer")
		}
	}
	requireStatus(t, request(s.Handler(), "GET", file, nil, nil), http.StatusTooManyRequests)
}

func TestConcurrentFileReservationsCannotExceedSessionFileBudget(t *testing.T) {
	s := sessionForTest(t, 1)
	id := resourceID(t, request(s.OwnerHandler(), "POST", "/api/v1/transfers", []byte(`{"max_downloads":1}`), nil))
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- request(s.OwnerHandler(), "POST", "/api/v1/transfers/"+id+"/files", nil, map[string]string{"Upload-Length": "64", "Tus-Resumable": "1.0.0"}).Code
		}()
	}
	wg.Wait()
	close(codes)
	successes := 0
	for code := range codes {
		if code == 201 {
			successes++
		} else if code != 403 && code != 400 {
			t.Fatalf("unexpected file admission status %d", code)
		}
	}
	if successes != 1 {
		t.Fatalf("file quota admitted %d reservations", successes)
	}
}
