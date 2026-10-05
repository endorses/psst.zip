package database

import (
	"database/sql"
	"io"
	"testing"
	"time"
)

func closeFixture(t *testing.T, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
}

// Historical migration fixtures must write their actual old schema, rather than
// invoke current creation methods that may require columns not introduced yet.
func legacyCreateTransfer(db *sql.DB, id string, expires time.Time) error {
	_, err := db.Exec(`INSERT INTO transfers(id,status,expires_at,max_downloads) VALUES(?,'pending',?,0)`, id, expires.UTC())
	return err
}
func legacyCreateSlot(db *sql.DB, id string, expires time.Time, owner string) error {
	var ownership any
	if owner != "" {
		ownership = owner
	}
	_, err := db.Exec(`INSERT INTO slots(id,status,expires_at,owner_id) VALUES(?,'waiting',?,?)`, id, expires.UTC(), ownership)
	return err
}
