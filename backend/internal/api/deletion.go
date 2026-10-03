package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/cleanup"
)

const slotDeletedEvent = `{"event":"slot_deleted"}`

func newDeleteToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func (s *Server) authorizeDeletion(w http.ResponseWriter, r *http.Request, hash []byte) bool {
	if len(hash) == 0 {
		if s.cfg.AllowLegacyDeletion {
			return true
		}
		writeError(w, http.StatusForbidden, "legacy link has no deletion token; this server does not allow legacy deletion")
		return false
	}
	authorization := strings.Fields(r.Header.Get("Authorization"))
	if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") {
		writeError(w, http.StatusForbidden, "deletion token required")
		return false
	}
	provided := sha256.Sum256([]byte(authorization[1]))
	if subtle.ConstantTimeCompare(provided[:], hash) != 1 {
		writeError(w, http.StatusForbidden, "invalid deletion token")
		return false
	}
	return true
}

func (s *Server) deleteTransfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	if !isValidUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}
	transfer, err := s.queries.GetTransfer(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "transfer not found")
		} else {
			writeError(w, http.StatusInternalServerError, "database error")
		}
		return
	}
	if !s.owns(r, "transfer", id) && !s.authorizeDeletion(w, r, transfer.DeleteTokenHash) {
		return
	}
	if err := cleanup.RemoveTransfer(s.queries, s.fileStore, id); err != nil {
		writeError(w, http.StatusServiceUnavailable, "deletion could not finish; retry to confirm cleanup")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSlot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "slotID")
	if !isValidUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid slot ID")
		return
	}
	slot, err := s.queries.GetSlot(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "slot not found")
		} else {
			writeError(w, http.StatusInternalServerError, "database error")
		}
		return
	}
	if !s.owns(r, "slot", id) && !s.authorizeDeletion(w, r, slot.DeleteTokenHash) {
		return
	}
	err = cleanup.RemoveSlot(s.queries, s.fileStore, id)
	// A failed disk cleanup still leaves the slot revoked. The stream also checks
	// the database periodically, covering other server instances and slow uploads.
	s.sseHub.Send(id, slotDeletedEvent)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "deletion could not finish; retry to confirm cleanup")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
