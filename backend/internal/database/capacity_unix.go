//go:build !windows

package database

import "golang.org/x/sys/unix"

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
