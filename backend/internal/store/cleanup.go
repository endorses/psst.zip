package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// BoundedDeleter supports incremental deletion while holding a resource lock.
// done=false keeps quota reservations charged and resumes on the next attempt.
// Legacy stores retain their DeleteAll contract; callers must not simulate
// cancellation by leaking a new goroutine for every blocked storage operation.
type BoundedDeleter interface {
	DeleteAllBounded(context.Context, string, int) (done bool, err error)
}

// DeleteAllBounded removes the exact resource directory (or file), never a
// sibling with a matching prefix. Names inside storage are opaque identifiers.
// Directory enumeration and unlink work share a finite per-call entry budget;
// symlinks are unlinked, never traversed. OS filesystem calls themselves cannot
// be forcibly interrupted by a Go context.
func (d *DiskStore) DeleteAllBounded(ctx context.Context, prefix string, maximum int) (bool, error) {
	if maximum < 1 || maximum > 1024 {
		return false, errors.New("invalid deletion budget")
	}
	root := d.path(prefix)
	relative, err := filepath.Rel(d.basePath, root)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || filepath.Dir(relative) != "." {
		return false, errors.New("invalid cleanup resource")
	}
	return deleteBoundedDirectory(ctx, root, &maximum, 0)
}
func deleteBoundedDirectory(ctx context.Context, path string, budget *int, depth int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if *budget <= 0 {
		return false, nil
	}
	if depth > 32 {
		return false, errors.New("storage tree is too deep")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		*budget--
		err = os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		return err == nil, err
	}
	directory, err := os.Open(path)
	if err != nil {
		return false, err
	}
	entries, readErr := directory.ReadDir(min(*budget, 64))
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, readErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if len(entries) == 0 {
		*budget--
		err = os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		return err == nil, err
	}
	for _, entry := range entries {
		if *budget <= 0 {
			return false, nil
		}
		*budget-- // Enumeration is bounded even for a hostile nested directory tree.
		done, err := deleteBoundedDirectory(ctx, filepath.Join(path, entry.Name()), budget, depth+1)
		if err != nil {
			return false, err
		}
		if !done {
			return false, nil
		}
	}
	if *budget <= 0 {
		return false, nil
	}
	*budget--
	check, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	remaining, readErr := check.ReadDir(1)
	closeErr = check.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, readErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if len(remaining) != 0 || *budget <= 0 {
		return false, nil
	}
	*budget--
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return err == nil, err
}
