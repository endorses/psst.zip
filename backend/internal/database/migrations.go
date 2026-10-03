package database

import (
	"database/sql"
	"fmt"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	// Migration 1: transfers table
	`CREATE TABLE IF NOT EXISTS transfers (
		id TEXT PRIMARY KEY,
		status TEXT NOT NULL DEFAULT 'pending',
		expires_at DATETIME NOT NULL,
		max_downloads INTEGER DEFAULT 0,
		download_count INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		completed_at DATETIME
	)`,
	// Migration 2: files (blobs) table
	`CREATE TABLE IF NOT EXISTS files (
		id TEXT PRIMARY KEY,
		transfer_id TEXT NOT NULL,
		size INTEGER NOT NULL DEFAULT 0,
		upload_offset INTEGER NOT NULL DEFAULT 0,
		upload_complete INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (transfer_id) REFERENCES transfers(id) ON DELETE CASCADE
	)`,
	// Migration 3: manifests table
	`CREATE TABLE IF NOT EXISTS manifests (
		transfer_id TEXT PRIMARY KEY,
		data BLOB NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (transfer_id) REFERENCES transfers(id) ON DELETE CASCADE
	)`,
	// Migration 4: drop slots table
	`CREATE TABLE IF NOT EXISTS slots (
		id TEXT PRIMARY KEY,
		status TEXT NOT NULL DEFAULT 'waiting',
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	// Migration 5: slot_transfers junction
	`CREATE TABLE IF NOT EXISTS slot_transfers (
		slot_id TEXT NOT NULL,
		transfer_id TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (slot_id, transfer_id),
		FOREIGN KEY (slot_id) REFERENCES slots(id) ON DELETE CASCADE,
		FOREIGN KEY (transfer_id) REFERENCES transfers(id) ON DELETE CASCADE
	)`,
	// Migration 6: download quotas apply independently to each file.
	`ALTER TABLE files ADD COLUMN download_count INTEGER NOT NULL DEFAULT 0`,
	// Migration 7: explicit recipient-reported completion, separate from GET attempts.
	`ALTER TABLE transfers ADD COLUMN downloaded_at DATETIME`,
	// Legacy rows stay NULL and require explicit compatibility opt-in to delete.
	`ALTER TABLE transfers ADD COLUMN delete_token_hash BLOB`,
	`ALTER TABLE slots ADD COLUMN delete_token_hash BLOB`,
	`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL COLLATE NOCASE UNIQUE, role TEXT NOT NULL CHECK(role IN ('admin','user')), disabled INTEGER NOT NULL DEFAULT 0, password_hash BLOB NOT NULL)`,
	`CREATE TABLE sessions (id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,token_hash BLOB NOT NULL UNIQUE,device_name TEXT NOT NULL,created_at DATETIME NOT NULL,expires_at DATETIME NOT NULL)`,
	`CREATE TABLE pairings (code_hash BLOB PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,expires_at DATETIME NOT NULL)`,
	`ALTER TABLE transfers ADD COLUMN owner_id TEXT REFERENCES users(id)`,
	`ALTER TABLE slots ADD COLUMN owner_id TEXT REFERENCES users(id)`,
	`ALTER TABLE slots ADD COLUMN upload_count INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE slots ADD COLUMN reserved_bytes INTEGER NOT NULL DEFAULT 0`,
	`UPDATE slots SET upload_count=(SELECT COUNT(*) FROM slot_transfers WHERE slot_id=slots.id), reserved_bytes=(SELECT COALESCE(SUM(f.size),0) FROM files f JOIN slot_transfers st ON st.transfer_id=f.transfer_id WHERE st.slot_id=slots.id)`,
	// Pairing identifiers are public tracking handles, separate from login secrets.
	`ALTER TABLE pairings ADD COLUMN id TEXT`,
	`UPDATE pairings SET id=lower(hex(randomblob(16))) WHERE id IS NULL`,
	`CREATE UNIQUE INDEX pairing_id ON pairings(id)`,
	`ALTER TABLE pairings ADD COLUMN status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','connected','canceled'))`,
	`ALTER TABLE pairings ADD COLUMN device_name TEXT NOT NULL DEFAULT ''`,
}

func runMigrations(db *sql.DB) error {
	// Ensure schema_migrations table exists (migration 0).
	if _, err := db.Exec(migrations[0]); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for i := 1; i < len(migrations); i++ {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", i).Scan(&count)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", i, err)
		}
		if count > 0 {
			continue
		}

		if _, err := db.Exec(migrations[i]); err != nil {
			return fmt.Errorf("run migration %d: %w", i, err)
		}
		if _, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", i); err != nil {
			return fmt.Errorf("record migration %d: %w", i, err)
		}
	}

	return nil
}
