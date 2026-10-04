package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

// Fixed cardinality: no identity, address, URL, secret or request-derived key is
// retained. One minute's HTTP load produces at most four database rows.
type authenticationAuditClass int

const (
	auditLoginRejected authenticationAuditClass = iota
	auditPairingRejected
	auditFactorRejected
	auditAuthenticationThrottled
	authenticationAuditClasses
)
const maximumAuthenticationAuditCount int64 = 1_000_000_000

var authenticationAuditKinds = [...]string{"authentication.login_rejected", "authentication.pairing_rejected", "authentication.factor_rejected", "authentication.throttled"}

type authenticationAuditCounters struct {
	mu      sync.Mutex
	flushMu sync.Mutex
	counts  [authenticationAuditClasses]int64
}

func (s *Server) recordAuthenticationFailure(class authenticationAuditClass) {
	if class < 0 || class >= authenticationAuditClasses {
		return
	}
	s.authenticationAudit.mu.Lock()
	saturated := s.authenticationAudit.counts[class] == maximumAuthenticationAuditCount
	if !saturated {
		s.authenticationAudit.counts[class]++
	}
	s.authenticationAudit.mu.Unlock()
	if saturated && s.queries != nil {
		s.queries.MarkSecurityAuditDegraded()
	}
}

// Observe only exact authentication endpoints. This surrounds the admission
// guards so rejected floods remain visible without any per-request DB writes.
func (s *Server) observeAuthenticationFailures(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var class authenticationAuditClass
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/api/v1/auth/login":
			class = auditLoginRejected
		case "/api/v1/auth/pairings/redeem":
			class = auditPairingRejected
		case "/api/v1/admin/security/reauth", "/api/v1/admin/security/enrollment/confirm":
			class = auditFactorRejected
		default:
			next.ServeHTTP(w, r)
			return
		}
		observed := &authenticationAuditWriter{ResponseWriter: w}
		next.ServeHTTP(observed, r)
		code := observed.Header().Get("X-Psst-Error-Code")
		if observed.status == http.StatusTooManyRequests || observed.status == http.StatusServiceUnavailable && code == "request_limit" {
			s.recordAuthenticationFailure(auditAuthenticationThrottled)
		} else if observed.status == http.StatusUnauthorized && code != "administrator_factor_required" && code != "administrator_authentication_changed" {
			s.recordAuthenticationFailure(class)
		}
	})
}

type authenticationAuditWriter struct {
	http.ResponseWriter
	status int
}

func (w *authenticationAuditWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *authenticationAuditWriter) WriteHeader(status int) {
	if status >= 200 && w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *authenticationAuditWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

// RunSecurityAudit is owned by main's lifecycle, never a constructor goroutine.
// An abrupt stop can lose the pending interval; this is monitoring, not a
// tamper-proof or complete account of every request.
func (s *Server) RunSecurityAudit(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = s.FlushSecurityAudit(flushCtx)
			cancel()
		}
	}
}

func (s *Server) FlushSecurityAudit(ctx context.Context) error {
	return s.flushAuthenticationAudit(ctx, s.queries.RecordSecurityEvents)
}

func (s *Server) flushAuthenticationAudit(ctx context.Context, write func(context.Context, []database.SecurityEvent) error) error {
	if !s.authenticationAudit.flushMu.TryLock() {
		return errors.New("security audit flush already in progress")
	}
	defer s.authenticationAudit.flushMu.Unlock()
	s.authenticationAudit.mu.Lock()
	pending := s.authenticationAudit.counts
	s.authenticationAudit.counts = [authenticationAuditClasses]int64{}
	s.authenticationAudit.mu.Unlock()
	events := make([]database.SecurityEvent, 0, authenticationAuditClasses)
	for i, count := range pending {
		if count > 0 {
			events = append(events, database.SecurityEvent{Kind: authenticationAuditKinds[i], Origin: "system", TargetType: "authentication", Outcome: "rejected", Count: count})
		}
	}
	if len(events) == 0 {
		return nil
	}
	if err := write(ctx, events); err != nil {
		s.authenticationAudit.mu.Lock()
		for i, count := range pending {
			s.authenticationAudit.counts[i] = min(maximumAuthenticationAuditCount, s.authenticationAudit.counts[i]+count)
		}
		s.authenticationAudit.mu.Unlock()
		if s.queries != nil {
			s.queries.MarkSecurityAuditDegraded()
		}
		return err
	}
	return nil
}
