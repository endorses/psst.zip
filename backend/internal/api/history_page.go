package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

type historyTransferResponse struct {
	TransferResponse
	FileCount *int64                `json:"file_count"`
	TotalSize *int64                `json:"total_size"`
	Summary   database.InboxSummary `json:"summary"`
}
type historySlotResponse struct {
	SlotResponse
	FileCount      *int64                `json:"file_count"`
	TotalSize      *int64                `json:"total_size"`
	CompletedFiles *int64                `json:"completed_files"`
	Summary        database.InboxSummary `json:"summary"`
}

func (s *Server) resources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := httpParseHistoryQuery(r)
	if err != nil {
		writeError(w, 400, "invalid history page parameters")
		return
	}
	a := identity(r)
	if a.user.Role == "admin" && !query.all {
		accountRestriction(w, "admin_transfer_forbidden")
		return
	}
	if query.all && a.user.Role != "admin" {
		writeError(w, 403, "administrator required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	page, err := s.queries.AccountHistoryPage(ctx, a.user.ID, query.all, query.limit, query.after)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrInvalidPage):
			writeError(w, 400, "invalid page limit or cursor")
		case errors.Is(err, database.ErrHistoryAccess):
			writeError(w, 403, "history access is no longer authorized")
		default:
			writeError(w, 503, "history unavailable")
		}
		return
	}
	transfers := []historyTransferResponse{}
	slots := []historySlotResponse{}
	for _, resource := range page.Resources {
		owner := ""
		if query.all {
			owner = resource.OwnerID
		}
		if t := resource.Transfer; t != nil {
			item := TransferResponse{ID: t.ID, Status: t.Status, HasManifest: resource.HasManifest, ExpiresAt: t.ExpiresAt, MaxDownloads: t.MaxDownloads, DownloadCount: t.DownloadCount, CreatedAt: t.CreatedAt, OwnerID: owner, Files: []FileInfo{}}
			if t.CompletedAt.Valid {
				item.CompletedAt = &t.CompletedAt.Time
			}
			if t.DownloadedAt.Valid {
				item.DownloadedAt = &t.DownloadedAt.Time
			}
			transfers = append(transfers, historyTransferResponse{TransferResponse: item, FileCount: resource.Summary.FileCount, TotalSize: resource.Summary.TotalSize, Summary: resource.Summary})
		} else {
			slot := resource.Slot
			item := SlotResponse{ID: slot.ID, Status: slot.Status, ExpiresAt: slot.ExpiresAt, CreatedAt: slot.CreatedAt, OwnerID: owner, Transfers: []SlotTransferInfo{}, ReceiveProtocol: slot.ReceiveProtocol, RecipientPublicKey: slot.RecipientPublicKey, MaxFiles: slot.MaxFiles, ReservedFiles: slot.ReservedFiles, RemainingFiles: remainingFiles(slot)}
			slots = append(slots, historySlotResponse{SlotResponse: item, FileCount: resource.Summary.FileCount, TotalSize: resource.Summary.TotalSize, CompletedFiles: resource.Summary.CompletedFiles, Summary: resource.Summary})
		}
	}
	writeJSON(w, 200, map[string]any{"paginated": true, "transfers": transfers, "slots": slots, "next_cursor": page.NextCursor})
}

type historyQuery struct {
	limit int
	after string
	all   bool
}

func httpParseHistoryQuery(r *http.Request) (historyQuery, error) {
	out := historyQuery{limit: 50}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return out, err
	}
	for key, value := range values {
		if len(value) != 1 {
			return out, database.ErrInvalidPage
		}
		switch key {
		case "limit":
			out.limit, err = strconv.Atoi(value[0])
			if err != nil || out.limit < 1 || out.limit > 100 || strconv.Itoa(out.limit) != value[0] {
				return out, database.ErrInvalidPage
			}
		case "after":
			out.after = value[0]
		case "all":
			if value[0] != "true" && value[0] != "false" {
				return out, database.ErrInvalidPage
			}
			out.all = value[0] == "true"
		default:
			return out, database.ErrInvalidPage
		}
	}
	return out, nil
}
