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
	if _, err := legacy.Exec(`INSERT INTO transfers (id, expires_at, max_downloads) VALUES (?, ?, ?)`, "existing-transfer", time.Now().Add(time.Hour).UTC(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO files(id,transfer_id,size) VALUES(?,?,?)`, "existing-file", "existing-transfer", 4); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("existing-file", 4, true); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO manifests(transfer_id,data) VALUES(?,?)`, "existing-transfer", []byte("opaque manifest")); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`UPDATE transfers SET status='complete', completed_at=CURRENT_TIMESTAMP WHERE id='existing-transfer'`); err != nil {
		t.Fatal(err)
	}
	if allowed, err := q.ReserveFileDownload("existing-transfer", "existing-file"); err != nil || !allowed {
		t.Fatalf("reserve existing: %v %v", allowed, err)
	}
	if _, err := legacy.Exec(`INSERT INTO slots (id, expires_at) VALUES (?, ?)`, "existing-slot", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
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
	if len(transfer.DeleteTokenHash) != 0 {
		t.Fatal("migration invented a legacy transfer token")
	}
	slot, err := q.GetSlot("existing-slot")
	if err != nil || slot.Status != "waiting" || len(slot.DeleteTokenHash) != 0 {
		t.Fatalf("migration changed legacy slot: %+v %v", slot, err)
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

func TestPairingTrackingMigrationPreservesOutstandingGrants(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pairings.db")
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for version, migration := range migrations[:18] {
		if _, err := legacy.Exec(migration); err != nil {
			t.Fatal(err)
		}
		if version > 0 {
			if _, err := legacy.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := NewQueries(legacy)
	user := User{ID: "owner", Username: "owner", Role: "user", PasswordHash: []byte("hash")}
	if _, err := legacy.Exec(`INSERT INTO users(id,username,role,password_hash) VALUES(?,?,?,?)`, user.ID, user.Username, user.Role, user.PasswordHash); err != nil {
		t.Fatal(err)
	}
	session := Session{ID: "browser", UserID: user.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := legacy.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES(?,?,?,?,?,?)`, session.ID, user.ID, []byte("browser-hash"), session.DeviceName, session.CreatedAt.UTC(), session.ExpiresAt.UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO pairings(code_hash,user_id,session_id,expires_at) VALUES(?,?,?,?)`, []byte("grant-hash"), user.ID, session.ID, time.Now().Add(time.Minute).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	q = NewQueries(upgraded)
	var id string
	if err := upgraded.QueryRow(`SELECT id FROM pairings`).Scan(&id); err != nil || id == "" {
		t.Fatalf("missing tracking ID %q %v", id, err)
	}
	status, err := q.PairingStatus(id, user.ID, session.ID)
	if err != nil || status.Status != "pending" {
		t.Fatalf("status %v %v", status, err)
	}
	phone := Session{ID: "phone", DeviceName: "Migrated phone", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	owner, err := q.RedeemPairing([]byte("grant-hash"), []byte("phone-hash"), phone)
	if err != nil || owner.ID != user.ID {
		t.Fatalf("legacy grant invalidated %v %v", owner, err)
	}
	status, err = q.PairingStatus(id, user.ID, session.ID)
	if err != nil || status.Status != "connected" {
		t.Fatalf("completion %v %v", status, err)
	}
	if _, err := q.RedeemPairing([]byte("grant-hash"), []byte("replay-hash"), phone); err == nil {
		t.Fatal("legacy grant replay accepted")
	}
}
