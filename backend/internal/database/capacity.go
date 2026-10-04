package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

type volumeCapacity struct {
	device           uint64
	total, available int64
}
type capacityPath struct {
	path     string
	payload  bool
	database bool
}
type capacityConfig struct {
	sync.RWMutex
	paths []capacityPath
	probe func(string) (volumeCapacity, error)
}

func diskCapacity(path string) (volumeCapacity, error) {
	var info unix.Statfs_t
	if err := unix.Statfs(path, &info); err != nil {
		return volumeCapacity{}, err
	}
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return volumeCapacity{}, err
	}
	bytes := func(blocks uint64) int64 {
		if info.Bsize <= 0 || blocks > uint64(1<<63-1)/uint64(info.Bsize) {
			return 1<<63 - 1
		}
		return int64(blocks) * int64(info.Bsize)
	}
	return volumeCapacity{uint64(stat.Dev), bytes(info.Blocks), bytes(info.Bavail)}, nil
}
func (q *Queries) SetCapacityPaths(dataPath, dbPath string) {
	q.capacity.Lock()
	defer q.capacity.Unlock()
	q.capacity.paths = nil
	if dataPath != "" {
		q.capacity.paths = append(q.capacity.paths, capacityPath{path: dataPath, payload: true})
	}
	if dbPath != "" {
		q.capacity.paths = append(q.capacity.paths, capacityPath{path: filepath.Dir(dbPath), database: true})
	}
	q.capacity.probe = diskCapacity
}
func (q *Queries) checkCapacity(tx *sql.Tx, p ResourcePolicy, extra int64, manifest bool) error {
	var outstanding int64
	if err := tx.QueryRow(`SELECT COALESCE(SUM(MAX(0,size-upload_offset)),0) FROM files WHERE payload_deleted=0`).Scan(&outstanding); err != nil {
		return err
	}
	return q.checkVolumes(p, extra, manifest, outstanding)
}
func (q *Queries) checkVolumes(p ResourcePolicy, extra int64, manifest bool, outstanding int64) error {
	q.capacity.RLock()
	paths := append([]capacityPath(nil), q.capacity.paths...)
	probe := q.capacity.probe
	q.capacity.RUnlock()
	type checkedVolume struct {
		capacity          volumeCapacity
		payload, database bool
	}
	volumes := map[uint64]checkedVolume{}
	for _, path := range paths {
		capacity, err := probe(path.path)
		if err != nil {
			return fmt.Errorf("%w: capacity unavailable", ErrDiskCapacity)
		}
		volume := volumes[capacity.device]
		volume.capacity = capacity
		volume.payload = volume.payload || path.payload
		volume.database = volume.database || path.database
		volumes[capacity.device] = volume
	}
	for _, volume := range volumes {
		reserve := max(p.ReserveDiskBytes, volume.capacity.total/100*p.ReserveDiskPercent)
		required := int64(1024 * 1024) // Leave room for a metadata write and WAL pages.
		if volume.payload {
			required += outstanding
			if !manifest {
				required += extra
			}
		}
		if volume.database && manifest {
			required += extra * 2
		} // Temporary replacement/WAL pages.
		if required < 0 || volume.capacity.available < reserve || required > volume.capacity.available-reserve {
			return ErrDiskCapacity
		}
	}
	return nil
}

// Streaming rechecks real free capacity without double-counting bytes being
// written by concurrent requests whose offsets have not yet been committed.
func (q *Queries) CheckWriteCapacity() error {
	p, err := q.ResourcePolicy()
	if err != nil {
		return err
	}
	return q.checkVolumes(p, 0, false, 0)
}
