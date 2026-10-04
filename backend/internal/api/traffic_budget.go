package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type trafficOwnerKey struct{}

func trafficFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, database.ErrTrafficPolicyChanged) {
		policyError(w, 409, "traffic_policy_changed", "Transfer controls changed. Retry explicitly to use the new limits.")
		return
	}
	var exhausted *database.TrafficExhausted
	if errors.As(err, &exhausted) {
		retry := exhausted.RetryAt.UTC().Format(time.RFC3339)
		w.Header().Set("Retry-After", strconv.FormatInt(max(1, int64(time.Until(exhausted.RetryAt).Seconds()+1)), 10))
		w.Header().Set("X-Psst-Retry-At", retry)
		w.Header().Set("X-Psst-Error-Code", "traffic_budget_exhausted")
		writeJSON(w, 429, map[string]string{"code": "traffic_budget_exhausted", "error": "Traffic budget exhausted. Retry after the next billing cycle.", "retry_at": retry})
		return
	}
	policyError(w, 503, "traffic_accounting_unavailable", "Traffic accounting is unavailable. Retry after the administrator restores accounting.")
}
func (s *Server) trafficSnapshot(w http.ResponseWriter, owner string) {
	snapshot, err := s.queries.TrafficBudgetSnapshot(owner, time.Now())
	if err != nil {
		trafficFailure(w, err)
		return
	}
	if s.trafficUnavailable.Load() {
		snapshot.State = "unavailable"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, snapshot)
}
func (s *Server) getTrafficPolicy(w http.ResponseWriter, r *http.Request) { s.trafficSnapshot(w, "") }
func (s *Server) accountTrafficUsage(w http.ResponseWriter, r *http.Request) {
	s.trafficSnapshot(w, identity(r).user.ID)
}
func (s *Server) getAccountTrafficPolicy(w http.ResponseWriter, r *http.Request) {
	owner := chi.URLParam(r, "userID")
	if _, err := s.queries.UserByID(owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "account not found")
		} else {
			trafficFailure(w, err)
		}
		return
	}
	s.trafficSnapshot(w, owner)
}
func (s *Server) updateTrafficPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.queries.TrafficPolicy()
	if err != nil {
		trafficFailure(w, err)
		return
	}
	if !decodeCreation(w, r, &p) {
		return
	}
	if err = p.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err = s.queries.SetTrafficPolicy(p); err != nil {
		trafficFailure(w, err)
		return
	}
	store.CancelStreamScope(s.queries.StreamNamespace() + "\x00streams")
	s.getTrafficPolicy(w, r)
}
func (s *Server) updateAccountTrafficPolicy(w http.ResponseWriter, r *http.Request) {
	// This PATCH alone deliberately accepts null to restore inheritance. Decode a
	// single keyed object token-by-token so duplicates/unknown fields stay invalid.
	defer r.Body.Close()
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, controlBodyLimit))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		writeError(w, 400, "invalid account traffic policy")
		return
	}
	if !d.More() {
		writeError(w, 400, "account_budget_bytes is required")
		return
	}
	t, err = d.Token()
	if err != nil || t != "account_budget_bytes" {
		writeError(w, 400, "invalid account traffic policy")
		return
	}
	var budget *int64
	if err = d.Decode(&budget); err != nil || d.More() {
		writeError(w, 400, "invalid account traffic policy")
		return
	}
	if _, err = d.Token(); err != nil || d.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid account traffic policy")
		return
	}
	owner := chi.URLParam(r, "userID")
	p, err := s.queries.TrafficPolicy()
	if err != nil {
		trafficFailure(w, err)
		return
	}
	if budget != nil && (*budget < 1 || *budget > p.ServerBudgetBytes) {
		writeError(w, 400, "account budget must be positive and no greater than server budget")
		return
	}
	if err = s.queries.SetAccountTrafficBudget(owner, budget); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "account not found")
		} else {
			trafficFailure(w, err)
		}
		return
	}
	store.CancelStreamScope(s.ownerScope(owner))
	s.getAccountTrafficPolicy(w, r)
}
func (s *Server) transferTrafficStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	if !isValidUUID(id) {
		writeError(w, 400, "invalid transfer ID")
		return
	}
	t, err := s.queries.GetTransfer(id)
	if err != nil {
		writeError(w, 404, "transfer not found")
		return
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner, err := s.trafficOwner(id)
		if err != nil {
			trafficFailure(w, err)
			return
		}
		s.resourceTrafficStatus(w, owner, t.Status, false, r.URL.Query().Get("direction") == "upload")
	})
	slots, err := s.queries.TransferSlotIDs(id)
	if err != nil {
		trafficFailure(w, err)
		return
	}
	if len(slots) > 0 && bearer(r) != "" && len(t.DeleteTokenHash) > 0 && subtle.ConstantTimeCompare(tokenHash(bearer(r)), t.DeleteTokenHash) == 1 {
		handler.ServeHTTP(w, r)
		return
	}
	s.requireTransferRead(handler).ServeHTTP(w, r)
}
func (s *Server) slotTrafficStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "slotID")
	if !isValidUUID(id) {
		writeError(w, 400, "invalid slot ID")
		return
	}
	slot, err := s.queries.GetSlot(id)
	if err != nil {
		writeError(w, 404, "slot not found")
		return
	}
	owner, err := s.queries.Owner("slot", id)
	if err != nil {
		trafficFailure(w, err)
		return
	}
	s.resourceTrafficStatus(w, owner, slot.Status, true, r.URL.Query().Get("direction") != "download")
}
func (s *Server) trafficOwner(id string) (string, error) {
	slots, err := s.queries.TransferSlotIDs(id)
	if err != nil {
		return "", err
	}
	if len(slots) > 0 {
		return s.queries.Owner("slot", slots[0])
	}
	return s.queries.Owner("transfer", id)
}
func (s *Server) resourceTrafficStatus(w http.ResponseWriter, owner, status string, submission, upload bool) {
	result := map[string]string{"state": "ready"}
	defer func() { w.Header().Set("Cache-Control", "no-store"); writeJSON(w, 200, result) }()
	if status == "revoked" {
		result["state"] = "revoked"
		return
	}
	if owner != "" && submission && upload {
		user, err := s.queries.UserByID(owner)
		if err != nil {
			result["state"] = "unavailable"
			return
		}
		if user.Disabled {
			result["state"] = "suspended"
			return
		}
	}
	incident, err := s.queries.IncidentState()
	if err != nil {
		result["state"] = "unavailable"
		return
	}
	if incident.PublicTransfersPaused {
		result["state"] = "paused"
		return
	}
	if owner == "" {
		owner = "legacy"
	}
	snapshot, err := s.queries.TrafficBudgetSnapshot(owner, time.Now())
	if err != nil || s.trafficUnavailable.Load() {
		result["state"] = "unavailable"
		return
	}
	if upload && snapshot.Policy.Basis == "outbound" {
		snapshot.State = "ready"
	}
	result["state"] = snapshot.State
	if snapshot.State == "exhausted" {
		result["retry_at"] = snapshot.Cycle.End.Format(time.RFC3339)
	}
}

// A global token bucket permits at most one lease of burst per direction. It
// buffers no payload and canceled waits do not spend tokens or create debt.
type trafficPacer struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func (p *trafficPacer) wait(ctx context.Context, n int64, rate int64) error {
	for {
		p.mu.Lock()
		now := time.Now()
		if p.last.IsZero() {
			p.tokens = float64(database.TrafficLeaseBytes)
		} else {
			p.tokens = min(float64(database.TrafficLeaseBytes), p.tokens+now.Sub(p.last).Seconds()*float64(rate))
		}
		p.last = now
		if p.tokens >= float64(n) {
			p.tokens -= float64(n)
			p.mu.Unlock()
			return nil
		}
		delay := time.Duration((float64(n) - p.tokens) / float64(rate) * float64(time.Second))
		p.mu.Unlock()
		timer := time.NewTimer(max(delay, time.Millisecond))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *trafficPacer) refund(n int) {
	if n <= 0 {
		return
	}
	p.mu.Lock()
	p.tokens = min(float64(database.TrafficLeaseBytes), p.tokens+float64(n))
	p.mu.Unlock()
}

type budgetFlow struct {
	server  *Server
	ctx     context.Context
	owner   string
	upload  bool
	lease   database.TrafficLease
	err     error
	rate    int64
	version string
	pacer   *trafficPacer
}

func (f *budgetFlow) reserve(want int64) error {
	if f.err != nil {
		return f.err
	}
	if f.lease.ID != "" {
		return nil
	}
	if f.server.trafficUnavailable.Load() {
		f.err = database.ErrTrafficAccounting
		return f.err
	}
	f.lease, f.err = f.server.queries.ReserveTraffic(f.owner, f.upload, want, time.Now(), f.version)
	return f.err
}
func (f *budgetFlow) settle(n int) error {
	if f.lease.ID == "" {
		return nil
	}
	err := f.server.queries.SettleTraffic(f.lease.ID, int64(n))
	f.lease = database.TrafficLease{}
	if err != nil {
		f.server.trafficUnavailable.Store(true)
		f.err = database.ErrTrafficAccounting
	}
	return err
}
func (f *budgetFlow) close() { _ = f.settle(0) }
func (f *budgetFlow) before(want int) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	if err := f.reserve(int64(want)); err != nil {
		return 0, err
	}
	n := min(want, int(f.lease.Bytes))
	if err := f.pacer.wait(f.ctx, int64(n), f.rate); err != nil {
		return 0, err
	}
	return n, nil
}

type budgetBody struct {
	io.ReadCloser
	flow      *budgetFlow
	remaining int64
}

func (b *budgetBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	if b.remaining > 0 && int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	if len(p) == 0 {
		return 0, nil
	}
	limit, err := b.flow.before(len(p))
	if err != nil {
		return 0, err
	}
	n, readErr := b.ReadCloser.Read(p[:limit])
	if b.remaining >= 0 {
		b.remaining -= int64(n)
	}
	b.flow.pacer.refund(limit - n)
	if err = b.flow.settle(n); err != nil {
		return n, database.ErrTrafficAccounting
	}
	return n, readErr
}

type budgetWriter struct {
	http.ResponseWriter
	flow     *budgetFlow
	status   int
	replaced bool
}

func (w *budgetWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *budgetWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	if w.flow.upload && w.flow.err != nil {
		w.replaced = true
		w.Header().Del("Content-Length")
		trafficFailure(w.ResponseWriter, w.flow.err)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *budgetWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.replaced {
		return len(p), nil
	}
	if w.flow.upload || w.status != 200 && w.status != 206 {
		return w.ResponseWriter.Write(p)
	}
	total := 0
	for len(p) > 0 {
		limit, err := w.flow.before(len(p))
		if err != nil {
			return total, err
		}
		n, writeErr := w.ResponseWriter.Write(p[:limit])
		w.flow.pacer.refund(limit - n)
		total += n
		p = p[n:]
		err = w.flow.settle(n)
		if err != nil {
			return total, database.ErrTrafficAccounting
		}
		if writeErr != nil {
			return total, writeErr
		}
		if n != limit {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}
func (s *Server) budgeted(next http.Handler, upload bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner, _ := r.Context().Value(trafficOwnerKey{}).(string)
		if owner == "" {
			trafficFailure(w, database.ErrTrafficAccounting)
			return
		}
		p, version, err := s.queries.TrafficPolicyVersion(owner)
		if err != nil {
			trafficFailure(w, err)
			return
		}
		f := &budgetFlow{server: s, ctx: r.Context(), owner: owner, upload: upload, version: version, rate: p.DownloadBytesPerSecond, pacer: &s.downloadPacer}
		if upload {
			f.rate = p.UploadBytesPerSecond
			f.pacer = &s.uploadPacer
		}
		defer f.close()
		// The initial durable lease precedes the handler and its download-attempt write.
		if err = f.reserve(database.TrafficLeaseBytes); err != nil {
			trafficFailure(w, err)
			return
		}
		if upload {
			r.Body = &budgetBody{r.Body, f, r.ContentLength}
		}
		bw := &budgetWriter{ResponseWriter: w, flow: f}
		next.ServeHTTP(bw, r)
		if !upload && f.err != nil && bw.status != 0 {
			panic(http.ErrAbortHandler)
		}
	})
}
