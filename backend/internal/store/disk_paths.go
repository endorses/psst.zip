package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Payloads always have exactly two opaque components: transfer/file. Restrict
// names before any filesystem call; never clean a hostile key into a valid one.
func payloadParts(key string) (string, string, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 2 {
		return "", "", errors.New("invalid payload key")
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 255 || part == "." || part == ".." {
			return "", "", errors.New("invalid payload key")
		}
		for _, c := range part {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
				return "", "", errors.New("invalid payload key")
			}
		}
	}
	return parts[0], parts[1], nil
}

func openDirectoryAt(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open directory", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

// Go 1.23 has no os.Root. Descriptor-relative opens with O_NOFOLLOW protect
// every directory component, including the configured storage root. No
// check-then-open path walk or background goroutine is involved.
func openBaseDirectory(path string, create bool) (*os.File, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), "/")
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := openDirectoryAt(current, part)
		created := false
		if create && errors.Is(err, os.ErrNotExist) {
			err = unix.Mkdirat(int(current.Fd()), part, 0o755)
			if err == nil || errors.Is(err, unix.EEXIST) {
				created = err == nil
				next, err = openDirectoryAt(current, part)
			}
		}
		if err != nil {
			return nil, errors.Join(err, current.Close())
		}
		if created {
			// Persist the new directory before publishing its entry in parent.
			err = errors.Join(next.Sync(), current.Sync())
		}
		err = errors.Join(err, current.Close())
		if err != nil {
			return nil, errors.Join(err, next.Close())
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
	parent, err = openDirectoryAt(root, transfer)
	if create && errors.Is(err, os.ErrNotExist) {
		err = unix.Mkdirat(int(root.Fd()), transfer, 0o755)
		if err == nil || errors.Is(err, unix.EEXIST) {
			parent, err = openDirectoryAt(root, transfer)
		}
	}
	if err != nil {
		return nil, nil, nil, errors.Join(err, root.Close())
	}
	if create {
		flags |= unix.O_CREAT
	}
	// NONBLOCK prevents special files such as FIFOs from blocking before the
	// regular-file check. It has no effect on regular disk payloads.
	fd, err := unix.Openat(int(parent.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0o644)
	if err != nil {
		return nil, nil, nil, errors.Join(&os.PathError{Op: "open payload", Path: key, Err: err}, parent.Close(), root.Close())
	}
	file = os.NewFile(uintptr(fd), filepath.Join(d.basePath, key))
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("payload is not a regular file")
	}
	if err != nil {
		return nil, nil, nil, errors.Join(err, file.Close(), parent.Close(), root.Close())
	}
	return file, parent, root, nil
}
