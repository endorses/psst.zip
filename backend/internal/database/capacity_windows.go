package database

import (
	"golang.org/x/sys/windows"
	"hash/fnv"
	"path/filepath"
	"strings"
)

func diskCapacity(path string) (volumeCapacity, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return volumeCapacity{}, err
	}
	name, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return volumeCapacity{}, err
	}
	var available, total, free uint64
	if err = windows.GetDiskFreeSpaceEx(name, &available, &total, &free); err != nil {
		return volumeCapacity{}, err
	}
	// Group payload/database paths on the same volume before reserving capacity.
	volume := fnv.New64a()
	_, _ = volume.Write([]byte(strings.ToLower(filepath.VolumeName(absolute))))
	bounded := func(n uint64) int64 {
		if n > 1<<63-1 {
			return 1<<63 - 1
		}
		return int64(n)
	}
	return volumeCapacity{device: volume.Sum64(), total: bounded(total), available: bounded(available)}, nil
}
