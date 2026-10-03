package tus

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/endorses/psst.zip/backend/internal/store"
)

const tusVersion = "1.0.0"

// UploadInfo holds metadata about an upload tracked by the caller.
type UploadInfo struct {
	ID         string
	Size       int64 // declared total size (from Upload-Length)
	Offset     int64 // bytes uploaded so far
	IsComplete bool
}

// Store defines what the tus handler needs to persist upload state.
type Store interface {
	// GetUpload returns the current upload state.
	GetUpload(id string) (*UploadInfo, error)
	// CreateUpload registers a new upload and returns its ID.
	CreateUpload(transferID string, size int64) (string, error)
	// UpdateOffset sets the upload offset after a successful PATCH.
	UpdateOffset(id string, offset int64, complete bool) error
}

// Handler implements a minimal tus 1.0.0 server (creation + offset resume).
type Handler struct {
	store     Store
	fileStore store.FileStore
	maxSize   int64
}

// NewHandler creates a tus Handler.
func NewHandler(s Store, fs store.FileStore, maxSize int64) *Handler {
	return &Handler{store: s, fileStore: fs, maxSize: maxSize}
}

// ServeCreate handles POST requests to create a new upload.
func (h *Handler) ServeCreate(w http.ResponseWriter, r *http.Request, transferID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	tusResumable := r.Header.Get("Tus-Resumable")
	if tusResumable != tusVersion {
		http.Error(w, "unsupported tus version", http.StatusPreconditionFailed)
		return
	}

	lengthStr := r.Header.Get("Upload-Length")
	if lengthStr == "" {
		http.Error(w, "Upload-Length header required", http.StatusBadRequest)
		return
	}
	length, err := strconv.ParseInt(lengthStr, 10, 64)
	if err != nil || length < 0 {
		http.Error(w, "invalid Upload-Length", http.StatusBadRequest)
		return
	}
	if h.maxSize > 0 && length > h.maxSize {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}

	id, err := h.store.CreateUpload(transferID, length)
	if err != nil {
		if errors.Is(err, ErrUploadLimit) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Tus-Resumable", tusVersion)
	w.Header().Set("Location", strings.TrimSuffix(r.URL.Path, "/")+"/"+id)
	w.WriteHeader(http.StatusCreated)
}

// ServeOffset handles HEAD requests to retrieve the current upload offset.
func (h *Handler) ServeOffset(w http.ResponseWriter, r *http.Request, fileID string) {
	if r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	info, err := h.store.GetUpload(fileID)
	if err != nil {
		http.Error(w, "upload not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Tus-Resumable", tusVersion)
	w.Header().Set("Upload-Offset", strconv.FormatInt(info.Offset, 10))
	w.Header().Set("Upload-Length", strconv.FormatInt(info.Size, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// ServePatch handles PATCH requests to upload a chunk.
func (h *Handler) ServePatch(w http.ResponseWriter, r *http.Request, fileID string, storageKey string) {
	if r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	tusResumable := r.Header.Get("Tus-Resumable")
	if tusResumable != tusVersion {
		http.Error(w, "unsupported tus version", http.StatusPreconditionFailed)
		return
	}

	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/offset+octet-stream") {
		http.Error(w, "Content-Type must be application/offset+octet-stream", http.StatusUnsupportedMediaType)
		return
	}

	offsetStr := r.Header.Get("Upload-Offset")
	if offsetStr == "" {
		http.Error(w, "Upload-Offset header required", http.StatusBadRequest)
		return
	}
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil || offset < 0 {
		http.Error(w, "invalid Upload-Offset", http.StatusBadRequest)
		return
	}

	info, err := h.store.GetUpload(fileID)
	if err != nil {
		http.Error(w, "upload not found", http.StatusNotFound)
		return
	}

	if info.IsComplete {
		http.Error(w, "upload already complete", http.StatusConflict)
		return
	}

	if offset != info.Offset {
		http.Error(w, fmt.Sprintf("offset mismatch: expected %d, got %d", info.Offset, offset), http.StatusConflict)
		return
	}

	// Bound both fixed-length and chunked requests before they reach storage.
	remaining := info.Size - offset
	if remaining < 0 || (h.maxSize > 0 && info.Size > h.maxSize) || r.ContentLength > remaining {
		http.Error(w, "chunk exceeds upload length", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, remaining)
	// Write data to file store at the current offset.
	n, err := h.fileStore.SaveAt(storageKey, r.Body, offset)
	if err != nil {
		var limitError *http.MaxBytesError
		if errors.As(err, &limitError) {
			http.Error(w, "chunk exceeds upload length", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "failed to save data", http.StatusInternalServerError)
		}
		return
	}

	newOffset := offset + n
	complete := newOffset == info.Size

	if err := h.store.UpdateOffset(fileID, newOffset, complete); err != nil {
		http.Error(w, "failed to update offset", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Tus-Resumable", tusVersion)
	w.Header().Set("Upload-Offset", strconv.FormatInt(newOffset, 10))
	w.WriteHeader(http.StatusNoContent)
}

// ServeOptions handles OPTIONS requests (tus discovery).
func ServeOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Tus-Resumable", tusVersion)
	w.Header().Set("Tus-Version", tusVersion)
	w.Header().Set("Tus-Extension", "creation")
	w.WriteHeader(http.StatusNoContent)
}

var ErrUploadLimit = errors.New("receive link upload limit reached")
