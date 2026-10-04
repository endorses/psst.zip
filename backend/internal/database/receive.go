package database

import "time"

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

// Inbox streams must stop when their session/account loses read authorization.
func (q *Queries) InboxSessionActive(session, user string) (bool, error) {
	var expires time.Time
	err := q.db.QueryRow(`SELECT s.expires_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id=? AND s.user_id=? AND u.disabled=0 AND u.role='user' AND u.must_change_password=0`, session, user).Scan(&expires)
	return err == nil && time.Now().Before(expires), err
}
