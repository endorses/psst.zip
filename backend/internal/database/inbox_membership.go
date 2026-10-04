package database

import (
	"context"
	"database/sql"
	"time"
)

// InboxOwnerContext resolves only the exact inbox row. Its context must not be
// propagated to a long-lived event stream after authorization completes.
func (q *Queries) InboxOwnerContext(ctx context.Context, slotID string) (string, error) {
	var owner sql.NullString
	err := q.db.QueryRowContext(ctx, `SELECT owner_id FROM slots WHERE id=?`, slotID).Scan(&owner)
	return owner.String, err
}

// InboxTransferMembership contains one exact relationship and the lifecycle
// policy needed to decide whether it may still be disclosed.
type InboxTransferMembership struct {
	SlotID, TransferID               string
	ReceiveProtocol                  int
	RecipientPublicKey               string
	SlotStatus, TransferStatus       string
	SlotExpiresAt, TransferExpiresAt time.Time
	PendingExpiresAt                 sql.NullTime
}

const inboxTransferMembershipQuery = `SELECT s.id,t.id,s.receive_protocol,
 substr(s.recipient_public_key,1,44),s.status,s.expires_at,t.status,t.expires_at,t.pending_expires_at
 FROM slot_transfers st
 JOIN slots s ON s.id=st.slot_id
 JOIN transfers t ON t.id=st.transfer_id
 WHERE st.slot_id=? AND st.transfer_id=? AND s.owner_id=?`

// InboxTransferMembershipContext uses the composite relationship primary key,
// followed by two exact primary-key lookups. It never enumerates siblings,
// counts files, or loads a manifest. Rechecking the owner in the same query
// prevents a changed owner from retaining authority after middleware runs.
func (q *Queries) InboxTransferMembershipContext(ctx context.Context, slotID, transferID, ownerID string) (*InboxTransferMembership, error) {
	entry := &InboxTransferMembership{}
	err := q.db.QueryRowContext(ctx, inboxTransferMembershipQuery, slotID, transferID, ownerID).Scan(
		&entry.SlotID, &entry.TransferID, &entry.ReceiveProtocol, &entry.RecipientPublicKey,
		&entry.SlotStatus, &entry.SlotExpiresAt, &entry.TransferStatus, &entry.TransferExpiresAt, &entry.PendingExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return entry, nil
}
