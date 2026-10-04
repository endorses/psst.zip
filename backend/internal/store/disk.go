package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DiskStore implements FileStore using the local filesystem.
type DiskStore struct {
	basePath string
}

// NewDiskStore creates a DiskStore rooted at basePath. The directory is created
// if it does not exist.
func NewDiskStore(basePath string) (*DiskStore, error) {
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}
	return &DiskStore{basePath: basePath}, nil
}

func (d *DiskStore) path(key string) string {
	return filepath.Join(d.basePath, filepath.FromSlash(key))
}

func (d *DiskStore) Save(key string, r io.Reader) error {
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

func (d *DiskStore) SaveAt(key string, r io.Reader, offset int64) (int64, error) {
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}

	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	return n, err
}

func (d *DiskStore) Load(key string) (io.ReadCloser, error) {
	return os.Open(d.path(key))
}

func (d *DiskStore) Size(key string) (int64, error) {
	info, err := os.Stat(d.path(key))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
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

func (d *DiskStore) Truncate(key string, size int64) error { return os.Truncate(d.path(key), size) }
