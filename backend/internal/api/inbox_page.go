package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/database"
)

type inboxPageResponse struct {
	SlotResponse
	CompletedFiles *int64                `json:"completed_files,omitempty"`
	Paginated      bool                  `json:"paginated"`
	NextCursor     *string               `json:"next_cursor"`
	Summary        database.InboxSummary `json:"summary"`
}

func (s *Server) getInboxPage(w http.ResponseWriter, r *http.Request) { s.readInbox(w, r, false) }
func (s *Server) readInbox(w http.ResponseWriter, r *http.Request, legacy bool) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	limit := 50
	after := ""
	if legacy {
		limit = 100
	} else {
		query := r.URL.Query()
		if len(query["limit"]) > 1 || len(query["after"]) > 1 {
			writeError(w, 400, "invalid page limit or cursor")
			return
		}
		if raw, ok := query["limit"]; ok {
			var err error
			limit, err = strconv.Atoi(raw[0])
			if err != nil || strconv.Itoa(limit) != raw[0] {
				writeError(w, 400, "invalid page limit or cursor")
				return
			}
		}
		after = query.Get("after")
	}
	page, err := s.queries.OwnerInboxPage(ctx, chi.URLParam(r, "slotID"), identity(r).user.ID, limit, after, legacy, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, database.ErrInvalidPage):
			writeError(w, 400, "invalid page limit or cursor")
		case errors.Is(err, database.ErrInboxPaginationRequired):
			policyError(w, 409, "inbox_pagination_required", "inbox requires paginated access")
		case errors.Is(err, database.ErrTransferFileLimit):
			policyError(w, 409, "transfer_file_limit_exceeded", database.ErrTransferFileLimit.Error())
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, 404, "slot not found")
		default:
			writeError(w, 503, "inbox unavailable")
		}
		return
	}
	slot := page.Slot
	if slot.Status == "revoked" {
		incidentFailure(w, database.ErrResourceRevoked)
		return
	}
	if !time.Now().Before(slot.ExpiresAt) {
		policyError(w, 410, "link_expired", "receive link expired")
		return
	}
	infos := make([]SlotTransferInfo, 0, len(page.Transfers))
	for _, item := range page.Transfers {
		infos = append(infos, SlotTransferInfo{TransferID: item.TransferID, Status: item.Status, FileCount: item.FileCount})
	}
	response := SlotResponse{ID: slot.ID, Status: slot.Status, ExpiresAt: slot.ExpiresAt, CreatedAt: slot.CreatedAt, Transfers: infos}
	completed := int64(0)
	if page.Summary.CompletedFiles != nil {
		completed = *page.Summary.CompletedFiles
	}
	if err = s.slotPolicy(&response, slot, completed); err != nil {
		writeError(w, 503, "inbox unavailable")
		return
	}
	response.FileCount = page.Summary.FileCount
	response.TotalSize = page.Summary.TotalSize
	if legacy {
		if page.Summary.State != "ready" {
			policyError(w, 503, "inbox_summary_updating", "inbox summary is updating")
			return
		}
		writeJSON(w, 200, response)
		return
	}
	writeJSON(w, 200, inboxPageResponse{SlotResponse: response, Paginated: true, NextCursor: page.NextCursor, Summary: page.Summary})
}
