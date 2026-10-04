package api

import "net/http"

func (s *Server) getCounterChecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	status, err := s.queries.CounterRebuildStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Storage counter status is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, status)
}
