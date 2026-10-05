package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/database"
)

type inboxMembershipResponse struct {
	SlotID             string `json:"slot_id"`
	TransferID         string `json:"transfer_id"`
	ReceiveProtocol    int    `json:"receive_protocol"`
	RecipientPublicKey string `json:"recipient_public_key"`
}

func (s *Server) inboxTransferMembership(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	slotID, transferID := chi.URLParam(r, "slotID"), chi.URLParam(r, "transferID")
	if !isValidUUID(slotID) || !isValidUUID(transferID) {
		writeError(w, http.StatusBadRequest, "invalid slot or transfer ID")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	entry, err := s.queries.InboxTransferMembershipContext(ctx, slotID, transferID, identity(r).user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "inbox submission not found")
		} else {
			writeError(w, http.StatusServiceUnavailable, "inbox membership unavailable")
		}
		return
	}
	if entry.SlotStatus == "revoked" || entry.TransferStatus == "revoked" {
		incidentFailure(w, database.ErrResourceRevoked)
		return
	}
	now := time.Now()
	if !now.Before(entry.SlotExpiresAt) || !now.Before(entry.TransferExpiresAt) ||
		(entry.TransferStatus == "pending" && entry.PendingExpiresAt.Valid && !now.Before(entry.PendingExpiresAt.Time)) {
		policyError(w, http.StatusGone, "link_expired", "inbox submission expired")
		return
	}
	parents, err := s.queries.TransferSlotIDsContext(ctx, transferID)
	if err != nil || len(parents) != 1 || parents[0] != slotID {
		writeError(w, http.StatusServiceUnavailable, "inbox membership unavailable")
		return
	}
	// A corrupt stored key cannot turn this small response into an unbounded
	// control read or a different receive protocol binding.
	key, keyErr := base64.RawURLEncoding.Strict().DecodeString(entry.RecipientPublicKey)
	if (entry.ReceiveProtocol != 1 || entry.RecipientPublicKey != "") &&
		(entry.ReceiveProtocol != 2 || keyErr != nil || len(key) != 32 || bytes.Equal(key, make([]byte, 32)) || base64.RawURLEncoding.EncodeToString(key) != entry.RecipientPublicKey) {
		writeError(w, http.StatusInternalServerError, "invalid inbox receive policy")
		return
	}
	writeJSON(w, http.StatusOK, inboxMembershipResponse{
		SlotID: entry.SlotID, TransferID: entry.TransferID,
		ReceiveProtocol: entry.ReceiveProtocol, RecipientPublicKey: entry.RecipientPublicKey,
	})
}
