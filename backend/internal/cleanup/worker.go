package cleanup

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/endorses/psst.zip/backend/internal/store"
)

// Run starts a background goroutine that periodically deletes expired
// transfers, slots, and their associated files. It blocks until ctx is cancelled.
func Run(ctx context.Context, db *sql.DB, fs store.FileStore, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := cleanExpired(db, fs); err != nil {
				log.Printf("cleanup error: %v", err)
			}
			if err := cleanDownloadLimited(db, fs); err != nil {
				log.Printf("cleanup (download limit) error: %v", err)
			}
		}
	}
}

func cleanExpired(db *sql.DB, fs store.FileStore) error {
	now := time.Now()

	// Find expired transfers.
	rows, err := db.Query("SELECT id FROM transfers WHERE expires_at <= ?", now)
	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	rows.Close()

	for _, id := range ids {
		if err := deleteTransfer(db, fs, id); err != nil {
			log.Printf("cleanup: failed to delete transfer %s: %v", id, err)
		}
	}

	// Delete expired slots.
	_, err = db.Exec("DELETE FROM slots WHERE expires_at <= ?", now)
	return err
}

func cleanDownloadLimited(db *sql.DB, fs store.FileStore) error {
	rows, err := db.Query(
		"SELECT id FROM transfers WHERE max_downloads > 0 AND download_count >= max_downloads",
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	rows.Close()

	for _, id := range ids {
		if err := deleteTransfer(db, fs, id); err != nil {
			log.Printf("cleanup: failed to delete download-limited transfer %s: %v", id, err)
		}
	}

	return nil
}

func deleteTransfer(db *sql.DB, fs store.FileStore, transferID string) error {
	// Delete files from storage.
	if err := fs.DeleteAll(transferID); err != nil {
		log.Printf("cleanup: failed to delete files for transfer %s: %v", transferID, err)
	}

	// Delete from database (cascades to files and manifests).
	_, err := db.Exec("DELETE FROM transfers WHERE id = ?", transferID)
	return err
}
