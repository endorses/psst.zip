package database

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadAcknowledgementMigrationPreservesExistingTransfers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Construct the real version-6 schema, without the new acknowledgement column.
	for version, migration := range migrations[:7] {
		if _, err := legacy.Exec(migration); err != nil {
			t.Fatal(err)
		}
		if version > 0 {
			if _, err := legacy.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := NewQueries(legacy)
	if err := q.CreateTransfer("existing-transfer", time.Now().Add(time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("existing-file", "existing-transfer", 4); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("existing-file", 4, true); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("existing-transfer", []byte("opaque manifest")); err != nil {
		t.Fatal(err)
	}
	if err := q.CompleteTransfer("existing-transfer"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := q.ReserveFileDownload("existing-transfer", "existing-file"); err != nil || !allowed {
		t.Fatalf("reserve existing: %v %v", allowed, err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	q = NewQueries(upgraded)
	transfer, err := q.GetTransfer("existing-transfer")
	if err != nil {
		t.Fatal(err)
	}
	if transfer.Status != "complete" || transfer.DownloadCount != 1 || transfer.DownloadedAt.Valid {
		t.Fatalf("migration changed existing facts: %+v", transfer)
	}
	file, err := q.GetFile("existing-file")
	if err != nil || file.DownloadCount != 1 || !file.UploadComplete {
		t.Fatalf("migration lost file: %+v %v", file, err)
	}
	manifest, err := q.GetManifest("existing-transfer")
	if err != nil || string(manifest) != "opaque manifest" {
		t.Fatalf("migration lost manifest: %q %v", manifest, err)
	}
	acknowledgedAt := time.Now().UTC().Truncate(time.Microsecond)
	if allowed, err := q.AcknowledgeDownload("existing-transfer", acknowledgedAt); err != nil || !allowed {
		t.Fatalf("acknowledge upgraded: %v %v", allowed, err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	q = NewQueries(reopened)
	if allowed, err := q.AcknowledgeDownload("existing-transfer", acknowledgedAt.Add(time.Minute)); err != nil || !allowed {
		t.Fatalf("retry reopened: %v %v", allowed, err)
	}
	transfer, err = q.GetTransfer("existing-transfer")
	if err != nil || !transfer.DownloadedAt.Valid || !transfer.DownloadedAt.Time.Equal(acknowledgedAt) {
		t.Fatalf("reopen lost first acknowledgement: %+v %v", transfer, err)
	}
}
