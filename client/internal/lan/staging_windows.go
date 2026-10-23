//go:build windows

package lan

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func protectStaging(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("LAN staging is not a private directory")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	// Newly created staging contains no files yet. Restrict inheritance before any
	// ciphertext or database is created; session keys are never written here.
	descriptor, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	existing, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := existing.Owner()
	if err != nil || !owner.Equals(user.User.Sid) {
		return errors.New("LAN staging belongs to another user")
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
func lockStaging(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}
func checkSpace(path string, needed int64) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var available, total, free uint64
	if err = windows.GetDiskFreeSpaceEx(pointer, &available, &total, &free); err != nil {
		return err
	}
	if available < uint64(needed) {
		return ErrInsufficientSpace
	}
	return nil
}
