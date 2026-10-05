package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/database"
)

type RenameTitleRequest struct {
	Title *string `json:"title"`
}

func normalizeRequestTitle(w http.ResponseWriter, title **string) bool {
	value, err := database.NormalizeLinkTitle(*title)
	if err != nil {
		policyError(w, 400, "invalid_link_title", err.Error())
		return false
	}
	*title = value
	return true
}
func transferPublicStatus(t *database.Transfer) string {
	if t.Exhausted {
		return "exhausted"
	}
	return t.Status
}
func transferInactiveReason(t *database.Transfer) *string {
	if !t.Exhausted {
		return nil
	}
	reason := "download_limit"
	return &reason
}
func (s *Server) renameTransferTitle(w http.ResponseWriter, r *http.Request) {
	s.renameLinkTitle(w, r, "transfer", chi.URLParam(r, "transferID"))
}
func (s *Server) renameSlotTitle(w http.ResponseWriter, r *http.Request) {
	s.renameLinkTitle(w, r, "slot", chi.URLParam(r, "slotID"))
}
func (s *Server) renameLinkTitle(w http.ResponseWriter, r *http.Request, kind, id string) {
	w.Header().Set("Cache-Control", "no-store")
	if !isValidUUID(id) {
		writeError(w, 400, "invalid link ID")
		return
	}
	var req RenameTitleRequest
	if !decodeCreation(w, r, &req) || !normalizeRequestTitle(w, &req.Title) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.queries.RenameLinkTitle(ctx, kind, id, identity(r).user.ID, req.Title); err != nil {
		if errors.Is(err, database.ErrTitleOwnership) {
			writeError(w, 404, "link not found")
		} else {
			writeError(w, 503, "could not update link title")
		}
		return
	}
	writeJSON(w, 200, req)
}

// encoding/json replaces malformed UTF-8 and lone UTF-16 surrogates. Reject
// those inputs before decoding so clients share a Unicode-scalar contract.
func validEncodedLinkTitle(raw json.RawMessage) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(raw) {
			return false
		}
		scalar, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil {
			return false
		}
		if scalar >= 0xDC00 && scalar <= 0xDFFF {
			return false
		}
		if scalar >= 0xD800 && scalar <= 0xDBFF {
			if i+12 > len(raw) || string(raw[i+6:i+8]) != `\u` {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 11
		} else {
			i += 5
		}
	}
	return true
}
