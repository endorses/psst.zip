package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// Exercise the production router and database; no authentication/admission
// middleware is substituted. Explicit fixture peers traverse the trusted resolver.
func admissionRouter(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	root := t.TempDir()
	db, err := openFixture(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := database.NewQueries(db)
	hash, err := bcrypt.GenerateFromPassword([]byte("disposable correct password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alice", "bob", "carol", "operator"} {
		role := "user"
		if name == "operator" {
			role = "admin"
		}
		if err := q.CreateUser(database.User{ID: name, Username: name, Role: role, PasswordHash: hash}, false); err != nil {
			t.Fatal(err)
		}
	}
	files, err := store.NewDiskStore(filepath.Join(root, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(config.Config{
		PublicURL: "https://transfer.example", TrustedProxies: "127.0.0.1/32",
		CORSOrigin: "https://transfer.example", StoragePath: filepath.Join(root, "payloads"), DBPath: filepath.Join(root, "state.db"),
		RateLimitGlobal: 1000, RateLimitBurst: 1000, RateLimitCreation: 1000, RateLimitCreationBurst: 1000,
		DefaultExpiry: time.Hour, MaxManifestSize: 1 << 20, MaxFilesPerTransfer: 100,
	}, q, files)
	return s, s.Router()
}

func admissionRequest(method, path, peer string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, "https://transfer.example/api/v1"+path, body)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", peer)
	r.Header.Set("Origin", "https://transfer.example")
	r.Header.Set("Content-Type", "application/json")
	return r
}

func admissionLogin(router http.Handler, peer, username, password string) *httptest.ResponseRecorder {
	data, _ := json.Marshal(map[string]string{"username": username, "password": password, "session_type": "web"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, admissionRequest("POST", "/auth/login", peer, strings.NewReader(string(data))))
	return w
}

func requireAdmissionStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("expected HTTP %d, received %d: %s", status, w.Code, w.Body.String())
	}
}

func TestLoginAccountThrottleAcrossResolvedAddresses(t *testing.T) {
	s, router := admissionRouter(t)
	variants := []string{"alice", " ALICE ", "Alice", "\talice\t"}
	var denied string
	for i := 0; i < 10; i++ {
		w := admissionLogin(router, fmt.Sprintf("192.0.2.%d", i+1), variants[i%len(variants)], "incorrect")
		requireAdmissionStatus(t, w, 401)
		if i == 0 {
			denied = w.Body.String()
		}
		if w.Body.String() != denied || w.Header().Get("Set-Cookie") != "" {
			t.Fatal("normalized failed login changed disclosure or issued a session")
		}
	}
	w := admissionLogin(router, "192.0.2.11", "alice", "disposable correct password")
	requireAdmissionStatus(t, w, 429)
	if w.Header().Get("Retry-After") != "30" || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("account throttle lacks finite retry guidance or issued a session")
	}
	// Another account sharing the final peer is unaffected by Alice's bucket.
	requireAdmissionStatus(t, admissionLogin(router, "192.0.2.11", "bob", "disposable correct password"), 200)
	if len(s.loginAccounts.buckets) != 2 {
		t.Fatal("case or whitespace variants bypassed normalized-account tracking")
	}
}

func TestLoginThrottleNormalizationAndRecovery(t *testing.T) {
	s, router := admissionRouter(t)
	for i := 0; i < 10; i++ {
		requireAdmissionStatus(t, admissionLogin(router, fmt.Sprintf("192.0.2.%d", i+1), " Alice ", "incorrect"), 401)
	}
	requireAdmissionStatus(t, admissionLogin(router, "192.0.2.20", "ALICE", "incorrect"), 429)
	// Advance only the stored bucket clock: no sleeps, rate changes or replacement
	// limiter. The next real login must replenish using the production algorithm.
	s.loginAccounts.mu.Lock()
	for _, bucket := range s.loginAccounts.buckets {
		bucket.lastTime = time.Now().Add(-31 * time.Second)
	}
	s.loginAccounts.mu.Unlock()
	requireAdmissionStatus(t, admissionLogin(router, "192.0.2.21", "alice", "disposable correct password"), 200)
	requireAdmissionStatus(t, admissionLogin(router, "192.0.2.22", "alice", "incorrect"), 429)
}

func TestLoginSharedNATAccountsAndNonEnumeratingFailures(t *testing.T) {
	_, router := admissionRouter(t)
	for _, user := range []string{"alice", "bob", "carol"} {
		requireAdmissionStatus(t, admissionLogin(router, "198.51.100.1", user, "disposable correct password"), 200)
	}
	known := admissionLogin(router, "198.51.100.1", "alice", "incorrect")
	unknown := admissionLogin(router, "198.51.100.1", "nonexistent", "incorrect")
	requireAdmissionStatus(t, known, 401)
	requireAdmissionStatus(t, unknown, 401)
	if known.Body.String() != unknown.Body.String() || unknown.Header().Get("Set-Cookie") != "" {
		t.Fatal("failed authentication enumerates accounts or issues sessions")
	}
	for i := 5; i < 10; i++ {
		requireAdmissionStatus(t, admissionLogin(router, "198.51.100.1", "bob", "incorrect"), 401)
	}
	// Distributed usernames cannot bypass the separate per-address login bucket.
	requireAdmissionStatus(t, admissionLogin(router, "198.51.100.1", "carol", "disposable correct password"), 429)
	requireAdmissionStatus(t, admissionLogin(router, "198.51.100.2", "carol", "disposable correct password"), 200)
}

type admissionBody struct {
	io.Reader
	reads       atomic.Int32
	entered     chan struct{}
	release     <-chan struct{}
	ctx         context.Context
	once        sync.Once
	panicOnRead bool
}

func (b *admissionBody) Read(p []byte) (int, error) {
	b.reads.Add(1)
	if b.panicOnRead {
		panic("disposable admission test panic")
	}
	if b.entered != nil {
		b.once.Do(func() { close(b.entered) })
		select {
		case <-b.release:
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
	return b.Reader.Read(p)
}

func (b *admissionBody) Close() error { return nil }

func TestLoginFloodPreservesControlResponsiveness(t *testing.T) {
	s, router := admissionRouter(t)
	admin := admissionLogin(router, "203.0.113.1", "operator", "disposable correct password")
	requireAdmissionStatus(t, admin, 200)
	cookies := admin.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("fixture administrator did not receive a web session")
	}
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	done := make(chan *httptest.ResponseRecorder, cap(s.passwordWork))
	for i := 0; i < cap(s.passwordWork); i++ {
		body := &admissionBody{Reader: strings.NewReader(`{"username":"alice","password":"incorrect","session_type":"web"}`), entered: make(chan struct{}), release: release, ctx: context.Background()}
		r := admissionRequest("POST", "/auth/login", fmt.Sprintf("192.0.2.%d", i+1), body)
		go func() { w := httptest.NewRecorder(); router.ServeHTTP(w, r); done <- w }()
		select {
		case <-body.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("login did not enter its bounded password-work slot")
		}
	}
	// Saturation is deterministic: all four production slots hold real requests.
	probe := &admissionBody{Reader: strings.NewReader("{}")}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, admissionRequest("POST", "/auth/login", "192.0.2.99", probe))
	requireAdmissionStatus(t, w, 429)
	if probe.reads.Load() != 0 || w.Header().Get("Retry-After") == "" {
		t.Fatal("password saturation consumed a body or omitted retry guidance")
	}
	for _, path := range []string{"/health", "/admin/resource-policy"} {
		r := admissionRequest("GET", path, "203.0.113.1", nil)
		r.AddCookie(cookies[0])
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		requireAdmissionStatus(t, w, 200)
	}
	unblock()
	for i := 0; i < cap(s.passwordWork); i++ {
		select {
		case w := <-done:
			requireAdmissionStatus(t, w, 401)
		case <-time.After(5 * time.Second):
			t.Fatal("login did not release its work slot")
		}
	}
	if len(s.passwordWork) != 0 || len(s.recoveryRequests) != 0 {
		t.Fatal("login flood leaked admission capacity")
	}
	requireAdmissionStatus(t, admissionLogin(router, "192.0.2.100", "bob", "disposable correct password"), 200)
}

func TestGlobalAdmissionRejectsBeforeAuthenticationAndBody(t *testing.T) {
	s, router := admissionRouter(t)
	q := s.queries
	s.applicationRequests = make(chan struct{}, 1)
	s.applicationRequests <- struct{}{}
	// A downstream authentication lookup would panic. The real router's guard
	// must reject first; its Recoverer would turn a misordering into HTTP 500.
	s.queries = nil
	body := &admissionBody{Reader: strings.NewReader("{}"), panicOnRead: true}
	r := admissionRequest("POST", "/transfers", "192.0.2.1", body)
	r.Header.Set("Authorization", "Bearer disposable-unverified-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	s.queries = q
	requireAdmissionStatus(t, w, 503)
	if body.reads.Load() != 0 || w.Header().Get("X-Psst-Error-Code") != "request_limit" {
		t.Fatal("global admission did not reject before authentication/body work")
	}
	// The separate recovery lane still handles health through this same router.
	health := httptest.NewRecorder()
	router.ServeHTTP(health, admissionRequest("GET", "/health", "192.0.2.1", nil))
	requireAdmissionStatus(t, health, 200)
	<-s.applicationRequests
	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, admissionRequest("GET", "/config", "192.0.2.1", nil))
	requireAdmissionStatus(t, ready, 200)
	if len(s.applicationRequests) != 0 || len(s.recoveryRequests) != 0 {
		t.Fatal("request admission did not release after router completion")
	}
}

func TestRequestAdmissionReleasesAfterCancellationAndPanic(t *testing.T) {
	for _, path := range []string{"/auth/login", "/transfers"} {
		t.Run(path, func(t *testing.T) {
			s, router := admissionRouter(t)
			login := admissionLogin(router, "192.0.2.100", "alice", "disposable correct password")
			requireAdmissionStatus(t, login, 200)
			cookies := login.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatal("fixture user did not receive a session")
			}
			request := func(peer string, body io.Reader) *http.Request {
				r := admissionRequest("POST", path, peer, body)
				r.AddCookie(cookies[0])
				return r
			}
			checkReleased := func() {
				t.Helper()
				if len(s.passwordWork) != 0 || len(s.recoveryRequests) != 0 || len(s.applicationRequests) != 0 {
					t.Fatal("error/panic/cancellation leaked admission capacity")
				}
			}
			for _, body := range []*admissionBody{
				{Reader: strings.NewReader("{}"), panicOnRead: true},
				{Reader: strings.NewReader("not JSON")},
			} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, request("192.0.2.1", body))
				want := 400
				if body.panicOnRead {
					want = 500
				}
				requireAdmissionStatus(t, w, want)
				checkReleased()
			}
			ctx, cancel := context.WithCancel(context.Background())
			body := &admissionBody{Reader: strings.NewReader("{}"), entered: make(chan struct{}), release: make(chan struct{}), ctx: ctx}
			r := request("192.0.2.2", body).WithContext(ctx)
			done := make(chan struct{})
			defer cancel()
			go func() { defer close(done); router.ServeHTTP(httptest.NewRecorder(), r) }()
			select {
			case <-body.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not enter the body reader")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled request did not return")
			}
			checkReleased()
			if path == "/transfers" {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, request("192.0.2.3", strings.NewReader("{}")))
				requireAdmissionStatus(t, w, 201)
			} else {
				requireAdmissionStatus(t, admissionLogin(router, "192.0.2.3", "alice", "disposable correct password"), 200)
			}
			checkReleased()
		})
	}
}
