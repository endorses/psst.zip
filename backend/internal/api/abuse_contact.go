package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func (s *Server) getAbuseContact(w http.ResponseWriter, r *http.Request) {
	email, err := s.queries.AbuseContactEmail()
	if err != nil {
		writeError(w, 503, "abuse contact is temporarily unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"email": email})
}
func (s *Server) updateAbuseContact(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email *string `json:"email"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF || request.Email == nil || !database.ValidAbuseContactEmail(*request.Email) {
		writeError(w, 400, "enter one ordinary email address, or leave it empty to disable abuse contact")
		return
	}
	if err := s.queries.SetAbuseContactEmail(*request.Email, adminActor(r)); err != nil {
		if rejectAdminMutation(w, err) {
			return
		}
		writeError(w, 503, "could not save abuse contact")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"email": *request.Email})
}
