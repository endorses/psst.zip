//go:build linux

package deployment

import (
	"fmt"
	"os"
	"syscall"
)

func ownedByRoot(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == 0
}
func fileUID(info os.FileInfo) uint32 {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ^uint32(0)
	}
	return s.Uid
}
func freeBytes(path string) (uint64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, err
	}
	return s.Bavail * uint64(s.Bsize), nil
}
func lock(root string) (func(), error) {
	f, err := os.OpenFile(root+"/deployment.lock", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !ownedByRoot(info) || info.Mode().Perm()&0o077 != 0 {
		f.Close()
		return nil, fmt.Errorf("unsafe deployment lock")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another deployment owns the host lock")
	}
	return func() { _ = f.Close() }, nil
}
func setPrivateUmask() error { syscall.Umask(0o077); return nil }
