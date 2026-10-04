package store

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// BoundedDeleter supports incremental deletion while holding a resource lock.
// done=false keeps quota reservations charged and resumes on the next attempt.
// Legacy stores retain their DeleteAll contract; callers must not simulate
// cancellation by leaking a new goroutine for every blocked storage operation.
type BoundedDeleter interface {
	DeleteAllBounded(context.Context, string, int) (done bool, err error)
}

var errCleanupEntryChanged = errors.New("storage entry changed during cleanup")

// DeleteAllBounded removes the exact resource directory (or file), never a
// sibling with a matching prefix. Descriptor-relative traversal never follows a
// symlink. Enumeration and unlink work share a finite per-call budget; successful
// removals sync their parent directory before they are acknowledged. Synchronous
// filesystem calls cannot be forcibly interrupted by a Go context.
func (d *DiskStore) DeleteAllBounded(ctx context.Context, prefix string, maximum int) (bool, error) {
	return d.deleteAllBounded(ctx, prefix, maximum, nil)
}

// beforeOpen is a per-call test seam for deterministic directory-replacement
// races; production always supplies nil and uses the same no-follow resolver.
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
	return d.deleteEntryAt(ctx, root, prefix, &maximum, 0, beforeOpen)
}

func sameDirectoryEntry(parent, directory *os.File, name string) error {
	var entry, opened unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &entry, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return errors.Join(errCleanupEntryChanged, err)
	}
	if err := unix.Fstat(int(directory.Fd()), &opened); err != nil {
		return err
	}
	if entry.Mode&unix.S_IFMT != unix.S_IFDIR || entry.Dev != opened.Dev || entry.Ino != opened.Ino {
		return errCleanupEntryChanged
	}
	return nil
}

func (d *DiskStore) unlinkEntryAt(ctx context.Context, parent *os.File, name string, flags int, budget *int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if *budget <= 0 {
		return false, nil
	}
	*budget--
	err := unix.Unlinkat(int(parent.Fd()), name, flags)
	if err == nil || errors.Is(err, unix.ENOENT) {
		// Also sync a now-missing entry: an earlier attempt may have unlinked
		// it but failed to persist the parent directory before returning.
		err = d.syncFile(parent)
		return err == nil, err
	}
	if errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EEXIST) {
		return false, nil
	}
	return false, err
}

func (d *DiskStore) deleteEntryAt(ctx context.Context, parent *os.File, name string, budget *int, depth int, beforeOpen func(*os.File, string)) (done bool, err error) {
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
	directory, err := openDirectoryAt(parent, name)
	if errors.Is(err, unix.ENOENT) {
		return true, d.syncFile(parent)
	}
	if errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
		// Unlink never follows the final component. A replacement directory
		// makes this fail safely instead of changing traversal strategy.
		return d.unlinkEntryAt(ctx, parent, name, 0, budget)
	}
	if err != nil {
		return false, err
	}
	defer func() {
		err = errors.Join(err, d.closeFile(directory))
		if err != nil {
			done = false
		}
	}()
	if err := sameDirectoryEntry(parent, directory, name); err != nil {
		return false, err
	}
	if *budget < 2 {
		return false, nil
	}

	// Charge the probe even for an empty directory, and charge all returned
	// entries now rather than only those that fit later deletion work.
	*budget--
	entries, readErr := directory.ReadDir(min(max(1, *budget/2), 64))
	*budget -= len(entries)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, readErr
	}
	for _, entry := range entries {
		if *budget <= 0 {
			return false, nil
		}
		if err := sameDirectoryEntry(parent, directory, name); err != nil {
			return false, err
		}
		done, err := d.deleteEntryAt(ctx, directory, entry.Name(), budget, depth+1, beforeOpen)
		if err != nil || !done {
			return false, err
		}
	}
	if *budget <= 0 {
		return false, nil
	}
	if err := sameDirectoryEntry(parent, directory, name); err != nil {
		return false, err
	}
	// An atomic rmdir checks emptiness without another pathname traversal.
	// Concurrent additions keep this directory pending. A swapped symlink is
	// never followed, even between the identity check and unlinkat.
	return d.unlinkEntryAt(ctx, parent, name, unix.AT_REMOVEDIR, budget)
}
