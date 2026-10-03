package api

import "net/http"

// health identifies the API without creating a transfer or a drop slot.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Service    string `json:"service"`
		APIVersion int    `json:"api_version"`
	}{Service: "psst.zip", APIVersion: 1})
}
