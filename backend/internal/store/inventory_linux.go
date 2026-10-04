package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func inventoryIdentity(st unix.Stat_t) InventoryIdentity {
	return InventoryIdentity{Device: st.Dev, Inode: st.Ino, Mode: st.Mode, Size: st.Size,
		MTimeSec: st.Mtim.Sec, MTimeNsec: st.Mtim.Nsec, CTimeSec: st.Ctim.Sec, CTimeNsec: st.Ctim.Nsec}
}

func inventoryStat(file *os.File) (InventoryIdentity, error) {
	var st unix.Stat_t
	err := unix.Fstat(int(file.Fd()), &st)
	return inventoryIdentity(st), err
}

func inventorySameObject(a, b InventoryIdentity) bool {
	return a.Device == b.Device && a.Inode == b.Inode && a.Mode&unix.S_IFMT == b.Mode&unix.S_IFMT
}

func inventoryComponent(name string) bool {
	_, _, err := payloadParts(name + "/payload")
	return err == nil
}

// checkInventoryAssociation verifies that the configured pathname still denotes
// root, and that the transfer entry still denotes parent. Callers hold resource
// and database writer locks while deleting; outside filesystem writers cannot be
// synchronized by those locks and must not modify the store during recovery.
func (d *DiskStore) checkInventoryAssociation(root, parent *os.File, directory string) error {
	fresh, err := openBaseDirectory(d.basePath, false)
	if err != nil {
		return errors.Join(ErrInventoryChanged, err)
	}
	a, aErr := inventoryStat(root)
	b, bErr := inventoryStat(fresh)
	err = errors.Join(aErr, bErr, d.closeFile(fresh))
	if err != nil {
		return err
	}
	if !inventorySameObject(a, b) {
		return ErrInventoryChanged
	}
	if directory != "" {
		if err := sameDirectoryEntry(root, parent, directory); err != nil {
			return errors.Join(ErrInventoryChanged, err)
		}
	}
	return nil
}

// InventoryDirectory performs bounded enumeration using Linux getdents64
// cookies. A fixed 280-byte buffer can hold one maximum-sized Linux dirent;
// read-ahead is discarded and continuation always records the last processed
// record. At most maximum records (including dot entries) or EOF probes are
// processed, with at most maximum syscalls and stat probes. No directory-wide
// allocation, sorting, buffered os.ReadDir offset, or recursive walk is used.
func (d *DiskStore) InventoryDirectory(ctx context.Context, directory string, cursor InventoryCursor, maximum int) (page InventoryPage, err error) {
	page.Next = cursor
	page.Unstable = cursor.Unstable
	if maximum < 1 || maximum > 64 {
		return page, errors.New("invalid inventory budget")
	}
	if directory != "" && !inventoryComponent(directory) {
		return page, ErrInventoryUnsupported
	}
	if cursor.Cookie < 0 || (!cursor.Begun && cursor.Cookie != 0) {
		return page, ErrInventoryUnsupported
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	root, err := openBaseDirectory(d.basePath, false)
	if err != nil {
		return page, err
	}
	defer func() {
		err = errors.Join(err, d.closeFile(root))
		if err != nil {
			page.Done = false
		}
	}()
	parent := root
	if directory != "" {
		parent, err = openDirectoryAt(root, directory)
		if err != nil {
			return page, err
		}
		defer func() {
			err = errors.Join(err, d.closeFile(parent))
			if err != nil {
				page.Done = false
			}
		}()
	}
	rootID, err := inventoryStat(root)
	if err != nil {
		return page, err
	}
	parentID, err := inventoryStat(parent)
	if err != nil {
		return page, err
	}
	if cursor.Begun && (!inventorySameObject(cursor.Root, rootID) || !inventorySameObject(cursor.Directory, parentID)) {
		page.Next = InventoryCursor{}
		page.Unstable = true
		return page, ErrInventoryChanged
	}
	if cursor.Begun && (cursor.Root != rootID || cursor.Directory != parentID) {
		page.Unstable = true
	}
	page.Next = InventoryCursor{Root: rootID, Directory: parentID, Cookie: cursor.Cookie, Begun: true, Unstable: page.Unstable}
	if cursor.Cookie != 0 {
		offset, seekErr := unix.Seek(int(parent.Fd()), cursor.Cookie, 0)
		if seekErr != nil || offset != cursor.Cookie {
			return page, errors.Join(ErrInventoryUnsupported, seekErr)
		}
	}
	var buf [280]byte
	remaining := maximum
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		n, readErr := unix.Getdents(int(parent.Fd()), buf[:])
		if readErr != nil {
			return page, errors.Join(ErrInventoryUnsupported, readErr)
		}
		if n == 0 {
			page.Done = true
			break
		}
		for offset := 0; offset < n && remaining > 0; {
			if err := ctx.Err(); err != nil {
				return page, err
			}
			if n-offset < 20 {
				return page, ErrInventoryUnsupported
			}
			record := buf[offset:n]
			length := int(binary.NativeEndian.Uint16(record[16:18]))
			if length < 20 || length > len(record) {
				return page, ErrInventoryUnsupported
			}
			cookie := int64(binary.NativeEndian.Uint64(record[8:16]))
			if cookie < 0 || cookie == page.Next.Cookie {
				return page, ErrInventoryUnsupported
			}
			nameBytes := record[19:length]
			end := bytes.IndexByte(nameBytes, 0)
			if end < 0 {
				return page, ErrInventoryUnsupported
			}
			name := string(nameBytes[:end])
			page.Next.Cookie = cookie
			remaining--
			offset += length
			if name == "." || name == ".." || binary.NativeEndian.Uint64(record[:8]) == 0 {
				continue
			}
			var st unix.Stat_t
			statErr := unix.Fstatat(int(parent.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
			if errors.Is(statErr, unix.ENOENT) {
				page.Unstable = true
				continue
			}
			if statErr != nil {
				return page, statErr
			}
			kind := st.Mode & unix.S_IFMT
			unsupported := !inventoryComponent(name) || (directory == "" && kind != unix.S_IFDIR) || (directory != "" && kind != unix.S_IFREG)
			page.Entries = append(page.Entries, InventoryEntry{Directory: directory, Name: name, Root: rootID, Parent: parentID, File: inventoryIdentity(st), Unsupported: unsupported})
		}
	}
	if err := d.checkInventoryAssociation(root, parent, directory); err != nil {
		page.Next = InventoryCursor{}
		page.Unstable = true
		return page, err
	}
	rootAfter, rootErr := inventoryStat(root)
	parentAfter, parentErr := inventoryStat(parent)
	if rootAfter != rootID || parentAfter != parentID {
		page.Unstable = true
	}
	page.Next.Unstable = page.Unstable
	return page, errors.Join(rootErr, parentErr)
}

// RemoveOrphan removes only one exact regular payload or an empty root child
// directory. It never follows symlinks or removes siblings/trees. Reader,
// transfer and database membership/writer locking belong to the caller.
func (d *DiskStore) RemoveOrphan(ctx context.Context, expected InventoryEntry) (bool, error) {
	return d.removeOrphan(ctx, expected, nil)
}

func (d *DiskStore) removeOrphan(ctx context.Context, expected InventoryEntry, beforeCheck func()) (done bool, err error) {
	if expected.Unsupported || !inventoryComponent(expected.Name) || (expected.Directory != "" && !inventoryComponent(expected.Directory)) {
		return false, ErrInventoryUnsupported
	}
	kind := expected.File.Mode & unix.S_IFMT
	if (expected.Directory == "" && kind != unix.S_IFDIR) || (expected.Directory != "" && kind != unix.S_IFREG) {
		return false, ErrInventoryUnsupported
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	root, err := openBaseDirectory(d.basePath, false)
	if err != nil {
		return false, errors.Join(ErrInventoryChanged, err)
	}
	defer func() {
		err = errors.Join(err, d.closeFile(root))
		if err != nil {
			done = false
		}
	}()
	rootID, err := inventoryStat(root)
	if err != nil {
		return false, err
	}
	if !inventorySameObject(rootID, expected.Root) {
		return false, ErrInventoryChanged
	}
	parent := root
	if expected.Directory != "" {
		parent, err = openDirectoryAt(root, expected.Directory)
		if errors.Is(err, unix.ENOENT) {
			if err := d.checkInventoryAssociation(root, root, ""); err != nil {
				return false, err
			}
			err = d.syncFile(root)
			return err == nil, err
		}
		if err != nil {
			return false, errors.Join(ErrInventoryChanged, err)
		}
		defer func() {
			err = errors.Join(err, d.closeFile(parent))
			if err != nil {
				done = false
			}
		}()
	}
	parentID, err := inventoryStat(parent)
	if err != nil {
		return false, err
	}
	if !inventorySameObject(parentID, expected.Parent) {
		return false, ErrInventoryChanged
	}
	if beforeCheck != nil {
		beforeCheck()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := d.checkInventoryAssociation(root, parent, expected.Directory); err != nil {
		return false, err
	}
	var st unix.Stat_t
	err = unix.Fstatat(int(parent.Fd()), expected.Name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		err = d.syncFile(parent)
		return err == nil, err
	}
	if err != nil {
		return false, err
	}
	if inventoryIdentity(st) != expected.File {
		return false, ErrInventoryChanged
	}
	flags := 0
	if kind == unix.S_IFDIR {
		flags = unix.AT_REMOVEDIR
	}
	budget := 1
	return d.unlinkEntryAt(ctx, parent, expected.Name, flags, &budget)
}
