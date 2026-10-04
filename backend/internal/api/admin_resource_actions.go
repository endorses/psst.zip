package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func (s *Server) resourceExists(kind, id string) error {
	if kind == "transfer" {
		_, err := s.queries.GetTransfer(id)
		return err
	}
	_, err := s.queries.GetSlot(id)
	return err
}

func adminResourceActionError(w http.ResponseWriter, err error) {
	if rejectAdminMutation(w, err) {
		return
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "resource not found")
	case errors.Is(err, database.ErrCleanupNotEligible):
		policyError(w, http.StatusConflict, "cleanup_not_eligible", "This resource is still active. Revoke it before requesting cleanup.")
	default:
		writeError(w, http.StatusServiceUnavailable, "could not confirm resource cleanup; refresh its status before retrying")
	}
}

func (s *Server) revokeAdminResource(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	kind, id, ok := adminResourceIdentity(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeCreation(w, r, &request) {
		return
	}
	if err := s.resourceExists(kind, id); err != nil {
		adminResourceActionError(w, err)
		return
	}
	event := deletionAudit(r, kind, id, true)
	var err error
	if kind == "transfer" {
		err = s.queries.RevokeTransferAudited(id, event, adminActor(r))
	} else {
		err = s.queries.RevokeSlotQueued(id, event, adminActor(r))
	}
	if err != nil {
		adminResourceActionError(w, err)
		return
	}
	// Denial is committed before cancellation. Deletion is performed by the
	// bounded worker; accepting this action never claims physical deletion.
	if kind == "transfer" {
		store.CancelStreams(id)
	} else {
		store.CancelStreamScope(s.slotStreamScope(id))
		store.CancelStreams("slot:" + id)
		s.sseHub.Send(id, slotDeletedEvent)
	}
	s.writeAdminCleanupResult(w, kind, id)
}

func (s *Server) retryAdminResourceCleanup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	kind, id, ok := adminResourceIdentity(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeCreation(w, r, &request) {
		return
	}
	if err := s.queries.RequestResourceCleanup(kind, id, adminActor(r)); err != nil {
		adminResourceActionError(w, err)
		return
	}
	s.writeAdminCleanupResult(w, kind, id)
}

func (s *Server) writeAdminCleanupResult(w http.ResponseWriter, kind, id string) {
	status, err := s.queries.ResourceCleanup(kind, id)
	if err == nil {
		err = s.resourceExists(kind, id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"state": "removed", "type": kind, "id": id})
		return
	}
	if err != nil {
		adminResourceActionError(w, err)
		return
	}
	if status.State == "none" {
		// Payload-only cleanup can finish before this response while intentionally
		// retaining the resource's manifest and lifecycle metadata.
		writeJSON(w, http.StatusOK, map[string]any{"state": "complete", "type": kind, "id": id, "cleanup": status})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"state": "pending", "type": kind, "id": id, "cleanup": status})
}

func (s *Server) getCleanupOverview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	result, err := s.queries.CleanupOverview()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "cleanup status is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) slotStreamScope(id string) string {
	return s.queries.StreamNamespace() + "\x00slot:" + id
}
