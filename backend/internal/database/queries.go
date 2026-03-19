package database

import (
	"database/sql"
	"fmt"
	"time"
)

// Transfer represents a row in the transfers table.
type Transfer struct {
	ID            string
	Status        string
	ExpiresAt     time.Time
	MaxDownloads  int
	DownloadCount int
	CreatedAt     time.Time
	CompletedAt   sql.NullTime
}

// File represents a row in the files table.
type File struct {
	ID             string
	TransferID     string
	Size           int64
	UploadOffset   int64
	UploadComplete bool
	CreatedAt      time.Time
}

// Slot represents a row in the slots table.
type Slot struct {
	ID        string
	Status    string
	ExpiresAt time.Time
	CreatedAt time.Time
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

func (q *Queries) CreateTransfer(id string, expiresAt time.Time, maxDownloads int) error {
	_, err := q.db.Exec(
		`INSERT INTO transfers (id, status, expires_at, max_downloads) VALUES (?, 'pending', ?, ?)`,
		id, expiresAt, maxDownloads,
	)
	return err
}

func (q *Queries) GetTransfer(id string) (*Transfer, error) {
	row := q.db.QueryRow(
		`SELECT id, status, expires_at, max_downloads, download_count, created_at, completed_at FROM transfers WHERE id = ?`, id,
	)
	t := &Transfer{}
	if err := row.Scan(&t.ID, &t.Status, &t.ExpiresAt, &t.MaxDownloads, &t.DownloadCount, &t.CreatedAt, &t.CompletedAt); err != nil {
		return nil, err
	}
	return t, nil
}

func (q *Queries) CompleteTransfer(id string) error {
	// Check that all files are fully uploaded.
	var incomplete int
	err := q.db.QueryRow(
		`SELECT COUNT(*) FROM files WHERE transfer_id = ? AND upload_complete = 0`, id,
	).Scan(&incomplete)
	if err != nil {
		return err
	}
	if incomplete > 0 {
		return fmt.Errorf("transfer has %d incomplete file uploads", incomplete)
	}

	res, err := q.db.Exec(
		`UPDATE transfers SET status = 'complete', completed_at = CURRENT_TIMESTAMP WHERE id = ? AND status = 'pending'`, id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("transfer not found or already complete")
	}
	return nil
}

func (q *Queries) IncrementDownloadCount(id string) error {
	_, err := q.db.Exec(`UPDATE transfers SET download_count = download_count + 1 WHERE id = ?`, id)
	return err
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
		`SELECT id, transfer_id, size, upload_offset, upload_complete, created_at FROM files WHERE id = ?`, id,
	)
	f := &File{}
	if err := row.Scan(&f.ID, &f.TransferID, &f.Size, &f.UploadOffset, &f.UploadComplete, &f.CreatedAt); err != nil {
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
		`SELECT id, transfer_id, size, upload_offset, upload_complete, created_at FROM files WHERE transfer_id = ?`, transferID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.TransferID, &f.Size, &f.UploadOffset, &f.UploadComplete, &f.CreatedAt); err != nil {
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

func (q *Queries) CreateSlot(id string, expiresAt time.Time) error {
	_, err := q.db.Exec(
		`INSERT INTO slots (id, status, expires_at) VALUES (?, 'waiting', ?)`,
		id, expiresAt,
	)
	return err
}

func (q *Queries) GetSlot(id string) (*Slot, error) {
	row := q.db.QueryRow(
		`SELECT id, status, expires_at, created_at FROM slots WHERE id = ?`, id,
	)
	s := &Slot{}
	if err := row.Scan(&s.ID, &s.Status, &s.ExpiresAt, &s.CreatedAt); err != nil {
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
		`SELECT t.id, t.status, t.expires_at, t.max_downloads, t.download_count, t.created_at, t.completed_at
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
		if err := rows.Scan(&t.ID, &t.Status, &t.ExpiresAt, &t.MaxDownloads, &t.DownloadCount, &t.CreatedAt, &t.CompletedAt); err != nil {
			return nil, err
		}
		transfers = append(transfers, t)
	}
	return transfers, rows.Err()
}

// --- Expiry / Cleanup ---

// ExpiredTransferIDs returns IDs of transfers that have passed their expiry
// time or exceeded their download limit.
func (q *Queries) ExpiredTransferIDs() ([]string, error) {
	rows, err := q.db.Query(
		`SELECT id FROM transfers WHERE expires_at < CURRENT_TIMESTAMP
		 OR (max_downloads > 0 AND download_count >= max_downloads)`,
	)
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

// ExpiredSlotIDs returns IDs of slots past their expiry time.
func (q *Queries) ExpiredSlotIDs() ([]string, error) {
	rows, err := q.db.Query(`SELECT id FROM slots WHERE expires_at < CURRENT_TIMESTAMP`)
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

func (q *Queries) DeleteSlot(id string) error {
	_, err := q.db.Exec(`DELETE FROM slots WHERE id = ?`, id)
	return err
}
