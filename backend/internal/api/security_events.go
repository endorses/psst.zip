package api

import (
	"net/http"
	"strconv"
	"time"
)

// Security events are administrator-only metadata. Never echo invalid query
// values: URLs can accidentally contain pasted capabilities or other secrets.
func (s *Server) getSecurityEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query := r.URL.Query()
	before := int64(0)
	limit := 50
	for key, values := range query {
		if (key != "before" && key != "limit") || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "invalid security activity pagination")
			return
		}
		value, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || value <= 0 || (key == "limit" && value > 100) {
			writeError(w, http.StatusBadRequest, "invalid security activity pagination")
			return
		}
		if key == "before" {
			before = value
		} else {
			limit = int(value)
		}
	}
	page, err := s.queries.SecurityEvents(before, limit, time.Now())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "security activity is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, page)
}
