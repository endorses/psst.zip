package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Open opens (or creates) a SQLite database at the given path and runs migrations.
func Open(dbPath string) (*sql.DB, error) {
	source := ":memory:?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	if dbPath != ":memory:" {
		absolute, err := filepath.Abs(dbPath)
		if err != nil {
			return nil, fmt.Errorf("resolve database path: %w", err)
		}
		if err = protectDatabaseFiles(absolute); err != nil {
			return nil, err
		}
		uri := url.URL{Scheme: "file", Path: absolute}
		uri.RawQuery = url.Values{"_pragma": []string{"journal_mode(wal)", "busy_timeout(5000)", "foreign_keys(on)"}}.Encode()
		source = uri.String()
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	if _, err := db.Exec(`UPDATE history_sync_state SET generation=? WHERE id=1`, uuid.NewString()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("rotate history generation: %w", err)
	}
	return db, nil
}

func protectDatabaseFiles(path string) error {
	if path == ":memory:" {
		return nil
	}
	if info, err := os.Stat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("database must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect database permissions: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("protect database: %w", err)
	}
	if err = file.Chmod(0600); err != nil {
		_ = file.Close()
		return fmt.Errorf("protect database permissions: %w", err)
	}
	if err = file.Close(); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("database journal must be a regular file")
		}
		if err = os.Chmod(path+suffix, 0600); err != nil {
			return fmt.Errorf("protect database journal permissions: %w", err)
		}
	}
	return nil
}
