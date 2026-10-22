//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package state

import (
	"errors"
	"os"
)

func protectDirectory(string) error {
	return errors.New("protected state is unsupported on this operating system")
}
func protectFile(string) error {
	return errors.New("protected state is unsupported on this operating system")
}
func tryLockFile(*os.File) (bool, error) {
	return false, errors.New("protected state locking is unsupported on this operating system")
}
func unlockFile(*os.File) error  { return nil }
func syncDirectory(string) error { return nil }
