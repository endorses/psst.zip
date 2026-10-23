package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ntOpenAt opens a single component relative to an already opened directory.
// FILE_OPEN_REPARSE_POINT prevents junction/symlink resolution; callers inspect
// attributes before using the handle. Ancestors are never re-resolved by name.
func ntOpenAt(parent *os.File, name string, access, options, disposition uint32) (*os.File, error) {
	return ntOpenAtSharing(parent, name, access, options, disposition, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE)
}

func ntOpenAtSharing(parent *os.File, name string, access, options, disposition, sharing uint32) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `\/:`) {
		return nil, errors.New("invalid storage component")
	}
	unicodeName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(parent.Fd()), ObjectName: unicodeName, Attributes: windows.OBJ_CASE_INSENSITIVE}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &attributes, &windows.IO_STATUS_BLOCK{}, nil, windows.FILE_ATTRIBUTE_NORMAL, sharing, disposition, options|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		var status windows.NTStatus
		if errors.As(err, &status) {
			err = status.Errno()
		}
		return nil, &os.PathError{Op: "open storage", Path: name, Err: err}
	}
	return os.NewFile(uintptr(handle), filepath.Join(parent.Name(), name)), nil
}

func storageAttributes(file *os.File) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info)
	return info, err
}

func checkedWindowsDirectory(file *os.File, err error) (*os.File, error) {
	if err != nil {
		return nil, err
	}
	info, err := storageAttributes(file)
	if err == nil && (info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0) {
		err = errors.New("storage directory must not be a reparse point or special file")
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func openDirectoryAt(parent *os.File, name string) (*os.File, error) {
	return checkedWindowsDirectory(ntOpenAt(parent, name, windows.FILE_GENERIC_READ, windows.FILE_DIRECTORY_FILE, windows.FILE_OPEN))
}

func createWindowsDirectoryAt(parent *os.File, name string) (*os.File, error) {
	return checkedWindowsDirectory(ntOpenAt(parent, name, windows.FILE_GENERIC_READ, windows.FILE_DIRECTORY_FILE, windows.FILE_OPEN_IF))
}

func openBaseDirectory(path string, create bool) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(absolute)
	rootName := volume + string(os.PathSeparator)
	encoded, err := windows.UTF16PtrFromString(rootName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(encoded, windows.FILE_GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	current, err := checkedWindowsDirectory(os.NewFile(uintptr(handle), rootName), nil)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.FieldsFunc(strings.TrimPrefix(absolute, volume), func(c rune) bool { return c == '\\' || c == '/' }) {
		var next *os.File
		if create {
			next, err = createWindowsDirectoryAt(current, part)
		} else {
			next, err = openDirectoryAt(current, part)
		}
		err = errors.Join(err, current.Close())
		if err != nil {
			if next != nil {
				err = errors.Join(err, next.Close())
			}
			return nil, err
		}
		current = next
	}
	return current, nil
}

func (d *DiskStore) openPayload(key string, flags int, create bool) (file, parent, root *os.File, err error) {
	transfer, name, err := payloadParts(key)
	if err != nil {
		return nil, nil, nil, err
	}
	root, err = openBaseDirectory(d.basePath, false)
	if err != nil {
		return nil, nil, nil, err
	}
	if create {
		parent, err = createWindowsDirectoryAt(root, transfer)
	} else {
		parent, err = openDirectoryAt(root, transfer)
	}
	if err != nil {
		return nil, nil, nil, errors.Join(err, root.Close())
	}
	access := uint32(windows.FILE_GENERIC_READ)
	if flags&os.O_WRONLY != 0 || flags&os.O_RDWR != 0 {
		access = windows.FILE_GENERIC_WRITE | windows.FILE_READ_ATTRIBUTES
	}
	disposition := uint32(windows.FILE_OPEN)
	if create {
		disposition = windows.FILE_OPEN_IF
	}
	file, err = ntOpenAt(parent, name, access, windows.FILE_NON_DIRECTORY_FILE, disposition)
	if err == nil {
		var attrs windows.ByHandleFileInformation
		attrs, err = storageAttributes(file)
		if err == nil && attrs.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
			err = fmt.Errorf("payload is not a regular file")
		}
	}
	if err != nil {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		return nil, nil, nil, errors.Join(err, parent.Close(), root.Close())
	}
	return file, parent, root, nil
}
