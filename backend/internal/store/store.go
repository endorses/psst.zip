package store

import "io"

// PayloadInfo distinguishes a missing payload from an existing empty file.
type PayloadInfo struct {
	Exists bool
	Size   int64
}

// FileStore abstracts file storage operations. Implementations may store
// files on local disk, S3, or any other backend.
type FileStore interface {
	// Save writes data from r into the store under the given key.
	Save(key string, r io.Reader) error

	// SaveAt writes data from r starting at the given byte offset. A positive
	// count proves those bytes are durable, including when r returns an error.
	// Durability failures return zero; callers must not acknowledge those bytes.
	SaveAt(key string, r io.Reader, offset int64) (int64, error)

	// Truncate discards bytes written by a rejected upload request.
	Truncate(key string, size int64) error

	// Load returns a ReadCloser for the file identified by key.
	Load(key string) (io.ReadCloser, error)

	// Inspect returns metadata only for a regular payload without following
	// symlinks. Missing payloads return Exists=false without an error.
	Inspect(key string) (PayloadInfo, error)

	// Size returns the current size of the stored file, or 0 if it does not exist.
	Size(key string) (int64, error)

	// Delete removes the file identified by key.
	Delete(key string) error

	// DeleteAll removes all files whose keys start with the given prefix.
	DeleteAll(prefix string) error
}
