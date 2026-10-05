package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

type historyChangeResponse struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Action   string `json:"action"`
	Resource any    `json:"resource,omitempty"`
}

func (s *Server) historyChanges(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	values, err := url.ParseQuery(r.URL.RawQuery)
	limit := 50
	if err == nil {
		for key, vs := range values {
			if len(vs) != 1 {
				err = database.ErrInvalidPage
				break
			}
			switch key {
			case "cursor":
				if len(vs[0]) == 0 || len(vs[0]) > 512 {
					err = database.ErrInvalidPage
				}
			case "limit":
				limit, err = strconv.Atoi(vs[0])
				if err == nil && (limit < 1 || limit > 100 || strconv.Itoa(limit) != vs[0]) {
					err = database.ErrInvalidPage
				}
			default:
				err = database.ErrInvalidPage
			}
			if err != nil {
				break
			}
		}
	}
	if err != nil || values.Get("cursor") == "" {
		writeError(w, 400, "invalid history change parameters")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	feed, err := s.queries.AccountHistoryChanges(ctx, identity(r).user.ID, limit, values.Get("cursor"), identity(r).session.ID)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrHistorySyncReset):
			writeError(w, 409, "history synchronization requires a fresh snapshot", "history_sync_reset_required")
		case errors.Is(err, database.ErrInvalidPage):
			writeError(w, 400, "invalid history change cursor")
		case errors.Is(err, database.ErrHistoryAccess):
			writeError(w, 403, "history access is no longer authorized")
		default:
			writeError(w, 503, "history unavailable")
		}
		return
	}
	changes := make([]historyChangeResponse, 0, len(feed.Changes))
	for _, event := range feed.Changes {
		row := historyChangeResponse{Kind: event.Kind, ID: event.ID, Revision: event.Revision, Action: event.Action}
		if event.Resource != nil {
			row.Resource = historyResponse(*event.Resource, "")
		}
		changes = append(changes, row)
	}
	response := struct {
		Version    int                     `json:"version"`
		Generation string                  `json:"generation"`
		Changes    []historyChangeResponse `json:"changes"`
		NextCursor string                  `json:"next_cursor"`
		HasMore    bool                    `json:"has_more"`
	}{1, feed.Generation, changes, feed.NextCursor, feed.HasMore}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > 1024*1024 {
		writeError(w, 503, "history response exceeds supported size")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(encoded)
}
