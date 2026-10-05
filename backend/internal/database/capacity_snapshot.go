package database

import (
	"context"
	"database/sql"
	"time"
)

// CapacitySnapshot is advisory encrypted-payload headroom, never a reservation.
// A nil byte value means the filesystem could not be checked, not zero capacity.
type CapacitySnapshot struct {
	CheckedAt          time.Time `json:"checked_at"`
	State              string    `json:"state"`
	Reason             string    `json:"reason,omitempty"`
	Scope              string    `json:"scope"`
	AvailableWireBytes *int64    `json:"available_wire_bytes"`
	AvailableFiles     int64     `json:"available_files"`
	AvailableTransfers int64     `json:"available_transfers"`
	AvailableSlots     int64     `json:"available_slots"`
}

type ResourceSnapshot struct {
	Policy   ResourcePolicy   `json:"policy"`
	Usage    ResourceUsage    `json:"usage"`
	Capacity CapacitySnapshot `json:"capacity"`
}

func headroom(limit, used int64) int64 { return max(0, limit-used) }

// ResourceSnapshot reads all database counters in one transaction. File-system
// observations can change immediately afterwards; allocation rechecks everything.
func (q *Queries) ResourceSnapshot(owner string) (ResourceSnapshot, error) {
	tx, err := q.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ResourceSnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	policy, err := readPolicy(tx)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	server, err := readUsage(tx, "")
	if err != nil {
		return ResourceSnapshot{}, err
	}
	usage := server
	capacity := CapacitySnapshot{CheckedAt: time.Now().UTC(), State: "ready", Scope: "server",
		AvailableFiles: headroom(policy.ServerFiles, server.Files), AvailableTransfers: headroom(policy.ServerTransfers, server.Transfers), AvailableSlots: headroom(policy.ServerSlots, server.Slots)}
	bytes := headroom(policy.ServerStorageBytes, server.ReservedBytes)
	if owner != "" {
		usage, err = readUsage(tx, owner)
		if err != nil {
			return ResourceSnapshot{}, err
		}
		capacity.Scope = "account"
		bytes = min(bytes, headroom(policy.AccountStorageBytes, usage.ReservedBytes))
		capacity.AvailableFiles = min(capacity.AvailableFiles, headroom(policy.AccountFiles, usage.Files))
		capacity.AvailableTransfers = min(capacity.AvailableTransfers, headroom(policy.AccountTransfers, usage.Transfers))
		capacity.AvailableSlots = min(capacity.AvailableSlots, headroom(policy.AccountSlots, usage.Slots))
	}
	var outstanding int64
	if err = tx.QueryRow(`SELECT COALESCE(SUM(MAX(0,size-upload_offset)),0) FROM files WHERE payload_deleted=0`).Scan(&outstanding); err != nil {
		return ResourceSnapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return ResourceSnapshot{}, err
	}
	disk, err := q.fileAllocationHeadroom(policy, outstanding)
	if err != nil {
		capacity.State = "unknown"
		capacity.Reason = "capacity_unavailable"
	} else {
		if bytes == 0 || capacity.AvailableFiles == 0 {
			capacity.State = "blocked"
			capacity.Reason = "resource_limit"
			bytes = 0
		} else if disk == 0 {
			capacity.State = "blocked"
			capacity.Reason = "disk_capacity"
		}
		bytes = min(bytes, disk)
		capacity.AvailableWireBytes = &bytes
	}
	return ResourceSnapshot{policy, usage, capacity}, nil
}
