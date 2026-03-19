package api

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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

	id := uuid.New().String()
	expiresAt := time.Now().Add(expiry)

	if err := s.queries.CreateTransfer(id, expiresAt, req.MaxDownloads); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create transfer")
		return
	}

	writeJSON(w, http.StatusCreated, CreateTransferResponse{
		ID:        id,
		ExpiresAt: expiresAt,
	})
}

func (s *Server) getTransfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	t, err := s.queries.GetTransfer(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "database error")
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

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) completeTransfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	if err := s.queries.CompleteTransfer(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uploadManifest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")

	if _, err := s.queries.GetTransfer(id); err != nil {
		writeError(w, http.StatusNotFound, "transfer not found")
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
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

	// Check download limits.
	t, err := s.queries.GetTransfer(transferID)
	if err != nil {
		writeError(w, http.StatusNotFound, "transfer not found")
		return
	}
	if t.MaxDownloads > 0 && t.DownloadCount >= t.MaxDownloads {
		writeError(w, http.StatusGone, "download limit reached")
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

	storageKey := fmt.Sprintf("%s/%s", transferID, fileID)
	rc, err := s.fileStore.Load(storageKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load file")
		return
	}
	defer rc.Close()

	_ = s.queries.IncrementDownloadCount(transferID)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, rc)
}

// --- tus wrappers ---

func (s *Server) tusCreate(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "transferID")
	s.tusH.ServeCreate(w, r, transferID)
}

func (s *Server) tusHead(w http.ResponseWriter, r *http.Request) {
	fileID := chi.URLParam(r, "fileID")
	s.tusH.ServeOffset(w, r, fileID)
}

func (s *Server) tusPatch(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "transferID")
	fileID := chi.URLParam(r, "fileID")
	storageKey := fmt.Sprintf("%s/%s", transferID, fileID)
	s.tusH.ServePatch(w, r, fileID, storageKey)
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

	id := uuid.New().String()
	expiresAt := time.Now().Add(expiry)

	if err := s.queries.CreateSlot(id, expiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create slot")
		return
	}

	writeJSON(w, http.StatusCreated, CreateSlotResponse{
		ID:        id,
		ExpiresAt: expiresAt,
	})
}

func (s *Server) getSlot(w http.ResponseWriter, r *http.Request) {
	slotID := chi.URLParam(r, "slotID")

	slot, err := s.queries.GetSlot(slotID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "slot not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	transfers, _ := s.queries.ListSlotTransfers(slotID)

	var infos []SlotTransferInfo
	for _, t := range transfers {
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

	if _, err := s.queries.GetSlot(slotID); err != nil {
		writeError(w, http.StatusNotFound, "slot not found")
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

	id := uuid.New().String()
	expiresAt := time.Now().Add(expiry)

	if err := s.queries.CreateTransfer(id, expiresAt, req.MaxDownloads); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create transfer")
		return
	}

	if err := s.queries.LinkSlotTransfer(slotID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link transfer to slot")
		return
	}

	// Notify SSE listeners.
	s.sseHub.Send(slotID, fmt.Sprintf(`{"event":"transfer_created","transfer_id":"%s"}`, id))

	writeJSON(w, http.StatusCreated, CreateTransferResponse{
		ID:        id,
		ExpiresAt: expiresAt,
	})
}
