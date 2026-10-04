package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

const defaultMaxFileSize int64 = 25 * 1024 * 1024
const absoluteMaxFileSize int64 = 1024 * 1024 * 1024 * 1024 // 1 TiB; arithmetic remains bounded.
const encryptedChunkSize int64 = 4 * 1024 * 1024
const encryptedChunkOverhead int64 = 60

type serverSettings struct {
	MaxFileSize        int64 `json:"max_file_size"`
	MaxFileSizeCeiling int64 `json:"max_file_size_ceiling"`
}

func (s *Server) settings() (serverSettings, error) {
	ceiling := s.cfg.MaxFileSize
	if ceiling <= 0 || ceiling > absoluteMaxFileSize {
		ceiling = absoluteMaxFileSize
	}
	value, err := s.queries.MaxFileSize()
	if err != nil {
		return serverSettings{}, err
	}
	if value == 0 {
		value = defaultMaxFileSize
	}
	if value > ceiling {
		value = ceiling
	}
	return serverSettings{value, ceiling}, nil
}

func (s *Server) publicConfig(w http.ResponseWriter, r *http.Request) {
	settings, err := s.settings()
	if err != nil {
		writeError(w, 500, "could not read server settings")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, settings)
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		MaxFileSize int64 `json:"max_file_size"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid settings body")
		return
	}
	settings, err := s.settings()
	if err != nil {
		writeError(w, 500, "could not read server settings")
		return
	}
	if request.MaxFileSize < 1024*1024 || request.MaxFileSize > settings.MaxFileSizeCeiling {
		writeError(w, 400, "file limit must be at least 1 MiB and no greater than the operator ceiling")
		return
	}
	if err := s.queries.SetMaxFileSize(request.MaxFileSize); err != nil {
		writeError(w, 500, "could not save server settings")
		return
	}
	settings.MaxFileSize = request.MaxFileSize
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, settings)
}

// Existing uploads keep their declared reservation when an administrator lowers
// the limit. New upload resources are checked before allocating storage.
func (s *Server) allowUploadSize(w http.ResponseWriter, r *http.Request) bool {
	length, err := strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
	if err != nil || length < 0 {
		writeError(w, 400, "invalid Upload-Length")
		return false
	}
	settings, err := s.settings()
	if err != nil {
		writeError(w, 500, "could not read server settings")
		return false
	}
	chunks := (settings.MaxFileSize + encryptedChunkSize - 1) / encryptedChunkSize
	if chunks < 1 {
		chunks = 1
	}
	if length > settings.MaxFileSize+chunks*encryptedChunkOverhead {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds the administrator's size limit")
		return false
	}
	return true
}
