package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CreateReceiveSlot persists immutable submission policy in the same allocation.
func (q *Queries) CreateReceiveSlot(id string, expires time.Time, hash []byte, owner string, protocol int, publicKey string, maxFiles int) error {
	return q.allocationExec(0, false, &expires, `INSERT INTO slots(id,status,expires_at,delete_token_hash,owner_id,receive_protocol,recipient_public_key,max_files) VALUES(?,'waiting',?,?,?,?,?,?)`, id, expires.UTC(), hash, optionalOwner([]string{owner}), protocol, publicKey, maxFiles)
}
func (q *Queries) CompletedSlotFiles(slot string) (int64, error) {
	var count int64
	err := q.db.QueryRow(`SELECT COUNT(*) FROM files f JOIN slot_transfers st ON st.transfer_id=f.transfer_id JOIN transfers t ON t.id=f.transfer_id WHERE st.slot_id=? AND t.status='complete' AND f.upload_complete=1`, slot).Scan(&count)
	return count, err
}
func (q *Queries) TransferReceiveProtocol(transfer string) (int, error) {
	var protocol int
	err := q.db.QueryRow(`SELECT COALESCE(MAX(s.receive_protocol),0) FROM slots s JOIN slot_transfers st ON st.slot_id=s.id WHERE st.transfer_id=?`, transfer).Scan(&protocol)
	return protocol, err
}

// InboxEventSessions checks all distinct readers of one inbox in a single query.
// Both periodic lifecycle checks and checks immediately before event disclosure
// use this predicate, so ownership changes cannot leave an old stream authorized.
func (q *Queries) InboxEventSessions(ctx context.Context, slot, owner string, sessions []string) (map[string]bool, error) {
	if len(sessions) == 0 || len(sessions) > 16 {
		return nil, fmt.Errorf("invalid inbox event reader count")
	}
	args := []any{slot, owner}
	placeholders := make([]string, len(sessions))
	for i, session := range sessions {
		placeholders[i] = "?"
		args = append(args, session)
	}
	rows, err := q.db.QueryContext(ctx, `SELECT se.id, se.expires_at, sl.expires_at
 FROM slots sl JOIN users u ON u.id=sl.owner_id JOIN sessions se ON se.user_id=u.id
 WHERE sl.id=? AND sl.owner_id=? AND sl.status!='revoked'
 AND u.disabled=0 AND u.role='user' AND u.must_change_password=0
 AND se.id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	active := make(map[string]bool, len(sessions))
	now := time.Now()
	for rows.Next() {
		var id string
		var sessionExpiry, slotExpiry time.Time
		if err := rows.Scan(&id, &sessionExpiry, &slotExpiry); err != nil {
			return nil, err
		}
		if now.Before(sessionExpiry) && now.Before(slotExpiry) {
			active[id] = true
		}
	}
	return active, rows.Err()
}
