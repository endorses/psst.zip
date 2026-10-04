package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/database"
)

func adminResourceIdentity(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	kind, id := chi.URLParam(r, "resourceType"), chi.URLParam(r, "resourceID")
	if (kind != "transfer" && kind != "slot") || !isValidUUID(id) {
		writeError(w, 400, "invalid resource type or ID")
		return "", "", false
	}
	return kind, id, true
}
func resourceQuery(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	permitted := map[string]bool{}
	for _, name := range allowed {
		permitted[name] = true
	}
	for name, values := range r.URL.Query() {
		if !permitted[name] || len(values) != 1 || values[0] == "" {
			writeError(w, 400, "invalid resource query")
			return false
		}
	}
	return true
}
func adminResourceLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeError(w, 400, "page limit must be between 1 and 100")
			return 0, false
		}
		limit = n
	}
	return limit, true
}
func resourceReadFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "resource not found")
	} else if errors.Is(err, database.ErrInvalidPage) {
		writeError(w, 400, "invalid resource query")
	} else {
		writeError(w, 503, "resource information is temporarily unavailable")
	}
}
func (s *Server) getAdminResources(w http.ResponseWriter, r *http.Request) {
	if !resourceQuery(w, r, "type", "owner_id", "status", "after", "limit") {
		return
	}
	limit, ok := adminResourceLimit(w, r)
	if !ok {
		return
	}
	filter := database.AdminResourceFilter{Type: r.URL.Query().Get("type"), OwnerID: r.URL.Query().Get("owner_id"), Status: r.URL.Query().Get("status")}
	if filter.OwnerID != "" && !isValidUUID(filter.OwnerID) {
		writeError(w, 400, "invalid resource owner")
		return
	}
	page, err := s.queries.AdminResources(filter, limit, r.URL.Query().Get("after"))
	if err != nil {
		resourceReadFailure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, page)
}
func (s *Server) getAdminResource(w http.ResponseWriter, r *http.Request) {
	kind, id, ok := adminResourceIdentity(w, r)
	if !ok || !resourceQuery(w, r) {
		return
	}
	item, err := s.queries.AdminResource(kind, id)
	if err != nil {
		resourceReadFailure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, item)
}
func (s *Server) getAdminResourceEvents(w http.ResponseWriter, r *http.Request) {
	kind, id, ok := adminResourceIdentity(w, r)
	if !ok || !resourceQuery(w, r, "before", "limit") {
		return
	}
	limit, ok := adminResourceLimit(w, r)
	if !ok {
		return
	}
	before := int64(0)
	if raw := r.URL.Query().Get("before"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			writeError(w, 400, "invalid event cursor")
			return
		}
		before = n
	}
	page, err := s.queries.AdminResourceEvents(kind, id, before, limit, time.Now())
	if err != nil {
		resourceReadFailure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, page)
}
