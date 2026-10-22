//go:build windows

package state

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Apply an explicit, non-inherited DACL before creating or opening any vault
// contents. Unix mode bits alone do not protect files on Windows.
func protectWindows(path string) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return errors.New("cannot identify private-state owner")
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return errors.New("cannot construct private-state ACL")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return errors.New("cannot construct private-state ACL")
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return errors.New("cannot protect private-state ACL")
	}
	return nil
}

func protectDirectory(path string) error { return protectWindows(path) }
func protectFile(path string) error      { return protectWindows(path) }

func tryLockFile(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}

func syncDirectory(string) error {
	// Go's Windows directory handles do not support File.Sync. The new encrypted
	// file is flushed before replacement; platform power-loss durability needs a
	// native Windows fixture in addition to the cross-compiled implementation.
	return nil
}
