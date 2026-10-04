package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func finiteLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return min(value, 4096)
}

type streamAdmission struct {
	mu                                 sync.Mutex
	active                             int
	counts                             map[string]int
	total, account, ip, transfer, slot int
}

func newStreamAdmission(cfg config.Config) *streamAdmission {
	return &streamAdmission{counts: make(map[string]int), total: finiteLimit(cfg.MaxActiveStreams, 64), account: finiteLimit(cfg.MaxStreamsPerAccount, 4), ip: finiteLimit(cfg.MaxStreamsPerIP, 4), transfer: finiteLimit(cfg.MaxStreamsPerTransfer, 4), slot: finiteLimit(cfg.MaxStreamsPerSlot, 4)}
}

type streamScope struct {
	key   string
	limit int
}

func (a *streamAdmission) acquire(owner, ip, transfer, slot string, policies ...database.TrafficPolicy) (func(), bool) {
	total, account, ipLimit, transferLimit, slotLimit := a.total, a.account, a.ip, a.transfer, a.slot
	if len(policies) > 0 {
		p := policies[0]
		total = p.MaxActiveStreams
		account = p.MaxStreamsPerAccount
		ipLimit = p.MaxStreamsPerIP
		transferLimit = p.MaxStreamsPerTransfer
		slotLimit = p.MaxStreamsPerSlot
	}
	scopes := []streamScope{{"owner:" + owner, account}, {"ip:" + ip, ipLimit}}
	if transfer != "" {
		scopes = append(scopes, streamScope{"transfer:" + transfer, transferLimit})
	}
	if slot != "" {
		scopes = append(scopes, streamScope{"slot:" + slot, slotLimit})
	}
	a.mu.Lock()
	if a.active >= total {
		a.mu.Unlock()
		return nil, false
	}
	for _, scope := range scopes {
		if a.counts[scope.key] >= scope.limit {
			a.mu.Unlock()
			return nil, false
		}
	}
	a.active++
	for _, scope := range scopes {
		a.counts[scope.key]++
	}
	a.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.active--
			for _, scope := range scopes {
				a.counts[scope.key]--
				if a.counts[scope.key] == 0 {
					delete(a.counts, scope.key)
				}
			}
		})
	}, true
}
func streamBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	policyError(w, 429, "stream_limit", "too many active transfers; retry shortly")
}

// Recovery requests have a separate bounded lane. Public payload saturation
// cannot consume these slots before an administrator is authenticated.
func recoveryRequest(r *http.Request) bool {
	return r.Method == http.MethodDelete || strings.HasPrefix(r.URL.Path, "/api/v1/auth/") || strings.HasPrefix(r.URL.Path, "/api/v1/admin/") || r.URL.Path == "/api/v1/health"
}
func (s *Server) limitRequestRates(application, recovery *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		normal := rateLimitMiddleware(application, s.clientIP)(next)
		control := rateLimitMiddleware(recovery, s.clientIP)(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if recoveryRequest(r) {
				control.ServeHTTP(w, r)
			} else {
				normal.ServeHTTP(w, r)
			}
		})
	}
}
func (s *Server) admitRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lane := s.applicationRequests
		if recoveryRequest(r) {
			lane = s.recoveryRequests
		}
		select {
		case lane <- struct{}{}:
			defer func() { <-lane }()
		case <-r.Context().Done():
			return
		default:
			w.Header().Set("Retry-After", "1")
			policyError(w, 503, "request_limit", "server is busy; retry shortly")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) admitPayload(next http.Handler) http.Handler { return s.admitStream(next, false) }
func (s *Server) admitEvents(next http.Handler) http.Handler  { return s.admitStream(next, true) }
func (s *Server) admitStream(next http.Handler, events bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !events && !s.allowPublicTransfers(w) {
			return
		}
		transfer, slot := chi.URLParam(r, "transferID"), chi.URLParam(r, "slotID")
		var owner string
		var expires time.Time
		if events {
			resource, err := s.queries.GetSlot(slot)
			if err != nil {
				writeError(w, 404, "slot not found")
				return
			}
			expires = resource.ExpiresAt
			owner, err = s.queries.Owner("slot", slot)
			if err != nil {
				writeError(w, 500, "database error")
				return
			}
		} else {
			if !isValidUUID(transfer) {
				writeError(w, 400, "invalid transfer ID")
				return
			}
			resource, err := s.queries.GetTransfer(transfer)
			if err != nil {
				writeError(w, 404, "transfer not found")
				return
			}
			if resource.Status == "revoked" {
				incidentFailure(w, database.ErrResourceRevoked)
				return
			}
			expires = resource.ExpiresAt
			if resource.Status == "pending" && resource.PendingExpiresAt.Valid && resource.PendingExpiresAt.Time.Before(expires) {
				expires = resource.PendingExpiresAt.Time
			}
			owner, err = s.queries.Owner("transfer", transfer)
			if err != nil {
				writeError(w, 500, "database error")
				return
			}
			slots, err := s.queries.TransferSlotIDsContext(r.Context(), transfer)
			if err != nil {
				writeError(w, 500, "database error")
				return
			}
			if len(slots) > 0 {
				slot = slots[0]
				// Historical inbox children may predate transfer.owner_id. Attribute their
				// streams to the inbox owner so shutdown cancels those reads as well.
				inboxOwner, err := s.queries.Owner("slot", slot)
				if err != nil {
					writeError(w, 500, "database error")
					return
				}
				if inboxOwner != "" {
					owner = inboxOwner
				}
			}
		}
		if !time.Now().Before(expires) {
			writeError(w, 410, "link expired")
			return
		}
		if owner == "" {
			owner = "legacy"
		}
		trafficPolicy, policyVersion, err := s.queries.TrafficPolicyVersion(owner)
		if err != nil {
			trafficFailure(w, err)
			return
		}
		release, ok := s.admission.acquire(owner, s.clientIP(r), transfer, slot, trafficPolicy)
		if !ok {
			streamBusy(w)
			return
		}
		defer release()
		ctx, cancel := context.WithDeadline(r.Context(), expires)
		defer cancel()
		operationID := transfer
		if events {
			operationID = "slot:" + slot
		}
		scopes := []string{s.ownerScope(owner), s.queries.StreamNamespace() + "\x00streams"}
		if slot != "" {
			scopes = append(scopes, s.slotStreamScope(slot))
		}
		if !events {
			scopes = append(scopes, s.payloadScope())
		}
		unregister, err := store.RegisterScopedStream(operationID, cancel, scopes...)
		if err != nil {
			streamBusy(w)
			return
		}
		defer unregister()
		_, currentVersion, err := s.queries.TrafficPolicyVersion(owner)
		if err != nil {
			trafficFailure(w, err)
			return
		}
		if currentVersion != policyVersion {
			trafficFailure(w, database.ErrTrafficPolicyChanged)
			return
		}
		if !events && !s.allowPublicTransfers(w) {
			return
		}
		ctx = context.WithValue(ctx, trafficOwnerKey{}, owner)
		r = r.WithContext(ctx)
		callbackDone := make(chan struct{})
		stop := context.AfterFunc(ctx, func() { defer close(callbackDone); cancelRequestIO(r) })
		defer func() {
			if !stop() {
				<-callbackDone
			}
		}()
		r.Body = &cancellableBody{ReadCloser: r.Body, ctx: ctx}
		writer := &cancellableWriter{ResponseWriter: w, ctx: ctx}
		next.ServeHTTP(writer, r)
		// Drain the final response buffer before dropping admission and reader
		// protection. The outer deadline wrapper still owns connection cleanup.
		if ctx.Err() == nil {
			_ = http.NewResponseController(writer).Flush()
		}
	})
}

type cancellableBody struct {
	io.ReadCloser
	ctx context.Context
}

func (b *cancellableBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.ReadCloser.Read(p)
}

type cancellableWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w *cancellableWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *cancellableWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}
func (w *cancellableWriter) WriteHeader(status int) {
	if w.ctx.Err() == nil {
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *cancellableWriter) FlushError() error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *cancellableWriter) Flush() { _ = w.FlushError() }
