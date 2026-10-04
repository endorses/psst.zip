package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

var ErrInboxPaginationRequired = errors.New("inbox requires paginated access")

type InboxSummary struct {
	State          string `json:"state"`
	CompletedFiles *int64 `json:"completed_files"`
	FileCount      *int64 `json:"file_count"`
	TotalSize      *int64 `json:"total_size"`
}
type InboxTransfer struct {
	TransferID string `json:"transfer_id"`
	Status     string `json:"status"`
	FileCount  int    `json:"file_count"`
}
type InboxPage struct {
	Slot       *Slot
	Transfers  []InboxTransfer
	Summary    InboxSummary
	NextCursor *string
}
type inboxCursor struct {
	Version int    `json:"v"`
	Slot    string `json:"slot"`
	After   string `json:"after"`
}

func decodeInboxCursor(slot, after string) (string, error) {
	if after == "" {
		return "", nil
	}
	if len(after) > 512 {
		return "", ErrInvalidPage
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(after)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != after {
		return "", ErrInvalidPage
	}
	var cursor inboxCursor
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Version != 1 || cursor.Slot != slot || cursor.After == "" || len(cursor.After) > 128 || strings.ContainsRune(cursor.After, 0) {
		return "", ErrInvalidPage
	}
	canonical, _ := json.Marshal(cursor)
	if !bytes.Equal(canonical, raw) {
		return "", ErrInvalidPage
	}
	return cursor.After, nil
}

// OwnerInboxPage consumes bounded raw membership rows before lifecycle filtering.
// Its slot policy, page metadata and retained totals share one read snapshot.
// An empty visible page can have a continuation, so callers must use the cursor.
func (q *Queries) OwnerInboxPage(ctx context.Context, slotID, owner string, limit int, after string, legacy bool, now time.Time) (InboxPage, error) {
	out := InboxPage{Slot: &Slot{}, Transfers: []InboxTransfer{}, Summary: InboxSummary{State: "updating"}}
	if limit < 1 || limit > 100 || (legacy && (limit != 100 || after != "")) {
		return out, ErrInvalidPage
	}
	cursor, err := decodeInboxCursor(slotID, after)
	if err != nil {
		return out, err
	}
	tx, err := q.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	s := out.Slot
	query := `SELECT id,status,expires_at,created_at,receive_protocol,CASE WHEN length(recipient_public_key)<=128 THEN recipient_public_key ELSE NULL END,max_files,reserved_files,reserved_bytes,upload_count FROM slots WHERE id=?`
	args := []any{slotID}
	if owner != "" {
		query += ` AND owner_id=? AND EXISTS(SELECT 1 FROM users WHERE id=slots.owner_id AND disabled=0 AND role='user' AND must_change_password=0)`
		args = append(args, owner)
	}
	err = tx.QueryRowContext(ctx, query, args...).Scan(&s.ID, &s.Status, &s.ExpiresAt, &s.CreatedAt, &s.ReceiveProtocol, &s.RecipientPublicKey, &s.MaxFiles, &s.ReservedFiles, &s.ReservedBytes, &s.UploadCount)
	if err != nil {
		return out, err
	}
	if s.Status == "revoked" || !now.Before(s.ExpiresAt) {
		return out, tx.Commit()
	}
	rows, err := tx.QueryContext(ctx, `SELECT transfer_id FROM slot_transfers WHERE slot_id=? AND transfer_id>? ORDER BY transfer_id LIMIT ?`, slotID, cursor, limit+1)
	if err != nil {
		return out, err
	}
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > limit {
		if legacy {
			return out, ErrInboxPaginationRequired
		}
		ids = ids[:limit]
		raw, _ := json.Marshal(inboxCursor{Version: 1, Slot: slotID, After: ids[len(ids)-1]})
		next := base64.RawURLEncoding.EncodeToString(raw)
		out.NextCursor = &next
	}
	// At most 100 primary-key lookups and 100 indexed, 101-row file probes.
	// Filtering before the file probe avoids reading hidden payload metadata.
	var pageFiles, pageCompleted, pageBytes int64
	for _, id := range ids {
		var item InboxTransfer
		var expires time.Time
		var pending sql.NullTime
		err = tx.QueryRowContext(ctx, `SELECT id,status,expires_at,pending_expires_at FROM transfers WHERE id=?`, id).Scan(&item.TransferID, &item.Status, &expires, &pending)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return out, err
		}
		if item.Status == "revoked" || !now.Before(expires) || (item.Status == "pending" && pending.Valid && !now.Before(pending.Time)) {
			continue
		}
		var uploaded, size int64
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(upload_complete),0),COALESCE(SUM(size),0) FROM (SELECT upload_complete,size FROM files INDEXED BY files_transfer_id_order WHERE transfer_id=? LIMIT 101)`, id).Scan(&item.FileCount, &uploaded, &size)
		if err != nil {
			return out, err
		}
		if item.FileCount > MaxTransferFiles {
			return out, ErrTransferFileLimit
		}
		if err = addCounter(&pageFiles, int64(item.FileCount)); err != nil {
			return out, err
		}
		if err = addCounter(&pageBytes, size); err != nil {
			return out, err
		}
		if item.Status == "complete" {
			if err = addCounter(&pageCompleted, uploaded); err != nil {
				return out, err
			}
		}
		out.Transfers = append(out.Transfers, item)
	}
	var known bool
	var completed, files, size int64
	err = tx.QueryRowContext(ctx, `SELECT inbox_known,completed_files,file_count,total_file_bytes FROM admin_resource_totals WHERE kind='slot' AND resource_id=?`, slotID).Scan(&known, &completed, &files, &size)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil && known && completed >= pageCompleted && files >= completed && files >= pageFiles && size >= pageBytes {
		out.Summary = InboxSummary{State: "ready", CompletedFiles: &completed, FileCount: &files, TotalSize: &size}
	}
	return out, tx.Commit()
}
