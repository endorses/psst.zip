package api

import (
	"database/sql"
	"fmt"
	"io"
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
	if err := decodeJSON(r, &req); err != nil {
		req = CreateTransferRequest{}
	}

	expiry := s.cfg.DefaultExpiry
	if req.ExpiresInSeconds > 0 {
		expiry = time.Duration(req.ExpiresInSeconds) * time.Second
	}

	token, hash, err := newDeleteToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate deletion token")
		return
	}

	id := uuid.New().String()
	expiresAt := time.Now().Add(expiry)

	if err := s.queries.CreateTransfer(id, expiresAt, req.MaxDownloads, hash); err != nil {
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

	if t.Status == "revoked" || !time.Now().Before(t.ExpiresAt) {
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
	defer s.lockTransfer(id)()
	if !isValidUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}

	if s.activeTransfer(w, id, true) == nil {
		return
	}
	if err := s.queries.CompleteTransfer(id); err != nil {
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
	defer s.lockTransfer(id)()
	if !isValidUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}

	if s.activeTransfer(w, id, true) == nil {
		return
	}

	// Limit request body size for manifest uploads.
	maxSize := s.cfg.MaxManifestSize
	if maxSize <= 0 {
		maxSize = 10 * 1024 * 1024 // 10 MB fallback
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "manifest too large")
		return
	}
	defer r.Body.Close()

	if err := s.queries.SaveManifest(id, data); err != nil {
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
		writeError(w, http.StatusGone, "download limit reached")
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
				writeError(w, http.StatusGone, "download limit reached")
				return
			}
		}

		writeError(w, http.StatusInternalServerError, "failed to load file")
		return
	}
	defer rc.Close()

	allowed, err := s.queries.ReserveFileDownload(transferID, fileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count download")
		return
	}
	if !allowed {
		writeError(w, http.StatusGone, "download limit reached")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, rc)
}

// --- tus wrappers ---

func (s *Server) tusCreate(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "transferID")
	defer s.lockTransfer(transferID)()
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
	transferID, fileID := chi.URLParam(r, "transferID"), chi.URLParam(r, "fileID")
	defer s.lockTransfer(transferID)()
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
	if t.Status == "revoked" || !time.Now().Before(t.ExpiresAt) {
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
	if err := decodeJSON(r, &req); err != nil {
		req = CreateSlotRequest{}
	}

	expiry := s.cfg.DefaultExpiry
	if req.ExpiresInSeconds > 0 {
		expiry = time.Duration(req.ExpiresInSeconds) * time.Second
	}

	token, hash, err := newDeleteToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate deletion token")
		return
	}

	id := uuid.New().String()
	expiresAt := time.Now().Add(expiry)

	if err := s.queries.CreateSlot(id, expiresAt, hash); err != nil {
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

	if slot.Status == "revoked" || !time.Now().Before(slot.ExpiresAt) {
		writeError(w, http.StatusGone, "slot expired or revoked")
		return
	}
	transfers, _ := s.queries.ListSlotTransfers(slotID)

	infos := make([]SlotTransferInfo, 0, len(transfers))
	for _, t := range transfers {
		if t.Status == "revoked" || !time.Now().Before(t.ExpiresAt) {
			continue
		}
		fc, _, _ := s.queries.FileCountAndSize(t.ID)
		infos = append(infos, SlotTransferInfo{
			TransferID: t.ID,
			Status:     t.Status,
			FileCount:  fc,
		})
	}

	writeJSON(w, http.StatusOK, SlotResponse{
		ID:        slot.ID,
		Status:    slot.Status,
		Transfers: infos,
		ExpiresAt: slot.ExpiresAt,
		CreatedAt: slot.CreatedAt,
	})
}

func (s *Server) createSlotTransfer(w http.ResponseWriter, r *http.Request) {
	slotID := chi.URLParam(r, "slotID")
	if !isValidUUID(slotID) {
		writeError(w, http.StatusBadRequest, "invalid slot ID")
		return
	}

	defer store.LockSlot(slotID)()
	slot, err := s.queries.GetSlot(slotID)
	if err != nil {
		writeError(w, http.StatusNotFound, "slot not found")
		return
	}

	if slot.Status == "revoked" || !time.Now().Before(slot.ExpiresAt) {
		writeError(w, http.StatusGone, "slot expired or revoked")
		return
	}
	var req CreateTransferRequest
	if err := decodeJSON(r, &req); err != nil {
		req = CreateTransferRequest{}
	}

	expiry := s.cfg.DefaultExpiry
	if req.ExpiresInSeconds > 0 {
		expiry = time.Duration(req.ExpiresInSeconds) * time.Second
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
	if err := s.queries.CreateSlotTransfer(slotID, id, expiresAt, req.MaxDownloads, hash); err != nil {
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
