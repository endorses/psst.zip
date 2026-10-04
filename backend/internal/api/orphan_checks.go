package api

import "net/http"

func (s *Server) getOrphanChecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	status, err := s.queries.OrphanScanStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Orphan file status is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, status)
}
