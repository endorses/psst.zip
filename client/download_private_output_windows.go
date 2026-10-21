//go:build windows

package client

import (
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createPrivateDownload installs the protected current-user-only DACL in the
// create syscall itself. Chmod and post-creation ACL changes leave a window in
// which a handle opened through an inherited public DACL could read plaintext.
// The validated single-component name and RootDirectory handle preserve Root's
// containment: root.Name is never used to open a security-relevant pathname.
func createPrivateDownload(root *os.Root, name string) (*os.File, error) {
	fail := func(err error) (*os.File, error) {
		return nil, &os.PathError{Op: "create private download", Path: name, Err: err}
	}
	if !validTemporaryName(name) {
		return fail(os.ErrInvalid)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fail(err)
	}
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;" + sid + ")")
	if err != nil {
		return fail(err)
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return fail(err)
	}
	directory, err := root.Open(".")
	if err != nil {
		return fail(err)
	}
	defer directory.Close()
	attributes := &windows.OBJECT_ATTRIBUTES{
		RootDirectory:      windows.Handle(directory.Fd()),
		ObjectName:         objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: descriptor,
	}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	var handle windows.Handle
	err = windows.NtCreateFile(
		&handle, windows.FILE_GENERIC_WRITE, attributes, &windows.IO_STATUS_BLOCK{},
		nil, windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0, 0,
	)
	runtime.KeepAlive(directory)
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(objectName)
	if err != nil {
		// Native statuses must become Win32 errno values so callers retain the
		// normal os.ErrExist/error classification for exclusive-create races.
		var status windows.NTStatus
		if errors.As(err, &status) {
			err = status.Errno()
		}
		return fail(err)
	}
	// An ACL-less filesystem must fail closed before a caller can write even
	// one authenticated plaintext frame. This only verifies creation-time
	// protection; it never repairs a public file after plaintext is present.
	actual, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err == nil && !privateDownloadDACL(actual, user.User.Sid) {
		err = errors.New("filesystem did not apply a private download ACL")
	}
	if err != nil {
		windows.CloseHandle(handle)
		_ = root.Remove(name)
		return fail(err)
	}
	file := os.NewFile(uintptr(handle), name)
	if file == nil {
		windows.CloseHandle(handle)
		_ = root.Remove(name)
		return fail(os.ErrInvalid)
	}
	return file, nil
}

func privateDownloadDACL(descriptor *windows.SECURITY_DESCRIPTOR, sid *windows.SID) bool {
	if descriptor == nil {
		return false
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 || control&windows.SE_DACL_PRESENT == 0 {
		return false
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(sid) {
		return false
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		return false
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if windows.GetAce(dacl, 0, &ace) != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
		return false
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	return aceSID.Equals(sid) && uint32(ace.Mask)&windows.FILE_GENERIC_WRITE == windows.FILE_GENERIC_WRITE
}
