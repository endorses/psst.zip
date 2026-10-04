package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestAuthenticationAuditClassifiesWithoutRetainingRequestData(t *testing.T) {
	s := &Server{}
	cases := []struct {
		path   string
		status int
		code   string
	}{
		{"/api/v1/auth/login", 401, ""},
		{"/api/v1/auth/pairings/redeem", 401, ""},
		{"/api/v1/admin/security/reauth", 401, "administrator_factor_invalid"},
		{"/api/v1/admin/security/enrollment/confirm", 401, "administrator_factor_invalid"},
		{"/api/v1/auth/login", 401, "administrator_factor_required"},
		{"/api/v1/auth/login", 401, "administrator_authentication_changed"},
		{"/api/v1/auth/login", 429, ""},
		{"/api/v1/auth/login", 503, "request_limit"},
		{"/api/v1/auth/login", 503, "administrator_security_unavailable"},
		{"/api/v1/auth/login", 200, ""},
		{"/api/v1/auth/pairings/redeem", 403, "password_change_required"},
		{"/api/v1/auth/login/secret-path", 401, ""},
	}
	for _, tc := range cases {
		request := httptest.NewRequest("POST", tc.path+"?secret-query=must-not-appear", strings.NewReader(`{"username":"private-username","password":"private-password","code":"private-code"}`))
		request.Header.Set("Authorization", "Bearer private-token")
		request.Header.Set("Cookie", "private-cookie")
		request.RemoteAddr = "198.51.100.99:1234"
		s.observeAuthenticationFailures(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Psst-Error-Code", tc.code)
			w.WriteHeader(tc.status)
		})).ServeHTTP(httptest.NewRecorder(), request)
	}
	var events []database.SecurityEvent
	if err := s.flushAuthenticationAudit(context.Background(), func(_ context.Context, e []database.SecurityEvent) error { events = e; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Count != 1 || events[1].Count != 1 || events[2].Count != 2 || events[3].Count != 2 {
		t.Fatal(events)
	}
	raw, _ := json.Marshal(events)
	for _, secret := range []string{"private-", "secret-", "198.51.100", "username", "password", "cookie", "Bearer"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("request data retained: %s", raw)
		}
	}
	for _, event := range events {
		if event.ActorID != "" || event.TargetID != "" || event.Origin != "system" {
			t.Fatal(event)
		}
	}
}

func TestAuthenticationAuditAggregatesAdmissionAndPasswordRejection(t *testing.T) {
	s := &Server{recoveryRequests: make(chan struct{}, 1), applicationRequests: make(chan struct{}, 1), passwordWork: make(chan struct{}, 1)}
	s.recoveryRequests <- struct{}{}
	response := httptest.NewRecorder()
	s.Router().ServeHTTP(response, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader("{}")))
	if response.Code != 503 {
		t.Fatal(response.Code)
	}
	<-s.recoveryRequests
	response = httptest.NewRecorder()
	limiter := newRateLimiter(0.0001, 1)
	limited := s.observeAuthenticationFailures(s.limitRequestRates(limiter, limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	limited.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v1/auth/pairings/redeem", nil))
	limited.ServeHTTP(response, httptest.NewRequest("POST", "/api/v1/auth/pairings/redeem", nil))
	if response.Code != 429 {
		t.Fatal(response.Code)
	}
	s.passwordWork <- struct{}{}
	s.observeAuthenticationFailures(s.limitPasswordWork(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("expensive work admitted") }))).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v1/admin/security/reauth", nil))
	if s.authenticationAudit.counts[auditAuthenticationThrottled] != 3 {
		t.Fatal(s.authenticationAudit.counts)
	}
}

func TestAuthenticationAuditFailedFlushMergesConcurrentCounts(t *testing.T) {
	s := &Server{}
	for range 25 {
		s.recordAuthenticationFailure(auditLoginRejected)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.flushAuthenticationAudit(context.Background(), func(context.Context, []database.SecurityEvent) error {
			close(started)
			<-release
			return errors.New("unavailable")
		})
	}()
	<-started
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 100 {
				s.recordAuthenticationFailure(auditLoginRejected)
				s.recordAuthenticationFailure(auditPairingRejected)
			}
		}()
	}
	group.Wait()
	if err := s.flushAuthenticationAudit(context.Background(), func(context.Context, []database.SecurityEvent) error { t.Fatal("parallel writer"); return nil }); err == nil {
		t.Fatal("overlapping flush accepted")
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("failure swallowed")
	}
	if err := s.flushAuthenticationAudit(context.Background(), func(_ context.Context, events []database.SecurityEvent) error {
		if len(events) != 2 || events[0].Count != 2025 || events[1].Count != 2000 {
			t.Fatal(events)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.flushAuthenticationAudit(context.Background(), func(context.Context, []database.SecurityEvent) error { t.Fatal("empty flush wrote rows"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticationAuditCancellationAndSaturation(t *testing.T) {
	s := &Server{}
	s.authenticationAudit.counts[auditLoginRejected] = maximumAuthenticationAuditCount
	s.recordAuthenticationFailure(auditLoginRejected)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.flushAuthenticationAudit(ctx, func(ctx context.Context, _ []database.SecurityEvent) error { return ctx.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.authenticationAudit.counts[auditLoginRejected] != maximumAuthenticationAuditCount {
		t.Fatal(s.authenticationAudit.counts)
	}
	done := make(chan struct{})
	go func() { s.RunSecurityAudit(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector failed to stop")
	}
}
