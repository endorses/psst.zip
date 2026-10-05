package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

var ErrHistoryAccess = errors.New("history access is no longer authorized")

type HistoryResource struct {
	Transfer    *Transfer
	Slot        *Slot
	OwnerID     string
	HasManifest bool
	Summary     InboxSummary
}
type HistoryPage struct {
	Resources  []HistoryResource
	NextCursor *string
}
type historyCursor struct {
	Version int    `json:"v"`
	Actor   string `json:"actor"`
	All     bool   `json:"all"`
	Created string `json:"created"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Filter  string `json:"filter,omitempty"`
}
type historyCandidate struct{ kind, id, created string }

func parseHistoryCursor(after, actor string, all bool, filter string) (*historyCursor, error) {
	if after == "" {
		return nil, nil
	}
	if len(after) > 512 {
		return nil, ErrInvalidPage
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(after)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != after {
		return nil, ErrInvalidPage
	}
	var cursor historyCursor
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Version != 1 || cursor.Actor != actor || cursor.All != all || cursor.Filter != filter || cursor.ID == "" || !validAuditID(cursor.ID) || len(cursor.Created) == 0 || len(cursor.Created) > 64 || strings.ContainsAny(cursor.Created, "\x00\r\n") || (cursor.Kind != "transfer" && cursor.Kind != "slot") {
		return nil, ErrInvalidPage
	}
	canonical, _ := json.Marshal(cursor)
	if !bytes.Equal(raw, canonical) {
		return nil, ErrInvalidPage
	}
	return &cursor, nil
}

// historyCandidatesQuery deliberately applies no membership/lifecycle filters.
// The two existing history indexes bound discovery even if old database rows
// exceed today's account or receive-submission limits.
func historyCandidatesQuery(kind, owner string, cursor *historyCursor, limit int) (string, []any) {
	index := "admin_" + kind + "s_history"
	if owner != "" {
		index = "admin_" + kind + "s_owner_history"
	}
	query := `SELECT id,substr(CAST(created_at AS TEXT),1,65) FROM ` + kind + `s INDEXED BY ` + index + ` WHERE 1=1`
	args := []any{}
	if owner != "" {
		query += ` AND owner_id=?`
		args = append(args, owner)
	}
	if cursor != nil {
		operator := "<"
		if kind < cursor.Kind {
			operator = "<="
		}
		query += ` AND (created_at,id)` + operator + `(?,?)`
		args = append(args, cursor.Created, cursor.ID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, limit+1)
	return query, args
}
func historySummary(known, files, completed, size sql.NullInt64) InboxSummary {
	summary := InboxSummary{State: "updating"}
	if known.Valid && known.Int64 == 1 && files.Valid && completed.Valid && size.Valid && completed.Int64 >= 0 && files.Int64 >= completed.Int64 && size.Int64 >= 0 {
		summary = InboxSummary{State: "ready", FileCount: &files.Int64, CompletedFiles: &completed.Int64, TotalSize: &size.Int64}
	}
	return summary
}

// AccountHistoryPage reads at most 2*(limit+1) raw indexed candidates, then
// inspects at most limit resources. Receive children still consume a raw page
// position; an empty result with a continuation is therefore meaningful.
// All policy, ownership and derived totals are read in one cancellable snapshot.
func (q *Queries) AccountHistoryPage(ctx context.Context, actor string, all bool, limit int, after string, kinds ...string) (HistoryPage, error) {
	page := HistoryPage{Resources: []HistoryResource{}}
	filter := ""
	if len(kinds) > 0 {
		filter = kinds[0]
	}
	if filter != "" && filter != "transfer" && filter != "slot" {
		return page, ErrInvalidPage
	}
	if actor == "" || !validAuditID(actor) || limit < 1 || limit > 100 {
		return page, ErrInvalidPage
	}
	cursor, err := parseHistoryCursor(after, actor, all, filter)
	if err != nil {
		return page, err
	}
	tx, err := q.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	var role string
	var disabled, changePassword bool
	err = tx.QueryRowContext(ctx, `SELECT role,disabled,must_change_password FROM users WHERE id=?`, actor).Scan(&role, &disabled, &changePassword)
	if errors.Is(err, sql.ErrNoRows) {
		return page, ErrHistoryAccess
	}
	if err != nil {
		return page, err
	}
	if disabled || changePassword || (all && role != "admin") || (!all && role != "user") {
		return page, ErrHistoryAccess
	}
	owner := actor
	if all {
		owner = ""
	}
	candidates := make([]historyCandidate, 0, 2*(limit+1))
	for _, kind := range []string{"transfer", "slot"} {
		if filter != "" && kind != filter {
			continue
		}
		query, args := historyCandidatesQuery(kind, owner, cursor, limit)
		rows, e := tx.QueryContext(ctx, query, args...)
		if e != nil {
			return page, e
		}
		for rows.Next() {
			item := historyCandidate{kind: kind}
			if e = rows.Scan(&item.id, &item.created); e != nil {
				_ = rows.Close()
				return page, e
			}
			if item.id == "" || !validAuditID(item.id) || len(item.created) == 0 || len(item.created) > 64 {
				_ = rows.Close()
				return page, errors.New("invalid history identity")
			}
			candidates = append(candidates, item)
		}
		e = rows.Err()
		_ = rows.Close()
		if e != nil {
			return page, e
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.created != b.created {
			return a.created > b.created
		}
		if a.id != b.id {
			return a.id > b.id
		}
		return a.kind > b.kind
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
		last := candidates[len(candidates)-1]
		raw, _ := json.Marshal(historyCursor{Version: 1, Actor: actor, All: all, Created: last.created, ID: last.id, Kind: last.kind, Filter: filter})
		next := base64.RawURLEncoding.EncodeToString(raw)
		if len(next) > 512 {
			return page, errors.New("invalid history cursor size")
		}
		page.NextCursor = &next
	}
	for _, candidate := range candidates {
		if candidate.kind == "transfer" {
			var child bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM slot_transfers INDEXED BY slot_transfers_transfer WHERE transfer_id=?)`, candidate.id).Scan(&child); err != nil {
				return page, err
			}
			if child {
				continue
			}
		}
		item := HistoryResource{}
		var known, files, completed, size sql.NullInt64
		if candidate.kind == "transfer" {
			item.Transfer = &Transfer{}
			t := item.Transfer
			err = tx.QueryRowContext(ctx, `SELECT t.id,t.status,t.expires_at,t.max_downloads,t.download_count,t.created_at,t.completed_at,t.downloaded_at,t.title,`+exhaustedSQL("t")+`,COALESCE(t.owner_id,''),EXISTS(SELECT 1 FROM manifests WHERE transfer_id=t.id),c.inbox_known,c.file_count,c.completed_files,c.total_file_bytes FROM transfers t LEFT JOIN admin_resource_totals c ON c.kind='transfer' AND c.resource_id=t.id WHERE t.id=?`, candidate.id).Scan(&t.ID, &t.Status, &t.ExpiresAt, &t.MaxDownloads, &t.DownloadCount, &t.CreatedAt, &t.CompletedAt, &t.DownloadedAt, &t.Title, &t.Exhausted, &item.OwnerID, &item.HasManifest, &known, &files, &completed, &size)
		} else {
			item.Slot = &Slot{}
			s := item.Slot
			err = tx.QueryRowContext(ctx, `SELECT s.id,s.status,s.expires_at,s.created_at,s.receive_protocol,CASE WHEN length(s.recipient_public_key)<=128 THEN s.recipient_public_key ELSE NULL END,s.max_files,s.reserved_files,s.title,COALESCE(s.owner_id,''),c.inbox_known,c.file_count,c.completed_files,c.total_file_bytes FROM slots s LEFT JOIN admin_resource_totals c ON c.kind='slot' AND c.resource_id=s.id WHERE s.id=?`, candidate.id).Scan(&s.ID, &s.Status, &s.ExpiresAt, &s.CreatedAt, &s.ReceiveProtocol, &s.RecipientPublicKey, &s.MaxFiles, &s.ReservedFiles, &s.Title, &item.OwnerID, &known, &files, &completed, &size)
		}
		if err != nil {
			return page, err
		}
		if !all && item.OwnerID != actor {
			return page, ErrHistoryAccess
		}
		item.Summary = historySummary(known, files, completed, size)
		page.Resources = append(page.Resources, item)
	}
	return page, tx.Commit()
}
