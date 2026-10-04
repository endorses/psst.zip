package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// DiskStore implements FileStore using the local filesystem.
type DiskStore struct {
	basePath string
	// Per-instance fault seam: nil uses the OS. Tests can inject durability
	// failures without replacing path resolution or the actual write itself.
	ioHooks *diskIOHooks
}

type diskIOHooks struct {
	sync  func(*os.File) error
	close func(*os.File) error
}

// NewDiskStore creates a DiskStore rooted at basePath. The directory is created
// if it does not exist.
func NewDiskStore(basePath string) (*DiskStore, error) {
	absolute, err := filepath.Abs(basePath)
	if err != nil {
		return nil, err
	}
	root, err := openBaseDirectory(absolute, true)
	if err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}
	if err := root.Close(); err != nil {
		return nil, err
	}
	return &DiskStore{basePath: absolute}, nil
}

func (d *DiskStore) path(key string) string {
	return filepath.Join(d.basePath, filepath.FromSlash(key))
}

func (d *DiskStore) Save(key string, r io.Reader) error {
	f, parent, root, err := d.openPayload(key, unix.O_WRONLY, true)
	if err != nil {
		return err
	}
	// Truncate only after verifying that the opened object is a regular file.
	err = f.Truncate(0)
	if err == nil {
		_, err = io.Copy(f, r)
	}
	return errors.Join(err, d.finishWrite(f, parent, root))
}

func (d *DiskStore) SaveAt(key string, r io.Reader, offset int64) (int64, error) {
	if offset < 0 {
		return 0, errors.New("negative payload offset")
	}
	f, parent, root, err := d.openPayload(key, unix.O_WRONLY, true)
	if err != nil {
		return 0, err
	}
	var n int64
	if _, err = f.Seek(offset, io.SeekStart); err == nil {
		n, err = io.Copy(f, r)
	}
	if durableErr := d.finishWrite(f, parent, root); durableErr != nil {
		return 0, errors.Join(err, durableErr)
	}
	return n, err
}

func (d *DiskStore) Load(key string) (io.ReadCloser, error) {
	f, parent, root, err := d.openPayload(key, unix.O_RDONLY, false)
	if err != nil {
		return nil, err
	}
	if err := errors.Join(parent.Close(), root.Close()); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

func (d *DiskStore) Inspect(key string) (PayloadInfo, error) {
	f, parent, root, err := d.openPayload(key, unix.O_RDONLY, false)
	if errors.Is(err, os.ErrNotExist) {
		return PayloadInfo{}, nil
	}
	if err != nil {
		return PayloadInfo{}, err
	}
	info, statErr := f.Stat()
	if err := errors.Join(statErr, f.Close(), parent.Close(), root.Close()); err != nil {
		return PayloadInfo{}, err
	}
	return PayloadInfo{Exists: true, Size: info.Size()}, nil
}

func (d *DiskStore) Size(key string) (int64, error) {
	info, err := d.Inspect(key)
	return info.Size, err
}

func (d *DiskStore) Delete(key string) error {
	err := os.Remove(d.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (d *DiskStore) DeleteAll(prefix string) error {
	root := d.path(prefix)

	// If it's a directory, remove the whole tree.
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.RemoveAll(root)
	}

	// Otherwise, scan for files matching the prefix.
	dir := filepath.Dir(root)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	base := filepath.Base(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), base) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (d *DiskStore) Truncate(key string, size int64) error {
	if size < 0 {
		return errors.New("negative payload size")
	}
	f, parent, root, err := d.openPayload(key, unix.O_WRONLY, false)
	if err != nil {
		return err
	}
	return errors.Join(f.Truncate(size), d.finishWrite(f, parent, root))
}

func (d *DiskStore) syncFile(file *os.File) error {
	if d.ioHooks != nil && d.ioHooks.sync != nil {
		return d.ioHooks.sync(file)
	}
	return file.Sync()
}
func (d *DiskStore) closeFile(file *os.File) error {
	if d.ioHooks != nil && d.ioHooks.close != nil {
		return d.ioHooks.close(file)
	}
	return file.Close()
}

// A successful write publishes file contents first, then its directory entry,
// then the transfer directory's entry in the store. Re-syncing both directories
// also covers retrying an earlier operation that failed before publication.
func (d *DiskStore) finishWrite(file, parent, root *os.File) error {
	fileErr := errors.Join(d.syncFile(file), d.closeFile(file))
	parentErr := errors.Join(d.syncFile(parent), d.closeFile(parent))
	rootErr := errors.Join(d.syncFile(root), d.closeFile(root))
	return errors.Join(fileErr, parentErr, rootErr)
}
