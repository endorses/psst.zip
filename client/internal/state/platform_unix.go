//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package state

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func protectPath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("cannot inspect private-state permissions")
	}
	metadata, ok := info.Sys().(*syscall.Stat_t)
	if !ok || metadata.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory {
		return errors.New("private state must be owned by the current user with no group or other access")
	}
	return nil
}

func protectDirectory(path string) error { return protectPath(path, true) }
func protectFile(path string) error      { return protectPath(path, false) }

func tryLockFile(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
