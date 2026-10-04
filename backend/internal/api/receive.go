package api

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/database"
)

func policyError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}
func validTransferPolicy(w http.ResponseWriter, req CreateTransferRequest) bool {
	if req.MaxDownloads < 0 || int64(req.MaxDownloads) > math.MaxInt32 || req.ExpiresInSeconds < 0 || int64(req.ExpiresInSeconds) > math.MaxInt64/int64(time.Second) {
		policyError(w, 400, "invalid_link_policy", "limits must be nonnegative integers within the supported range")
		return false
	}
	return true
}
func validSlotPolicy(w http.ResponseWriter, req *CreateSlotRequest) bool {
	if req.MaxFiles < 0 || int64(req.MaxFiles) > math.MaxInt32 {
		policyError(w, 400, "invalid_link_policy", "max_files must be an integer between 0 and 2147483647")
		return false
	}
	if req.ReceiveProtocol == 0 {
		req.ReceiveProtocol = 1
	}
	if req.ReceiveProtocol == 1 && req.RecipientPublicKey == "" {
		return true
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(req.RecipientPublicKey)
	if req.ReceiveProtocol != 2 || err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != req.RecipientPublicKey || bytes.Equal(key, make([]byte, 32)) {
		policyError(w, 400, "invalid_receive_protocol", "receive_protocol 2 requires a canonical base64url 32-byte recipient public key")
		return false
	}
	return true
}
func remainingFiles(slot *database.Slot) *int64 {
	if slot.MaxFiles == 0 {
		return nil
	}
	n := max(int64(0), int64(slot.MaxFiles)-slot.ReservedFiles)
	return &n
}
func (s *Server) slotPolicy(response *SlotResponse, slot *database.Slot, counts ...int64) error {
	response.ReceiveProtocol = slot.ReceiveProtocol
	response.RecipientPublicKey = slot.RecipientPublicKey
	response.MaxFiles = slot.MaxFiles
	response.ReservedFiles = slot.ReservedFiles
	response.RemainingFiles = remainingFiles(slot)
	if len(counts) > 0 {
		response.CompletedFiles = counts[0]
	} else {
		completed, err := s.queries.CompletedSlotFiles(slot.ID)
		if err != nil {
			return err
		}
		response.CompletedFiles = completed
	}
	return nil
}
func (s *Server) filePolicy(t *database.Transfer) ([]FileInfo, error) {
	files, err := s.queries.ListFiles(t.ID)
	if err != nil {
		return nil, err
	}
	result := make([]FileInfo, 0, len(files))
	for _, file := range files {
		var remaining *int
		if t.MaxDownloads > 0 {
			n := max(0, t.MaxDownloads-file.DownloadCount)
			remaining = &n
		}
		result = append(result, FileInfo{ID: file.ID, Size: file.Size, UploadOffset: file.UploadOffset, UploadComplete: file.UploadComplete, DownloadCount: file.DownloadCount, RemainingDownloads: remaining})
	}
	return result, nil
}
func (s *Server) slotAvailability(w http.ResponseWriter, r *http.Request) {
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
	if slot.Status == "revoked" || !time.Now().Before(slot.ExpiresAt) {
		policyError(w, 410, "link_unavailable", "receive link expired or revoked")
		return
	}
	if !s.slotOwnerActive(w, id) {
		return
	}
	bytesLimit := s.cfg.MaxSlotSize
	if bytesLimit <= 0 {
		bytesLimit = 5 * 1024 * 1024 * 1024
	}
	transferLimit := s.cfg.MaxSlotTransfers
	if transferLimit <= 0 {
		transferLimit = 20
	}
	remaining := remainingFiles(slot)
	response := SlotAvailability{ID: id, Status: "waiting", ExpiresAt: slot.ExpiresAt, ReceiveProtocol: slot.ReceiveProtocol, RecipientPublicKey: slot.RecipientPublicKey, MaxFiles: slot.MaxFiles, RemainingFiles: remaining, RemainingBytes: max(int64(0), bytesLimit-slot.ReservedBytes), RemainingTransfers: max(0, transferLimit-slot.UploadCount)}
	response.Available = slot.ReceiveProtocol == 2 && response.RemainingBytes > 0 && response.RemainingTransfers > 0 && (remaining == nil || *remaining > 0)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, response)
}
func (s *Server) requireInboxOwner(next http.Handler) http.Handler {
	return s.requireRegularUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "slotID")
		if !isValidUUID(id) {
			writeError(w, 400, "invalid slot ID")
			return
		}
		owner, err := s.queries.Owner("slot", id)
		if err != nil {
			writeError(w, 404, "slot not found")
			return
		}
		if owner != identity(r).user.ID {
			writeError(w, 403, "only the inbox owner may read submissions")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	}))
}
func (s *Server) requireTransferRead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "transferID")
		if !isValidUUID(id) {
			writeError(w, 400, "invalid transfer ID")
			return
		}
		slots, err := s.queries.TransferSlotIDs(id)
		if err != nil {
			writeError(w, 500, "database error")
			return
		}
		if len(slots) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		s.requireRegularUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, slot := range slots {
				owner, err := s.queries.Owner("slot", slot)
				if err != nil || owner != identity(r).user.ID {
					writeError(w, 403, "only the inbox owner may read submissions")
					return
				}
			}
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})).ServeHTTP(w, r)
	})
}
func (s *Server) validateReceiveManifest(w http.ResponseWriter, id string, data []byte) bool {
	protocol, err := s.queries.TransferReceiveProtocol(id)
	if err != nil {
		writeError(w, 500, "database error")
		return false
	}
	if protocol == 2 && (len(data) < 116 || len(data) > database.MaxManifestBytes || !bytes.Equal(data[:min(8, len(data))], []byte("PSSTRCV2"))) {
		policyError(w, 400, "invalid_receive_envelope", "receive manifest must use the version 2 encrypted envelope")
		return false
	}
	return true
}

// A sender may resolve a lost finalization response without acquiring inbox
// read authority. The capability can only inspect its own submission status.
func (s *Server) uploadStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transferID")
	if !isValidUUID(id) {
		writeError(w, 400, "invalid transfer ID")
		return
	}
	token := bearer(r)
	if token == "" {
		writeError(w, 403, "upload capability required")
		return
	}
	transfer, err := s.queries.GetTransfer(id)
	if err != nil {
		writeError(w, 404, "transfer not found")
		return
	}
	slots, err := s.queries.TransferSlotIDs(id)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	if len(slots) == 0 || len(transfer.DeleteTokenHash) == 0 || subtle.ConstantTimeCompare(tokenHash(token), transfer.DeleteTokenHash) != 1 {
		writeError(w, 403, "upload capability required")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"id": transfer.ID, "status": transfer.Status})
}
