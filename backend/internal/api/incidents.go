package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/cleanup"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func (s *Server) payloadScope() string { return s.queries.StreamNamespace() + "\x00payload" }
func (s *Server) ownerScope(owner string) string {
	return s.queries.StreamNamespace() + "\x00owner:" + owner
}
func incidentFailure(w http.ResponseWriter, err error) bool {
	switch database.IncidentError(err) {
	case database.ErrTransfersPaused:
		policyError(w, 503, "public_transfers_paused", "Public transfers are paused. Retry after the administrator resumes transfers.")
	case database.ErrAccountDisabled:
		policyError(w, 403, "account_disabled", "This account is disabled.")
	case database.ErrResourceRevoked:
		policyError(w, 410, "resource_revoked", "This link has been revoked.")
	default:
		return false
	}
	return true
}
func (s *Server) allowPublicTransfers(w http.ResponseWriter) bool {
	state, err := s.queries.IncidentState()
	if err != nil {
		policyError(w, 503, "incident_state_unavailable", "Transfer controls are temporarily unavailable.")
		return false
	}
	if state.PublicTransfersPaused {
		incidentFailure(w, database.ErrTransfersPaused)
		return false
	}
	return true
}
func (s *Server) requirePublicTransfers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.allowPublicTransfers(w) {
			next.ServeHTTP(w, r)
		}
	})
}
func (s *Server) getIncidentState(w http.ResponseWriter, r *http.Request) {
	state, err := s.queries.IncidentState()
	if err != nil {
		writeError(w, 503, "could not read transfer controls")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, state)
}
func (s *Server) updateIncidentState(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Paused *bool `json:"public_transfers_paused"`
	}
	if !decodeCreation(w, r, &request) {
		return
	}
	if request.Paused == nil {
		writeError(w, 400, "public_transfers_paused is required")
		return
	}
	if err := s.queries.SetTransfersPaused(*request.Paused); err != nil {
		writeError(w, 503, "could not persist transfer controls")
		return
	}
	if *request.Paused {
		store.CancelStreamScope(s.payloadScope())
	}
	s.getIncidentState(w, r)
}
func (s *Server) shutdownAccount(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if !decodeCreation(w, r, &request) {
		return
	}
	id := chi.URLParam(r, "userID")
	result, err := s.queries.ShutdownAccount(id)
	if err != nil {
		if errors.Is(err, database.ErrLastAdmin) {
			writeError(w, 409, err.Error())
		} else if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "account not found")
		} else {
			writeError(w, 503, "account shutdown could not be persisted")
		}
		return
	}
	store.CancelStreamScope(s.ownerScope(id))
	// Cleanup's persistent revoked-row sweep is retryable and independent of this
	// HTTP request. It never gates deny-first revocation or administrator recovery.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}

// RunIncidentMonitor observes operator CLI changes without requiring a restart.
// A failed read cancels payload work conservatively; recovery routes stay open.
func (s *Server) RunIncidentMonitor(ctx context.Context) {
	go cleanup.RunIncidentCleanup(ctx, s.queries, s.fileStore)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := s.queries.IncidentState()
		if err != nil || state.PublicTransfersPaused {
			store.CancelStreamScope(s.payloadScope())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
