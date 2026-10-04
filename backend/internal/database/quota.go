package database

import "errors"

var ErrSlotFileQuota = errors.New("receive file allowance exhausted")

var ErrTransferFileQuota = errors.New("maximum file count reached for this transfer")

// EffectiveTransferFileLimit preserves stricter operator limits within the protocol ceiling.
func EffectiveTransferFileLimit(configured int) int {
	if configured <= 0 || configured > MaxTransferFiles {
		return MaxTransferFiles
	}
	return configured
}

var ErrSlotQuota = errors.New("receive link upload limit reached")

// CreateFileWithQuota reserves declared bytes before accepting any content.
// The cumulative budget is not refunded by deleting child transfers.
func (q *Queries) CreateFileWithQuota(id, transfer string, size, limit int64, fileLimits ...int) error {
	if size < 0 {
		return errors.New("invalid file size")
	}
	if limit <= 0 {
		limit = 5 * 1024 * 1024 * 1024
	}
	tx, err := q.beginAllocation(size, false, nil)
	if err != nil {
		return ResourceError(err)
	}
	defer tx.Rollback()
	fileLimit := MaxTransferFiles
	if len(fileLimits) > 0 {
		fileLimit = EffectiveTransferFileLimit(fileLimits[0])
	}
	// beginAllocation holds the SQLite writer lock: another allocator cannot
	// consume the last file slot between this bounded probe and the insert.
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM files INDEXED BY files_transfer_id_order WHERE transfer_id=? LIMIT ?)`, transfer, fileLimit).Scan(&count); err != nil {
		return err
	}
	if count >= fileLimit {
		return ErrTransferFileQuota
	}
	result, err := tx.Exec(`UPDATE slots SET reserved_bytes=reserved_bytes+?,reserved_files=reserved_files+1 WHERE id IN (SELECT slot_id FROM slot_transfers WHERE transfer_id=?) AND reserved_bytes<=?-? AND status!='revoked' AND receive_protocol=2 AND (max_files=0 OR reserved_files<max_files) AND reserved_files<9223372036854775807`, size, transfer, limit, size)
	if err != nil {
		return ResourceError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ResourceError(err)
	}
	if n == 0 {
		var slots int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM slot_transfers WHERE transfer_id=?`, transfer).Scan(&slots); err != nil {
			return ResourceError(err)
		}
		if slots > 0 {
			var exhausted bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM slots WHERE id IN (SELECT slot_id FROM slot_transfers WHERE transfer_id=?) AND max_files>0 AND reserved_files>=max_files)`, transfer).Scan(&exhausted); err != nil {
				return ResourceError(err)
			}
			if exhausted {
				return ErrSlotFileQuota
			}
			return ErrSlotQuota
		}
	}
	if _, err := tx.Exec(`INSERT INTO files(id,transfer_id,size) VALUES(?,?,?)`, id, transfer, size); err != nil {
		return ResourceError(err)
	}
	return tx.Commit()
}
