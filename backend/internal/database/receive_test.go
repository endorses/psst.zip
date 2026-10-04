package database

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReceiveFileAllowanceConcurrentCumulativeAndRestartSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receive.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueries(db)
	if err := q.CreateReceiveSlot("slot", time.Now().Add(time.Hour), nil, "", 2, "public-key", 3); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if err := q.CreateSlotTransfer("slot", fmt.Sprint(i), time.Now().Add(time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			results <- q.CreateFileWithQuota(fmt.Sprint("file", i), fmt.Sprint(i), 0, 1024)
		}(i)
	}
	wait.Wait()
	close(results)
	success, limited := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrSlotFileQuota) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if success != 3 || limited != 13 {
		t.Fatalf("success=%d limited=%d", success, limited)
	}
	slot, err := q.GetSlot("slot")
	if err != nil || slot.ReservedFiles != 3 || slot.ReservedBytes != 0 {
		t.Fatalf("slot=%+v %v", slot, err)
	}
	var file, transfer string
	if err := db.QueryRow(`SELECT id,transfer_id FROM files LIMIT 1`).Scan(&file, &transfer); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset(file, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteTransfer(transfer); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlotTransfer("slot", "new-child", time.Now().Add(time.Hour), 0, nil); !errors.Is(err, ErrSlotFileQuota) {
		t.Fatalf("deletion refunded file allowance: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	slot, err = NewQueries(db).GetSlot("slot")
	if err != nil || slot.ReservedFiles != 3 || slot.MaxFiles != 3 {
		t.Fatalf("restart lost reservation: %+v %v", slot, err)
	}
}
func TestReceiveFileAllowanceRollbackAndByteQuota(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := NewQueries(db)
	if err := q.CreateReceiveSlot("slot", time.Now().Add(time.Hour), nil, "", 2, "key", 2); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlotTransfer("slot", "child", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFileWithQuota("file", "child", 4, 5); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFileWithQuota("file", "child", 0, 5); err == nil {
		t.Fatal("duplicate allocation accepted")
	}
	if err := q.CreateFileWithQuota("over-budget", "child", 2, 5); !errors.Is(err, ErrSlotQuota) {
		t.Fatalf("byte quota bypassed: %v", err)
	}
	slot, err := q.GetSlot("slot")
	if err != nil || slot.ReservedFiles != 1 || slot.ReservedBytes != 4 {
		t.Fatalf("failed transaction consumed reservation: %+v %v", slot, err)
	}
	if err := q.CreateFileWithQuota("last", "child", 1, 5); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFileWithQuota("empty", "child", 0, 5); !errors.Is(err, ErrSlotFileQuota) {
		t.Fatalf("zero byte allocation bypassed file quota: %v", err)
	}
}
func TestReceivePolicyMigrationPreservesLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for version, migration := range migrations {
		if strings.Contains(migration, "ADD COLUMN receive_protocol") {
			break
		}
		if _, err := db.Exec(migration); err != nil {
			t.Fatal(err)
		}
		if version > 0 {
			if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := NewQueries(db)
	until := time.Now().Add(time.Hour)
	if err := q.CreateSlot("legacy", until, []byte("private-token-hash")); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("existing", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer("legacy", "existing"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("known-file", "existing", 42); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q = NewQueries(db)
	slot, err := q.GetSlot("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if slot.ReceiveProtocol != 1 || slot.MaxFiles != 0 || slot.ReservedFiles != 1 || slot.RecipientPublicKey != "" || string(slot.DeleteTokenHash) != "private-token-hash" {
		t.Fatalf("migration changed legacy data: %+v", slot)
	}
	files, err := q.ListFiles("existing")
	if err != nil || len(files) != 1 || files[0].Size != 42 {
		t.Fatalf("lost existing file: %+v %v", files, err)
	}
}
