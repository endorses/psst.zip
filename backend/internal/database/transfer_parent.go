package database

import (
	"context"
	"errors"
	"time"
)

// ErrAmbiguousTransferParent rejects legacy or damaged records that associate a
// submission with multiple inboxes. Choosing one could misattribute access,
// traffic, or stream cancellation; returning every parent would be unbounded.
var ErrAmbiguousTransferParent = errors.New("transfer has multiple inbox parents")

func (q *Queries) TransferSlotIDs(transferID string) ([]string, error) {
	return q.TransferSlotIDsContext(context.Background(), transferID)
}

// TransferSlotIDsContext returns no parent for standalone sends and one parent
// for receive submissions. A two-row indexed probe detects unsupported records
// without enumerating their memberships. No partial result escapes on error.
func (q *Queries) TransferSlotIDsContext(parent context.Context, transferID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	rows, err := q.db.QueryContext(ctx, `SELECT slot_id FROM slot_transfers WHERE transfer_id=? ORDER BY slot_id LIMIT 2`, transferID)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 1 {
		return nil, ErrAmbiguousTransferParent
	}
	return ids, nil
}
