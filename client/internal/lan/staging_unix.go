//go:build linux || darwin

package lan

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func protectStaging(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	metadata, ok := info.Sys().(*syscall.Stat_t)
	if !ok || metadata.Uid != uint32(os.Geteuid()) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("LAN staging must be a private directory owned by the current user")
	}
	return nil
}
func lockStaging(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
func checkSpace(path string, needed int64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return err
	}
	if stat.Bsize <= 0 || uint64(stat.Bavail) < uint64((needed+int64(stat.Bsize)-1)/int64(stat.Bsize)) {
		return ErrInsufficientSpace
	}
	return nil
}
