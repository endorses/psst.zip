package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/endorses/psst.zip/backend/internal/reconcile"
)

// Callers hold the transfer lock across physical inspection and subsequent use.
func (s *Server) checkPayload(w http.ResponseWriter, r *http.Request, fileID string) bool {
	if err := reconcile.CheckFile(r.Context(), s.queries, s.fileStore, fileID); err != nil {
		if errors.Is(err, reconcile.ErrPayloadUnavailable) {
			policyError(w, http.StatusServiceUnavailable, "payload_unavailable", "This file's stored data is unavailable. Contact the sender or server administrator.")
		} else {
			policyError(w, http.StatusServiceUnavailable, "storage_check_failed", "Could not verify stored data. Retry when server storage is available.")
		}
		return false
	}
	return true
}

func (s *Server) checkTransferPayloads(w http.ResponseWriter, r *http.Request, transferID string) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	after := ""
	for {
		ids, err := s.queries.TransferFileIDs(ctx, transferID, after, 64)
		if err != nil {
			policyError(w, http.StatusServiceUnavailable, "storage_check_failed", "Could not verify stored data before publishing. Retry when server storage is available.")
			return false
		}
		for _, id := range ids {
			if !s.checkPayload(w, r, id) {
				return false
			}
			after = id
		}
		if len(ids) < 64 {
			return true
		}
	}
}

func (s *Server) getStorageChecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	status, err := s.queries.ReconciliationStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "storage check status is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, status)
}
