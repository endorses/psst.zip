package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ResourcePolicy is a finite persistent admission policy. Reductions never delete data.
type ResourcePolicy struct {
	ServerStorageBytes   int64 `json:"server_storage_bytes"`
	AccountStorageBytes  int64 `json:"account_storage_bytes"`
	ServerFiles          int64 `json:"server_files"`
	AccountFiles         int64 `json:"account_files"`
	ServerTransfers      int64 `json:"server_transfers"`
	AccountTransfers     int64 `json:"account_transfers"`
	ServerSlots          int64 `json:"server_slots"`
	AccountSlots         int64 `json:"account_slots"`
	MaxRetentionSeconds  int64 `json:"max_retention_seconds"`
	PendingUploadSeconds int64 `json:"pending_upload_seconds"`
	ReserveDiskBytes     int64 `json:"reserve_disk_bytes"`
	ReserveDiskPercent   int64 `json:"reserve_disk_percent"`
}

type ResourceUsage struct {
	OccupiedBytesEstimate bool  `json:"occupied_bytes_estimate"`
	ReservedBytes         int64 `json:"reserved_bytes"`
	OccupiedBytes         int64 `json:"occupied_bytes"`
	Files                 int64 `json:"files"`
	Transfers             int64 `json:"transfers"`
	Slots                 int64 `json:"slots"`
}

var ErrResourceLimit = errors.New("resource limit reached")
var ErrDiskCapacity = errors.New("insufficient disk capacity above the safety reserve")
var ErrRetentionLimit = errors.New("requested retention exceeds the server policy")

func ResourceError(err error) error {
	if err != nil && strings.Contains(err.Error(), "resource_limit") {
		return fmt.Errorf("%w: %s", ErrResourceLimit, err.Error())
	}
	return err
}

const policyColumns = `server_storage_bytes,account_storage_bytes,server_files,account_files,server_transfers,account_transfers,server_slots,account_slots,max_retention_seconds,pending_upload_seconds,reserve_disk_bytes,reserve_disk_percent`

type rowQuery interface{ QueryRow(string, ...any) *sql.Row }

func readPolicy(q rowQuery) (ResourcePolicy, error) {
	var p ResourcePolicy
	err := q.QueryRow(`SELECT `+policyColumns+` FROM resource_policy WHERE id=1`).Scan(&p.ServerStorageBytes, &p.AccountStorageBytes, &p.ServerFiles, &p.AccountFiles, &p.ServerTransfers, &p.AccountTransfers, &p.ServerSlots, &p.AccountSlots, &p.MaxRetentionSeconds, &p.PendingUploadSeconds, &p.ReserveDiskBytes, &p.ReserveDiskPercent)
	return p, err
}
func (q *Queries) ResourcePolicy() (ResourcePolicy, error) { return readPolicy(q.db) }
func (p ResourcePolicy) Validate() error {
	for _, n := range []int64{p.ServerStorageBytes, p.AccountStorageBytes} {
		if n < 1024*1024 || n > 1<<50 {
			return errors.New("storage budgets must be between 1 MiB and 1 PiB")
		}
	}
	for _, n := range []int64{p.ServerFiles, p.AccountFiles, p.ServerTransfers, p.AccountTransfers, p.ServerSlots, p.AccountSlots} {
		if n < 1 || n > 1000000 {
			return errors.New("object budgets must be between 1 and 1000000")
		}
	}
	if p.MaxRetentionSeconds < 60 || p.MaxRetentionSeconds > 365*86400 || p.PendingUploadSeconds < 60 || p.PendingUploadSeconds > p.MaxRetentionSeconds {
		return errors.New("retention must be 60 seconds to 365 days; pending lifetime must not exceed retention")
	}
	if p.ReserveDiskBytes < 1024*1024 || p.ReserveDiskBytes > 1<<40 || p.ReserveDiskPercent < 1 || p.ReserveDiskPercent > 50 {
		return errors.New("disk reserve must be 1 MiB to 1 TiB and 1 to 50 percent")
	}
	return nil
}
func (q *Queries) SetResourcePolicy(p ResourcePolicy, actors ...*AdminActor) error {
	if err := p.Validate(); err != nil {
		return err
	}
	tx, err := q.beginAdminMutation(actors)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	auditChanged := false
	if optionalAdminActor(actors) != nil {
		previous, readErr := readPolicy(tx)
		if readErr != nil {
			return readErr
		}
		auditChanged = previous != p
	}

	_, err = tx.Exec(`UPDATE resource_policy SET server_storage_bytes=?,account_storage_bytes=?,server_files=?,account_files=?,server_transfers=?,account_transfers=?,server_slots=?,account_slots=?,max_retention_seconds=?,pending_upload_seconds=?,reserve_disk_bytes=?,reserve_disk_percent=? WHERE id=1`, p.ServerStorageBytes, p.AccountStorageBytes, p.ServerFiles, p.AccountFiles, p.ServerTransfers, p.AccountTransfers, p.ServerSlots, p.AccountSlots, p.MaxRetentionSeconds, p.PendingUploadSeconds, p.ReserveDiskBytes, p.ReserveDiskPercent)
	if err != nil {
		return err
	}
	if auditChanged {
		if err = q.auditAdminMutation(tx, actors, "settings.resource_policy_changed", "server", "", false); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func readUsage(q rowQuery, owner string) (ResourceUsage, error) {
	where := ""
	var args []any
	if owner != "" {
		where = " WHERE owner_id=?"
		args = append(args, owner)
	}
	u := ResourceUsage{OccupiedBytesEstimate: true}
	err := q.QueryRow(`SELECT COALESCE(SUM(reserved_bytes),0),COALESCE(SUM(occupied_bytes),0),COALESCE(SUM(files),0),COALESCE(SUM(transfers),0),COALESCE(SUM(slots),0) FROM resource_usage`+where, args...).Scan(&u.ReservedBytes, &u.OccupiedBytes, &u.Files, &u.Transfers, &u.Slots)
	return u, err
}
func (q *Queries) ResourceUsage(owner string) (ResourceUsage, error) { return readUsage(q.db, owner) }

// beginAllocation takes SQLite's write lock before capacity checks. This prevents
// two connections/processes from allocating against the same free capacity.
func (q *Queries) beginAllocation(extra int64, manifest bool, expires *time.Time) (*sql.Tx, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE resource_policy SET id=id WHERE id=1`); err != nil {
		tx.Rollback()
		return nil, err
	}
	p, err := readPolicy(tx)
	if err == nil && expires != nil && expires.After(time.Now().Add(time.Duration(p.MaxRetentionSeconds)*time.Second)) {
		err = ErrRetentionLimit
	}
	if err == nil {
		err = q.checkCapacity(tx, p, extra, manifest)
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
func (q *Queries) allocationExec(extra int64, manifest bool, expires *time.Time, query string, args ...any) error {
	tx, err := q.beginAllocation(extra, manifest, expires)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(query, args...); err != nil {
		return ResourceError(err)
	}
	return tx.Commit()
}

func (q *Queries) ReleasePayloads(id string) error { return q.ReleaseCleanedPayloads(id) }
