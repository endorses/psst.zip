package cleanup

import (
	"errors"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

// RemoveTransfer serializes with upload mutations, revokes the link durably,
// then removes payloads and metadata. Disk errors retain the token and revoked
// row so authenticated retries and the cleanup worker can finish the deletion.
func RemoveTransfer(q *database.Queries, files store.FileStore, id string) error {
	defer store.LockTransfer(id)()
	if err := q.RevokeTransfer(id); err != nil {
		return err
	}
	if err := files.DeleteAll(id); err != nil {
		return err
	}
	return q.DeleteTransfer(id)
}

// RemoveSlot prevents new children, revokes the existing children atomically,
// then waits for each active upload before deleting its payload. Never acquire
// two transfer locks together: striped IDs can share the same underlying lock.
func RemoveSlot(q *database.Queries, files store.FileStore, id string) error {
	defer store.LockSlot(id)()
	children, err := q.RevokeSlot(id)
	if err != nil {
		return err
	}
	var cleanupError error
	for _, child := range children {
		cleanupError = errors.Join(cleanupError, RemoveTransfer(q, files, child))
	}
	if cleanupError != nil {
		return cleanupError
	}
	return q.DeleteSlot(id)
}
