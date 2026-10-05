package database

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This child leaves committed policy changes in WAL without closing SQLite.
// The parent kills and waits for it before copying any files; no live instance
// or operator-supplied path is ever used by the exercise.
func TestRestoreJournalWriter(t *testing.T) {
	if os.Getenv("PSST_RESTORE_JOURNAL_CHILD") != "1" {
		return
	}
	path := os.Getenv("PSST_RESTORE_JOURNAL_DB")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, db)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, conn)
	if _, err = conn.ExecContext(context.Background(), `PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	// Use this held connection so its automatic WAL checkpoint stays disabled.
	if _, err = conn.ExecContext(context.Background(), `UPDATE server_settings SET max_file_size=34603008 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(context.Background(), `UPDATE incident_state SET public_transfers_paused=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	fmt.Println("restore-wal-ready")
	// The test parent must terminate this child while its connection remains open.
	time.Sleep(30 * time.Second)
	t.Fatal("parent did not terminate the journal writer")
}

func TestRestoreColdCopyRetainsCommittedWAL(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "server.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueries(db)
	if err = q.SetMaxFileSize(25 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(binary, "-test.run=^TestRestoreJournalWriter$")
	// Deliberately exclude the parent application's database/configuration env.
	child.Env = []string{"PSST_RESTORE_JOURNAL_CHILD=1", "PSST_RESTORE_JOURNAL_DB=" + path}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "restore-wal-ready\n" {
			_ = child.Process.Kill()
			_ = child.Wait()
			waited = true
			t.Fatalf("writer failed before committed WAL: %q %s", line, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("journal writer readiness timed out")
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err == nil {
		t.Fatal("writer exited cleanly instead of retaining its WAL")
	}
	waited = true
	wal, err := os.Stat(path + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("exercise did not retain a committed WAL: %v", err)
	}

	for _, includeSidecars := range []bool{true, false} {
		name := "complete-stopped-set"
		if !includeSidecars {
			name = "incomplete-main-file-only"
		}
		t.Run(name, func(t *testing.T) {
			restored := filepath.Join(t.TempDir(), "server.db")
			copyRestoreJournalFile(t, path, restored)
			if includeSidecars {
				for _, suffix := range []string{"-wal", "-shm", "-journal"} {
					if _, err := os.Stat(path + suffix); os.IsNotExist(err) {
						continue
					} else if err != nil {
						t.Fatal(err)
					}
					copyRestoreJournalFile(t, path+suffix, restored+suffix)
				}
			}
			rdb, err := Open(restored)
			if err != nil {
				t.Fatal(err)
			}
			defer closeFixture(t, rdb)
			rq := NewQueries(rdb)
			limit, err := rq.MaxFileSize()
			if err != nil {
				t.Fatal(err)
			}
			state, err := rq.IncidentState()
			if err != nil {
				t.Fatal(err)
			}
			wantLimit := int64(25 << 20)
			if includeSidecars {
				wantLimit = 33 << 20
			}
			if limit != wantLimit || state.PublicTransfersPaused != includeSidecars {
				t.Fatalf("restored state limit=%d paused=%v, want limit=%d paused=%v", limit, state.PublicTransfersPaused, wantLimit, includeSidecars)
			}
			var integrity string
			if err := rdb.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity %q: %v", integrity, err)
			}
			// Both databases can be structurally valid. Integrity checks cannot
			// establish that a backup includes the latest committed policy state.
		})
	}
}

func copyRestoreJournalFile(t *testing.T, source, target string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, in)
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
}
