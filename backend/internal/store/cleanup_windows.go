package store

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// DeleteAllBounded uses no-follow, parent-relative Windows handles. Directory
// handles deny delete sharing while enumerated, so their names cannot be moved
// outside the selected resource tree during recursive deletion.
func (d *DiskStore) DeleteAllBounded(ctx context.Context, prefix string, maximum int) (bool, error) {
	return d.deleteAllBounded(ctx, prefix, maximum, nil)
}

func (d *DiskStore) deleteAllBounded(ctx context.Context, prefix string, maximum int, beforeOpen func(*os.File, string)) (done bool, err error) {
	if maximum < 1 || maximum > 1024 {
		return false, errors.New("invalid deletion budget")
	}
	if _, _, err := payloadParts(prefix + "/payload"); err != nil {
		return false, errors.New("invalid cleanup resource")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	root, err := openBaseDirectory(d.basePath, false)
	if err != nil {
		return false, err
	}
	defer func() {
		err = errors.Join(err, d.closeFile(root))
		if err != nil {
			done = false
		}
	}()
	return d.deleteWindowsEntry(ctx, root, prefix, &maximum, 0, beforeOpen)
}

func (d *DiskStore) deleteWindowsEntry(ctx context.Context, parent *os.File, name string, budget *int, depth int, beforeOpen func(*os.File, string)) (done bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if *budget <= 0 {
		return false, nil
	}
	if depth > 32 {
		return false, errors.New("storage tree is too deep")
	}
	if beforeOpen != nil {
		beforeOpen(parent, name)
	}
	entry, err := ntOpenAtSharing(parent, name, windows.DELETE|windows.FILE_GENERIC_READ, 0, windows.FILE_OPEN, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if errors.Is(err, os.ErrNotExist) {
		return true, d.syncFile(parent)
	}
	if err != nil {
		return false, err
	}
	defer func() {
		err = errors.Join(err, d.closeFile(entry))
		if err != nil {
			done = false
		}
	}()
	info, err := storageAttributes(entry)
	if err != nil {
		return false, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		if *budget < 2 {
			return false, nil
		}
		*budget--
		entries, readErr := entry.ReadDir(min(max(1, *budget/2), 64))
		*budget -= len(entries)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return false, readErr
		}
		for _, child := range entries {
			removed, err := d.deleteWindowsEntry(ctx, entry, child.Name(), budget, depth+1, beforeOpen)
			if err != nil || !removed {
				return false, err
			}
		}
	}
	if *budget <= 0 {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	*budget--
	// FILE_DISPOSITION_INFO marks this exact opened object, without a second path
	// traversal. Nonempty directories remain pending for the next bounded pass.
	disposition := byte(1)
	err = windows.SetFileInformationByHandle(windows.Handle(entry.Fd()), windows.FileDispositionInfo, &disposition, 1)
	if errors.Is(err, windows.ERROR_DIR_NOT_EMPTY) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, d.syncFile(parent)
}
