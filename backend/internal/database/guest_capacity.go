package database

import (
	"context"
	"database/sql"
	"time"
)

// GuestUploadCapacity describes one new submission without exposing its owner's
// identity, quota policy, current usage, or other receive submissions.
// It is advisory: every allocation still rechecks authoritative admission.
type GuestUploadCapacity struct {
	CheckedAt            time.Time `json:"checked_at"`
	State                string    `json:"state"`
	Reason               string    `json:"reason,omitempty"`
	AvailableWireBytes   *int64    `json:"available_wire_bytes"`
	AvailableFiles       *int64    `json:"available_files"`
	ManifestReserveBytes int64     `json:"manifest_reserve_bytes"`
}

type GuestCapacityLimits struct {
	SlotBytes        int64
	SlotTransfers    int
	FilesPerTransfer int
	ManifestBytes    int64
}

func (l GuestCapacityLimits) normalized() GuestCapacityLimits {
	if l.SlotBytes <= 0 {
		l.SlotBytes = 5 * 1024 * 1024 * 1024
	}
	if l.SlotTransfers <= 0 {
		l.SlotTransfers = 20
	}
	l.FilesPerTransfer = EffectiveTransferFileLimit(l.FilesPerTransfer)
	if l.ManifestBytes <= 0 || l.ManifestBytes > MaxManifestBytes {
		l.ManifestBytes = MaxManifestBytes
	}
	return l
}

type GuestSlotSnapshot struct {
	Slot          *Slot
	LegacyOwner   bool
	OwnerDisabled bool
	Capacity      GuestUploadCapacity
}

type contextualRowQuery struct {
	ctx context.Context
	tx  *sql.Tx
}

func (q contextualRowQuery) QueryRow(query string, args ...any) *sql.Row {
	return q.tx.QueryRowContext(q.ctx, query, args...)
}

// GuestSlotCapacity reads link lifetime allowances, owner state and canonical
// account/server usage in one cancellable snapshot. It reserves nothing. Public
// receive keys identify recipients; they are not standalone send-link secrets.
func (q *Queries) GuestSlotCapacity(ctx context.Context, id string, limits GuestCapacityLimits) (GuestSlotSnapshot, error) {
	limits = limits.normalized()
	out := GuestSlotSnapshot{Slot: &Slot{}, Capacity: GuestUploadCapacity{CheckedAt: time.Now().UTC(), State: "unknown", Reason: "capacity_unavailable", ManifestReserveBytes: limits.ManifestBytes}}
	tx, err := q.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	var owner string
	s := out.Slot
	err = tx.QueryRowContext(ctx, `SELECT s.id,s.status,s.expires_at,s.receive_protocol,s.recipient_public_key,s.max_files,s.reserved_files,s.reserved_bytes,s.upload_count,s.title,COALESCE(s.owner_id,''),COALESCE(u.disabled,1) FROM slots s LEFT JOIN users u ON u.id=s.owner_id WHERE s.id=?`, id).Scan(&s.ID, &s.Status, &s.ExpiresAt, &s.ReceiveProtocol, &s.RecipientPublicKey, &s.MaxFiles, &s.ReservedFiles, &s.ReservedBytes, &s.UploadCount, &s.Title, &owner, &out.OwnerDisabled)
	if err != nil {
		return out, err
	}
	out.LegacyOwner = owner == ""
	if out.LegacyOwner || out.OwnerDisabled || s.Status == "revoked" || !out.Capacity.CheckedAt.Before(s.ExpiresAt) {
		return out, tx.Commit()
	}
	blocked := func(reason string) GuestSlotSnapshot {
		zeroBytes, zeroFiles := int64(0), int64(0)
		out.Capacity.State = "blocked"
		out.Capacity.Reason = reason
		out.Capacity.AvailableWireBytes = &zeroBytes
		out.Capacity.AvailableFiles = &zeroFiles
		return out
	}
	linkBytes := headroom(headroom(limits.SlotBytes, s.ReservedBytes), limits.ManifestBytes)
	linkFiles := int64(limits.FilesPerTransfer)
	if s.MaxFiles > 0 {
		linkFiles = min(linkFiles, headroom(int64(s.MaxFiles), s.ReservedFiles))
	}
	if s.ReceiveProtocol != 2 || s.UploadCount >= limits.SlotTransfers || linkFiles == 0 || linkBytes < 60 {
		return blocked("link_limit"), tx.Commit()
	}
	reader := contextualRowQuery{ctx, tx}
	policy, err := readPolicy(reader)
	if err != nil {
		return out, err
	}
	server, err := readUsage(reader, "")
	if err != nil {
		return out, err
	}
	account, err := readUsage(reader, owner)
	if err != nil {
		return out, err
	}
	bytes := min(linkBytes, headroom(headroom(policy.ServerStorageBytes, server.ReservedBytes), limits.ManifestBytes), headroom(headroom(policy.AccountStorageBytes, account.ReservedBytes), limits.ManifestBytes))
	files := min(linkFiles, headroom(policy.ServerFiles, server.Files), headroom(policy.AccountFiles, account.Files))
	if bytes < 60 || files == 0 || headroom(policy.ServerTransfers, server.Transfers) == 0 || headroom(policy.AccountTransfers, account.Transfers) == 0 {
		return blocked("capacity_limit"), tx.Commit()
	}
	var outstanding int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(MAX(0,size-upload_offset)),0) FROM files WHERE payload_deleted=0`).Scan(&outstanding); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	disk, err := q.guestAllocationHeadroom(ctx, policy, outstanding, limits.ManifestBytes)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, nil
	}
	bytes = min(bytes, disk)
	// Even an empty chunked-v1 file occupies a 60-byte authenticated frame.
	files = min(files, bytes/60)
	if bytes < 60 || files == 0 {
		return blocked("capacity_limit"), nil
	}
	out.Capacity.State = "ready"
	out.Capacity.Reason = ""
	out.Capacity.AvailableWireBytes = &bytes
	out.Capacity.AvailableFiles = &files
	return out, nil
}

func (q *Queries) guestAllocationHeadroom(ctx context.Context, p ResourcePolicy, outstanding, manifest int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	q.capacity.RLock()
	paths := append([]capacityPath(nil), q.capacity.paths...)
	probe := q.capacity.probe
	q.capacity.RUnlock()
	volumes := map[uint64]checkedVolume{}
	unknown := false
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if probe == nil {
			unknown = true
			continue
		}
		capacity, err := probe(path.path)
		if err != nil {
			unknown = true
			continue
		}
		v, exists := volumes[capacity.device]
		if exists {
			capacity.available = min(capacity.available, v.capacity.available)
			capacity.total = max(capacity.total, v.capacity.total)
		}
		v.capacity = capacity
		v.payload = v.payload || path.payload
		v.database = v.database || path.database
		volumes[capacity.device] = v
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(volumes) == 0 {
		return 0, ErrDiskCapacity
	}
	available := int64(1<<63 - 1)
	payloadFound := false
	databaseFound := false
	for _, v := range volumes {
		reserve := max(p.ReserveDiskBytes, v.capacity.total/100*p.ReserveDiskPercent)
		room := headroom(v.capacity.available, reserve)
		room = headroom(room, 1024*1024) // Metadata/WAL floor also used by allocation.
		if v.payload {
			payloadFound = true
			room = headroom(room, outstanding)
		}
		// Manifest replacement may occupy the database plus WAL simultaneously.
		// On a shared volume this is charged alongside outstanding payload writes.
		if v.database {
			databaseFound = true
			if room < 2*manifest {
				return 0, nil
			}
			room -= 2 * manifest
		}
		if v.payload {
			if !v.database {
				room = headroom(room, manifest)
			}
			available = min(available, room)
		}
	}
	if available < 60 {
		return 0, nil
	}
	if !payloadFound || !databaseFound || unknown {
		return 0, ErrDiskCapacity
	}
	return available, nil
}
