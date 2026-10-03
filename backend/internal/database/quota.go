package database

import "errors"

var ErrSlotQuota = errors.New("receive link upload limit reached")

// CreateFileWithQuota reserves declared bytes before accepting any content.
// The cumulative budget is not refunded by deleting child transfers.
func (q *Queries) CreateFileWithQuota(id, transfer string, size, limit int64) error {
	if limit <= 0 {
		limit = 5 * 1024 * 1024 * 1024
	}
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE slots SET reserved_bytes=reserved_bytes+? WHERE id IN (SELECT slot_id FROM slot_transfers WHERE transfer_id=?) AND reserved_bytes<=?-? AND status!='revoked'`, size, transfer, limit, size)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var slots int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM slot_transfers WHERE transfer_id=?`, transfer).Scan(&slots); err != nil {
			return err
		}
		if slots > 0 {
			return ErrSlotQuota
		}
	}
	if _, err := tx.Exec(`INSERT INTO files(id,transfer_id,size) VALUES(?,?,?)`, id, transfer, size); err != nil {
		return err
	}
	return tx.Commit()
}
