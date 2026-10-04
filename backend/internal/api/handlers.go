package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

// --- Transfer handlers ---

func (s *Server) createTransfer(w http.ResponseWriter, r *http.Request) {
	var req CreateTransferRequest
	if !decodeCreation(w, r, &req) {
		return
	}

	if !validTransferPolicy(w, req) {
		return
	}
	expiry, ok := s.effectiveRetention(w, req.ExpiresInSeconds, s.cfg.DefaultExpiry)
	if !ok {
		return
	}

	token, hash, err := newDeleteToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate deletion token")
		return
	}

	id := uuid.New().String()
	expiresAt := time.Now().Add(expiry)

	if err := s.queries.CreateTransfer(id, expiresAt, req.MaxDownloads, hash, identity(r).user.ID); err != nil {
		if incidentFailure(w, err) || resourceFailure(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create transfer")
		return
	}

	writeJSON(w, http.StatusCreated, CreateTransferResponse{
		ID:          id,
		ExpiresAt:   expiresAt,
		DeleteToken: token,
	})
}

func (s *Server) getTransfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	if !isValidUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}

	t, err := s.queries.GetTransfer(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	if t.Status == "revoked" {
		incidentFailure(w, database.ErrResourceRevoked)
		return
	}
	if !time.Now().Before(t.ExpiresAt) || (t.Status == "pending" && t.PendingExpiresAt.Valid && !time.Now().Before(t.PendingExpiresAt.Time)) {
		writeError(w, http.StatusGone, "transfer expired or revoked")
		return
	}
	fileCount, totalSize, _ := s.queries.FileCountAndSize(id)
	hasManifest, _ := s.queries.HasManifest(id)

	resp := TransferResponse{
		ID:            t.ID,
		Status:        t.Status,
		FileCount:     fileCount,
		TotalSize:     totalSize,
		HasManifest:   hasManifest,
		ExpiresAt:     t.ExpiresAt,
		MaxDownloads:  t.MaxDownloads,
		DownloadCount: t.DownloadCount,
		CreatedAt:     t.CreatedAt,
	}
	resp.Files, err = s.filePolicy(t)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	if t.CompletedAt.Valid {
		resp.CompletedAt = &t.CompletedAt.Time
	}

	if t.DownloadedAt.Valid {
		resp.DownloadedAt = &t.DownloadedAt.Time
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) completeTransfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	unlock, ok := acquireResource(w, r, id, false)
	if !ok {
		return
	}
	defer unlock()
	if !isValidUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}

	if s.activeTransfer(w, id, true) == nil {
		return
	}
	protocol, err := s.queries.TransferReceiveProtocol(id)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	if protocol == 2 {
		manifest, err := s.queries.GetManifest(id)
		if errors.Is(err, database.ErrManifestTooLarge) {
			policyError(w, http.StatusRequestEntityTooLarge, "unsupported_manifest", err.Error())
			return
		}
		if err != nil {
			writeError(w, 400, "receive manifest required")
			return
		}
		if !s.validateReceiveManifest(w, id, manifest) {
			return
		}
	}
	if err := s.queries.CompleteTransfer(id); err != nil {
		if incidentFailure(w, err) {
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	slotIDs, err := s.queries.TransferSlotIDs(id)
	if err == nil {
		for _, slotID := range slotIDs {
			s.sseHub.Send(slotID, fmt.Sprintf(`{"event":"transfer_complete","transfer_id":"%s"}`, id))
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uploadManifest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	if !isValidUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}

	if s.activeTransfer(w, id, true) == nil {
		return
	}

	// Operators may lower the client-compatible ceiling, but cannot raise it.
	maxSize := s.cfg.MaxManifestSize
	if maxSize <= 0 || maxSize > database.MaxManifestBytes {
		maxSize = database.MaxManifestBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		var limit *http.MaxBytesError
		var timeout net.Error
		if errors.As(err, &limit) {
			writeError(w, http.StatusRequestEntityTooLarge, "manifest too large")
		} else if errors.As(err, &timeout) && timeout.Timeout() {
			writeError(w, http.StatusRequestTimeout, "manifest upload timed out")
		} else {
			writeError(w, http.StatusBadRequest, "could not read manifest")
		}
		return
	}
	defer r.Body.Close()

	unlock, ok := acquireResource(w, r, id, false)
	if !ok {
		return
	}
	defer unlock()
	if s.activeTransfer(w, id, true) == nil {
		return
	}

	if !s.validateReceiveManifest(w, id, data) {
		return
	}
	if err := s.queries.SaveManifest(id, data); err != nil {
		if incidentFailure(w, err) || resourceFailure(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save manifest")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) downloadManifest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	if !isValidUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}

	if s.downloadableTransfer(w, id) == nil {
		return
	}
	data, err := s.queries.GetManifest(id)
	if errors.Is(err, database.ErrManifestTooLarge) {
		policyError(w, http.StatusRequestEntityTooLarge, "unsupported_manifest", err.Error())
		return
	}
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "manifest not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "transferID")
	fileID := chi.URLParam(r, "fileID")
	if !isValidUUID(transferID) || !isValidUUID(fileID) {
		writeError(w, http.StatusBadRequest, "invalid ID format")
		return
	}

	unlock, ok := acquireResource(w, r, transferID, false)
	if !ok {
		return
	}
	defer unlock()
	t := s.downloadableTransfer(w, transferID)
	if t == nil {
		return
	}

	f, err := s.queries.GetFile(fileID)
	if err != nil || f.TransferID != transferID {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}

	if !f.UploadComplete {
		writeError(w, http.StatusConflict, "file upload not complete")
		return
	}

	if t.MaxDownloads > 0 && f.DownloadCount >= t.MaxDownloads {
		policyError(w, http.StatusGone, "download_limit", "download limit reached")
		return
	}

	storageKey := fmt.Sprintf("%s/%s", transferID, fileID)
	rc, err := s.fileStore.Load(storageKey)
	if err != nil {
		// Another request can consume the last allowance and trigger payload cleanup
		// between our metadata read and opening the blob. Keep the quota response.
		if t.MaxDownloads > 0 {
			latest, lookupErr := s.queries.GetFile(fileID)
			if lookupErr == nil && latest.DownloadCount >= t.MaxDownloads {
				policyError(w, http.StatusGone, "download_limit", "download limit reached")
				return
			}
		}

		writeError(w, http.StatusInternalServerError, "failed to load file")
		return
	}
	readerDone, err := store.AcquireReader(transferID)
	if err != nil {
		rc.Close()
		writeError(w, 503, "resource is busy; retry later")
		return
	}
	defer readerDone()
	defer rc.Close()
	if r.Context().Err() != nil {
		return
	}

	allowed, err := s.queries.ReserveFileDownload(transferID, fileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count download")
		return
	}
	if !allowed {
		policyError(w, http.StatusGone, "download_limit", "download limit reached")
		return
	}

	unlock()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, rc)
}

// --- tus wrappers ---

func (s *Server) tusCreate(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "transferID")
	unlock, ok := acquireResource(w, r, transferID, false)
	if !ok {
		return
	}
	defer unlock()
	if !isValidUUID(transferID) {
		writeError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}

	// Validate the transfer exists.
	if s.activeTransfer(w, transferID, true) == nil {
		return
	}

	// Validate max files per transfer.
	if s.cfg.MaxFilesPerTransfer > 0 {
		count, err := s.queries.FileCount(transferID)
		if err == nil && count >= s.cfg.MaxFilesPerTransfer {
			writeError(w, http.StatusBadRequest, "maximum file count reached for this transfer")
			return
		}
	}

	if !s.allowUploadSize(w, r) {
		return
	}
	s.tusH.ServeCreate(w, r, transferID)
}

func (s *Server) tusHead(w http.ResponseWriter, r *http.Request) {
	transferID, fileID := chi.URLParam(r, "transferID"), chi.URLParam(r, "fileID")
	if !s.validUpload(w, transferID, fileID, false) {
		return
	}
	s.tusH.ServeOffset(w, r, fileID)
}

func (s *Server) tusPatch(w http.ResponseWriter, r *http.Request) {
	r.Body = &capacityBody{ReadCloser: r.Body, queries: s.queries}
	if err := s.queries.CheckWriteCapacity(); err != nil {
		if !resourceFailure(w, err) {
			writeError(w, 500, "could not check storage capacity")
		}
		return
	}
	transferID, fileID := chi.URLParam(r, "transferID"), chi.URLParam(r, "fileID")
	unlock, ok := acquireResource(w, r, transferID, false)
	if !ok {
		return
	}
	defer unlock()
	if !s.validUpload(w, transferID, fileID, true) {
		return
	}
	s.tusH.ServePatch(w, r, fileID, fmt.Sprintf("%s/%s", transferID, fileID))
}

func (s *Server) validUpload(w http.ResponseWriter, transferID, fileID string, mutable bool) bool {
	if !isValidUUID(transferID) || !isValidUUID(fileID) {
		writeError(w, http.StatusBadRequest, "invalid ID format")
		return false
	}
	if s.activeTransfer(w, transferID, mutable) == nil {
		return false
	}
	f, err := s.queries.GetFile(fileID)
	if err != nil || f.TransferID != transferID {
		writeError(w, http.StatusNotFound, "file not found")
		return false
	}
	return true
}

func (s *Server) activeTransfer(w http.ResponseWriter, id string, mutable bool) *database.Transfer {
	t, err := s.queries.GetTransfer(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "transfer not found")
		return nil
	}
	if t.Status == "revoked" {
		incidentFailure(w, database.ErrResourceRevoked)
		return nil
	}
	if !time.Now().Before(t.ExpiresAt) || (t.Status == "pending" && t.PendingExpiresAt.Valid && !time.Now().Before(t.PendingExpiresAt.Time)) {
		writeError(w, http.StatusGone, "transfer expired or revoked")
		return nil
	}
	if mutable && t.Status != "pending" {
		writeError(w, http.StatusConflict, "transfer is complete")
		return nil
	}
	return t
}

func (s *Server) downloadableTransfer(w http.ResponseWriter, id string) *database.Transfer {
	t := s.activeTransfer(w, id, false)
	if t != nil && t.Status != "complete" {
		writeError(w, http.StatusConflict, "transfer not complete")
		return nil
	}
	return t
}

// --- Slot handlers ---

func (s *Server) createSlot(w http.ResponseWriter, r *http.Request) {
	var req CreateSlotRequest
	if !decodeCreation(w, r, &req) {
		return
	}

	if !validSlotPolicy(w, &req) {
		return
	}
	expiry, ok := s.effectiveRetention(w, req.ExpiresInSeconds, s.cfg.DefaultExpiry)
	if !ok {
		return
	}

	maxExpiry := s.cfg.MaxSlotExpiry
	if maxExpiry <= 0 {
		maxExpiry = 168 * time.Hour
	}
	if req.ExpiresInSeconds < 0 || (req.ExpiresInSeconds > 0 && int64(req.ExpiresInSeconds) > int64(maxExpiry/time.Second)) {
		writeError(w, 400, "receive link expiry exceeds server limit")
		return
	}
	if expiry > maxExpiry {
		expiry = maxExpiry
	}

	token, hash, err := newDeleteToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate deletion token")
		return
	}

	id := uuid.New().String()
	expiresAt := time.Now().Add(expiry)

	if err := s.queries.CreateReceiveSlot(id, expiresAt, hash, identity(r).user.ID, req.ReceiveProtocol, req.RecipientPublicKey, req.MaxFiles); err != nil {
		if incidentFailure(w, err) || resourceFailure(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create slot")
		return
	}

	writeJSON(w, http.StatusCreated, CreateSlotResponse{
		ID:          id,
		ExpiresAt:   expiresAt,
		DeleteToken: token,
	})
}

func (s *Server) getSlot(w http.ResponseWriter, r *http.Request) {
	slotID := chi.URLParam(r, "slotID")
	if !isValidUUID(slotID) {
		writeError(w, http.StatusBadRequest, "invalid slot ID")
		return
	}

	slot, err := s.queries.GetSlot(slotID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "slot not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	if slot.Status == "revoked" {
		incidentFailure(w, database.ErrResourceRevoked)
		return
	}
	if !time.Now().Before(slot.ExpiresAt) {
		writeError(w, http.StatusGone, "slot expired or revoked")
		return
	}
	transfers, _ := s.queries.ListSlotTransfers(slotID)

	infos := make([]SlotTransferInfo, 0, len(transfers))
	for _, t := range transfers {
		if t.Status == "revoked" || !time.Now().Before(t.ExpiresAt) || (t.Status == "pending" && t.PendingExpiresAt.Valid && !time.Now().Before(t.PendingExpiresAt.Time)) {
			continue
		}
		fc, _, _ := s.queries.FileCountAndSize(t.ID)
		infos = append(infos, SlotTransferInfo{
			TransferID: t.ID,
			Status:     t.Status,
			FileCount:  fc,
		})
	}

	response := SlotResponse{
		ID:        slot.ID,
		Status:    slot.Status,
		Transfers: infos,
		ExpiresAt: slot.ExpiresAt,
		CreatedAt: slot.CreatedAt,
	}
	if err := s.slotPolicy(&response, slot); err != nil {
		writeError(w, 500, "could not read inbox counters")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) createSlotTransfer(w http.ResponseWriter, r *http.Request) {
	slotID := chi.URLParam(r, "slotID")
	if !isValidUUID(slotID) {
		writeError(w, http.StatusBadRequest, "invalid slot ID")
		return
	}

	var req CreateTransferRequest
	if !decodeCreation(w, r, &req) {
		return
	}
	if !validTransferPolicy(w, req) {
		return
	}
	if req.MaxDownloads != 0 {
		policyError(w, 400, "invalid_link_policy", "receive submissions cannot limit owner downloads")
		return
	}
	unlock, ok := acquireResource(w, r, slotID, true)
	if !ok {
		return
	}
	defer unlock()
	slot, err := s.queries.GetSlot(slotID)
	if err != nil {
		writeError(w, http.StatusNotFound, "slot not found")
		return
	}

	if slot.Status == "revoked" {
		incidentFailure(w, database.ErrResourceRevoked)
		return
	}
	if !time.Now().Before(slot.ExpiresAt) {
		writeError(w, http.StatusGone, "slot expired or revoked")
		return
	}
	if slot.ReceiveProtocol != 2 {
		policyError(w, 403, "legacy_receive_disabled", "legacy receive links cannot accept submissions; create a new receive link")
		return
	}
	if slot.MaxFiles > 0 && slot.ReservedFiles >= int64(slot.MaxFiles) {
		policyError(w, 403, "receive_file_limit", "receive file allowance exhausted")
		return
	}
	if !s.slotOwnerActive(w, slotID) {
		return
	}
	expiry, ok := s.effectiveRetention(w, req.ExpiresInSeconds, s.cfg.DefaultExpiry)
	if !ok {
		return
	}

	token, hash, err := newDeleteToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate deletion token")
		return
	}

	id := uuid.New().String()
	expiresAt := time.Now().Add(expiry)

	if expiresAt.After(slot.ExpiresAt) {
		expiresAt = slot.ExpiresAt
	}
	if err := s.queries.CreateSlotTransfer(slotID, id, expiresAt, req.MaxDownloads, hash, s.cfg.MaxSlotTransfers); err != nil {
		if incidentFailure(w, err) || resourceFailure(w, err) {
			return
		}
		if err == database.ErrSlotFileQuota {
			policyError(w, 403, "receive_file_limit", err.Error())
			return
		}
		if err == database.ErrSlotQuota {
			policyError(w, http.StatusForbidden, "receive_batch_limit", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create transfer in slot")
		return
	}

	// Notify SSE listeners.
	s.sseHub.Send(slotID, fmt.Sprintf(`{"event":"transfer_created","transfer_id":"%s"}`, id))

	writeJSON(w, http.StatusCreated, CreateTransferResponse{
		ID:          id,
		ExpiresAt:   expiresAt,
		DeleteToken: token,
	})
}

// acknowledgeDownload accepts a recipient's report that every file was
// downloaded and decrypted. It is not proof of local filesystem persistence.
func (s *Server) acknowledgeDownload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	if !isValidUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}
	if s.downloadableTransfer(w, id) == nil {
		return
	}
	acknowledged, err := s.queries.AcknowledgeDownload(id, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record download acknowledgement")
		return
	}
	if !acknowledged {
		writeError(w, http.StatusConflict, "every file must be requested before acknowledging download")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
