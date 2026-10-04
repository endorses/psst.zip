package database

import (
	"database/sql"
	"fmt"
	"time"
)

// Transfer represents a row in the transfers table.
type Transfer struct {
	ID              string
	Status          string
	ExpiresAt       time.Time
	MaxDownloads    int
	DownloadCount   int
	CreatedAt       time.Time
	CompletedAt     sql.NullTime
	DownloadedAt    sql.NullTime
	DeleteTokenHash []byte
}

// File represents a row in the files table.
type File struct {
	ID             string
	TransferID     string
	Size           int64
	UploadOffset   int64
	UploadComplete bool
	DownloadCount  int
	CreatedAt      time.Time
}

// Slot represents a row in the slots table.
type Slot struct {
	ID              string
	Status          string
	ExpiresAt       time.Time
	CreatedAt       time.Time
	DeleteTokenHash []byte
}

// Queries wraps a *sql.DB and provides typed query methods.
type Queries struct {
	db *sql.DB
}

// NewQueries returns a new Queries instance.
func NewQueries(db *sql.DB) *Queries {
	return &Queries{db: db}
}

// --- Transfers ---

func (q *Queries) CreateTransfer(id string, expiresAt time.Time, maxDownloads int, deleteTokenHash []byte, owner ...string) error {
	_, err := q.db.Exec(
		`INSERT INTO transfers (id, status, expires_at, max_downloads, delete_token_hash, owner_id) VALUES (?, 'pending', ?, ?, ?, ?)`,
		id, expiresAt.UTC(), maxDownloads, deleteTokenHash, optionalOwner(owner),
	)
	return err
}

func (q *Queries) GetTransfer(id string) (*Transfer, error) {
	row := q.db.QueryRow(
		`SELECT id, status, expires_at, max_downloads, download_count, created_at, completed_at, downloaded_at, delete_token_hash FROM transfers WHERE id = ?`, id,
	)
	t := &Transfer{}
	if err := row.Scan(&t.ID, &t.Status, &t.ExpiresAt, &t.MaxDownloads, &t.DownloadCount, &t.CreatedAt, &t.CompletedAt, &t.DownloadedAt, &t.DeleteTokenHash); err != nil {
		return nil, err
	}
	return t, nil
}

func (q *Queries) CompleteTransfer(id string) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// First write acquires the SQLite writer lock before reading file counts.
	res, err := tx.Exec(`UPDATE transfers SET status='complete',completed_at=CURRENT_TIMESTAMP
 WHERE id=? AND status='pending'
 AND NOT EXISTS(SELECT 1 FROM files WHERE transfer_id=? AND upload_complete=0)`, id, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("transfer missing, already complete, or has incomplete uploads")
	}
	var count int64
	var received bool
	if err = tx.QueryRow(`SELECT COUNT(*) FROM files WHERE transfer_id=?`, id).Scan(&count); err != nil {
		return err
	}
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM slot_transfers WHERE transfer_id=?)`, id).Scan(&received); err != nil {
		return err
	}
	totals := TrafficTotals{FilesUploaded: count}
	if received {
		totals.ReceivedFilesUploaded = count
	} else {
		totals.StandaloneFilesUploaded = count
	}
	if err = addTraffic(tx, time.Now(), totals); err != nil {
		return err
	}
	return tx.Commit()
}

// ReserveFileDownload atomically consumes one GET allowance for this file.
// Each file gets max_downloads attempts, so a multi-file transfer remains usable.
// Transfer download_count is the minimum across its files: complete file sets.
func (q *Queries) ReserveFileDownload(transferID, fileID string) (bool, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE files SET download_count = download_count + 1
 WHERE id = ? AND transfer_id = ? AND upload_complete = 1
 AND EXISTS (SELECT 1 FROM transfers t WHERE t.id = files.transfer_id
 AND t.status = 'complete'
 AND (t.max_downloads <= 0 OR files.download_count < t.max_downloads))`, fileID, transferID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	_, err = tx.Exec(`UPDATE transfers SET download_count =
 (SELECT MIN(download_count) FROM files WHERE transfer_id = ?) WHERE id = ?`, transferID, transferID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// --- Files ---

func (q *Queries) CreateFile(id, transferID string, size int64) error {
	_, err := q.db.Exec(
		`INSERT INTO files (id, transfer_id, size) VALUES (?, ?, ?)`,
		id, transferID, size,
	)
	return err
}

func (q *Queries) GetFile(id string) (*File, error) {
	row := q.db.QueryRow(
		`SELECT id, transfer_id, size, upload_offset, upload_complete, download_count, created_at FROM files WHERE id = ?`, id,
	)
	f := &File{}
	if err := row.Scan(&f.ID, &f.TransferID, &f.Size, &f.UploadOffset, &f.UploadComplete, &f.DownloadCount, &f.CreatedAt); err != nil {
		return nil, err
	}
	return f, nil
}

func (q *Queries) UpdateFileOffset(id string, offset int64, complete bool) error {
	completeInt := 0
	if complete {
		completeInt = 1
	}
	_, err := q.db.Exec(
		`UPDATE files SET upload_offset = ?, upload_complete = ? WHERE id = ?`,
		offset, completeInt, id,
	)
	return err
}

func (q *Queries) ListFiles(transferID string) ([]File, error) {
	rows, err := q.db.Query(
		`SELECT id, transfer_id, size, upload_offset, upload_complete, download_count, created_at FROM files WHERE transfer_id = ?`, transferID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.TransferID, &f.Size, &f.UploadOffset, &f.UploadComplete, &f.DownloadCount, &f.CreatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// FileCount returns the number of files for a transfer.
func (q *Queries) FileCount(transferID string) (int, error) {
	row := q.db.QueryRow(`SELECT COUNT(*) FROM files WHERE transfer_id = ?`, transferID)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (q *Queries) FileCountAndSize(transferID string) (int, int64, error) {
	row := q.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM files WHERE transfer_id = ?`, transferID,
	)
	var count int
	var total int64
	if err := row.Scan(&count, &total); err != nil {
		return 0, 0, err
	}
	return count, total, nil
}

// --- Manifests ---

func (q *Queries) SaveManifest(transferID string, data []byte) error {
	_, err := q.db.Exec(
		`INSERT OR REPLACE INTO manifests (transfer_id, data) VALUES (?, ?)`,
		transferID, data,
	)
	return err
}

func (q *Queries) GetManifest(transferID string) ([]byte, error) {
	row := q.db.QueryRow(`SELECT data FROM manifests WHERE transfer_id = ?`, transferID)
	var data []byte
	if err := row.Scan(&data); err != nil {
		return nil, err
	}
	return data, nil
}

func (q *Queries) HasManifest(transferID string) (bool, error) {
	row := q.db.QueryRow(`SELECT COUNT(*) FROM manifests WHERE transfer_id = ?`, transferID)
	var count int
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// --- Slots ---

func (q *Queries) CreateSlot(id string, expiresAt time.Time, deleteTokenHash []byte, owner ...string) error {
	_, err := q.db.Exec(
		`INSERT INTO slots (id, status, expires_at, delete_token_hash, owner_id) VALUES (?, 'waiting', ?, ?, ?)`,
		id, expiresAt.UTC(), deleteTokenHash, optionalOwner(owner),
	)
	return err
}

func (q *Queries) GetSlot(id string) (*Slot, error) {
	row := q.db.QueryRow(
		`SELECT id, status, expires_at, created_at, delete_token_hash FROM slots WHERE id = ?`, id,
	)
	s := &Slot{}
	if err := row.Scan(&s.ID, &s.Status, &s.ExpiresAt, &s.CreatedAt, &s.DeleteTokenHash); err != nil {
		return nil, err
	}
	return s, nil
}

func (q *Queries) LinkSlotTransfer(slotID, transferID string) error {
	_, err := q.db.Exec(
		`INSERT INTO slot_transfers (slot_id, transfer_id) VALUES (?, ?)`,
		slotID, transferID,
	)
	return err
}

func (q *Queries) ListSlotTransfers(slotID string) ([]Transfer, error) {
	rows, err := q.db.Query(
		`SELECT t.id, t.status, t.expires_at, t.max_downloads, t.download_count, t.created_at, t.completed_at, t.downloaded_at, t.delete_token_hash
		 FROM transfers t
		 JOIN slot_transfers st ON st.transfer_id = t.id
		 WHERE st.slot_id = ?`, slotID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transfers []Transfer
	for rows.Next() {
		var t Transfer
		if err := rows.Scan(&t.ID, &t.Status, &t.ExpiresAt, &t.MaxDownloads, &t.DownloadCount, &t.CreatedAt, &t.CompletedAt, &t.DownloadedAt, &t.DeleteTokenHash); err != nil {
			return nil, err
		}
		transfers = append(transfers, t)
	}
	return transfers, rows.Err()
}

// --- Expiry / Cleanup ---

// ExpiredTransferIDs returns expired or revoked transfers. Exhausting a
// download quota removes payloads separately, keeping acknowledgement metadata.
func (q *Queries) ExpiredTransferIDs() ([]string, error) {
	// Compare parsed times, since historical rows use Go timestamp strings with
	// different timezone offsets, which SQLite cannot order chronologically.
	rows, err := q.db.Query(`SELECT id, expires_at, status FROM transfers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	var ids []string
	for rows.Next() {
		var id string
		var expiresAt time.Time
		var status string
		if err := rows.Scan(&id, &expiresAt, &status); err != nil {
			return nil, err
		}
		if status == "revoked" || !now.Before(expiresAt) {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// ExhaustedTransferIDs returns immutable transfers whose file GET allowances
// are all consumed. Their metadata stays until TTL so recipients can acknowledge
// after downloading and senders can subsequently retrieve that acknowledgement.
func (q *Queries) ExhaustedTransferIDs() ([]string, error) {
	rows, err := q.db.Query(`SELECT id FROM transfers
 WHERE status = 'complete' AND max_downloads > 0 AND download_count >= max_downloads`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (q *Queries) DeleteTransfer(id string) error {
	_, err := q.db.Exec(`DELETE FROM transfers WHERE id = ?`, id)
	return err
}

// ExpiredSlotIDs returns IDs of expired or revoked slots.
func (q *Queries) ExpiredSlotIDs() ([]string, error) {
	rows, err := q.db.Query(`SELECT id, expires_at, status FROM slots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	var ids []string
	for rows.Next() {
		var id string
		var expiresAt time.Time
		var status string
		if err := rows.Scan(&id, &expiresAt, &status); err != nil {
			return nil, err
		}
		if status == "revoked" || !now.Before(expiresAt) {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func (q *Queries) DeleteSlot(id string) error {
	_, err := q.db.Exec(`DELETE FROM slots WHERE id = ?`, id)
	return err
}

func (q *Queries) TransferSlotIDs(transferID string) ([]string, error) {
	rows, err := q.db.Query(`SELECT slot_id FROM slot_transfers WHERE transfer_id = ?`, transferID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AcknowledgeDownload records the first recipient report, never inferred from
// an HTTP response. File counters only ensure the transfer has been requested;
// they cannot prove decryption or saving to the recipient's filesystem.
func (q *Queries) AcknowledgeDownload(id string, at time.Time) (bool, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE transfers SET downloaded_at=?
 WHERE id=? AND status='complete' AND downloaded_at IS NULL
 AND EXISTS (SELECT 1 FROM files WHERE transfer_id=transfers.id)
 AND NOT EXISTS (SELECT 1 FROM files WHERE transfer_id=transfers.id AND download_count<=0)`, at.UTC(), id)
	if err != nil {
		return false, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed > 0 {
		var count int64
		if err = tx.QueryRow(`SELECT COUNT(*) FROM files WHERE transfer_id=?`, id).Scan(&count); err != nil {
			return false, err
		}
		if err = addTraffic(tx, at, TrafficTotals{FilesDelivered: count}); err != nil {
			return false, err
		}
	} else {
		// Receipt retries remain successful but never add to event aggregates twice.
		var acknowledged bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM transfers WHERE id=? AND status='complete' AND downloaded_at IS NOT NULL)`, id).Scan(&acknowledged); err != nil {
			return false, err
		}
		if !acknowledged {
			return false, nil
		}
	}
	return true, tx.Commit()
}

// CreateSlotTransfer is called while holding the slot mutation lock. The
// transaction prevents an unlinked transfer from surviving a failed create.
func (q *Queries) CreateSlotTransfer(slotID, id string, expiresAt time.Time, maxDownloads int, hash []byte, limits ...int) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	limit := 20
	if len(limits) > 0 && limits[0] > 0 {
		limit = limits[0]
	}
	reservation, err := tx.Exec(`UPDATE slots SET upload_count=upload_count+1 WHERE id=? AND status!='revoked' AND upload_count<?`, slotID, limit)
	if err != nil {
		return err
	}
	n, err := reservation.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSlotQuota
	}
	result, err := tx.Exec(`INSERT INTO transfers (id, status, expires_at, max_downloads, delete_token_hash,owner_id)
 SELECT ?, 'pending', ?, ?, ?,owner_id FROM slots WHERE id = ? AND status != 'revoked'`, id, expiresAt.UTC(), maxDownloads, hash, slotID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`INSERT INTO slot_transfers (slot_id, transfer_id) VALUES (?, ?)`, slotID, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (q *Queries) RevokeTransfer(id string) error {
	_, err := q.db.Exec(`UPDATE transfers SET status = 'revoked' WHERE id = ?`, id)
	return err
}

// RevokeSlot closes the slot and every linked transfer atomically before disk
// cleanup. Retaining these rows on cleanup failure keeps revocation retryable.
func (q *Queries) RevokeSlot(id string) ([]string, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE slots SET status = 'revoked' WHERE id = ?`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE transfers SET status = 'revoked' WHERE id IN (SELECT transfer_id FROM slot_transfers WHERE slot_id = ?)`, id); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT transfer_id FROM slot_transfers WHERE slot_id = ?`, id)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var child string
		if err := rows.Scan(&child); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, child)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}

func optionalOwner(owner []string) any {
	if len(owner) > 0 && owner[0] != "" {
		return owner[0]
	}
	return nil
}
