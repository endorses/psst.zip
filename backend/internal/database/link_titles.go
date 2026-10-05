package database

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidTitle = errors.New("use a title of at most 200 Unicode characters without control characters")
var ErrTitleOwnership = errors.New("link title is unavailable to this account")

// Titles are deliberately shared descriptive metadata, never private filenames.
func NormalizeLinkTitle(title *string) (*string, error) {
	if title == nil {
		return nil, nil
	}
	if !utf8.ValidString(*title) {
		return nil, ErrInvalidTitle
	}
	for _, r := range *title {
		if unicode.IsControl(r) {
			return nil, ErrInvalidTitle
		}
	}
	value := strings.TrimSpace(*title)
	if value == "" {
		return nil, nil
	}
	if len(value) > 800 || utf8.RuneCountInString(value) > 200 {
		return nil, ErrInvalidTitle
	}
	return &value, nil
}

func (q *Queries) CreateTransferWithTitle(id string, expiresAt time.Time, maxDownloads int, hash []byte, owner string, title *string) error {
	normalized, err := NormalizeLinkTitle(title)
	if err != nil {
		return err
	}
	return q.allocationExec(0, false, &expiresAt, `INSERT INTO transfers(id,status,expires_at,max_downloads,delete_token_hash,owner_id,title) VALUES(?,'pending',?,?,?,?,?)`, id, expiresAt.UTC(), maxDownloads, hash, optionalOwner([]string{owner}), normalized)
}

// Keep complete for final receipts and reader-aware cleanup. Allowance admission
// updates download_count to MIN(file.download_count) in the same writer
// transaction; finalized file membership is immutable through the API, and
// payload cleanup keeps the file metadata. A positive minimum therefore proves
// a nonempty file set. Reuse that authoritative counter without scanning files,
// including restored oversized records; History and cleanup use it as well.
func exhaustedSQL(alias string) string {
	return `(` + alias + `.status='complete' AND ` + alias + `.max_downloads>0 AND ` + alias + `.download_count>=` + alias + `.max_downloads)`
}

func (q *Queries) RenameLinkTitle(ctx context.Context, kind, id, owner string, title *string) error {
	if kind != "transfer" && kind != "slot" {
		return ErrTitleOwnership
	}
	normalized, err := NormalizeLinkTitle(title)
	if err != nil {
		return err
	}
	table := kind + "s"
	query := `UPDATE ` + table + ` SET title=? WHERE id=? AND owner_id=? AND status!='revoked' AND EXISTS(SELECT 1 FROM users u WHERE u.id=` + table + `.owner_id AND u.role='user' AND u.disabled=0 AND u.must_change_password=0)`
	if kind == "transfer" {
		query += ` AND NOT EXISTS(SELECT 1 FROM slot_transfers WHERE transfer_id=transfers.id)`
	}
	result, err := q.db.ExecContext(ctx, query, normalized, id, owner)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrTitleOwnership
	}
	return nil
}
