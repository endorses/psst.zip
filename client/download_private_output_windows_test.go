//go:build windows

package client

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Run natively on an ACL-capable Windows volume. Cross-compilation checks the
// adapter API but cannot establish inheritance, access, or handle behavior.
func TestPrivateDownloadWindowsOwnerOnlyACL(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// Model a shared download directory whose ordinary children inherit an
	// Everyone read grant. This fixture contains no private file yet.
	parent, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;GR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	parentDACL, _, err := parent.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, parentDACL, nil); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	name := ".psst-12345678-1234-4234-8234-123456789abc.partial"
	file, err := createPrivateDownload(root, name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// Inspect the native file object before any plaintext is written. There is
	// no chmod/ACL repair step between this assertion and the create syscall.
	security, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := security.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 || control&windows.SE_DACL_PRESENT == 0 {
		t.Fatalf("file DACL is not protected at creation: control%#x err%v", control, err)
	}
	owner, _, err := security.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatal("file owner is not the current user", err)
	}
	dacl, _, err := security.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		t.Fatal("shared parent ACL was inherited or file DACL is missing", err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err = windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 || !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(user.User.Sid) {
		t.Fatal("file grants access beyond one explicit current-user ACE")
	}
	if uint32(ace.Mask)&(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE) != windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE {
		t.Fatal("current user cannot read and save the private output")
	}
	plaintext := []byte("authenticated but not yet published\n")
	if _, err = file.Write(plaintext); err != nil {
		t.Fatal(err)
	}
	if err = file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	collision, err := createPrivateDownload(root, name)
	if collision != nil {
		collision.Close()
	}
	var pathError *os.PathError
	if !errors.Is(err, os.ErrExist) || !errors.As(err, &pathError) {
		t.Fatalf("exclusive collision was not a filesystem existence error: %v", err)
	}
	got, err := root.ReadFile(name)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatal("exclusive creation replaced the existing output", err)
	}
	for _, invalid := range []string{"../escape", `C:\escape`, "ordinary.txt", ".psst-../outside.partial"} {
		if f, err := createPrivateDownload(root, invalid); err == nil {
			f.Close()
			t.Fatal("accepted a non-leaf or non-generated download name")
		} else if !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("invalid leaf did not preserve filesystem classification: %v", err)
		}
	}
}
