package testutil

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func closeDatabase(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseTemplateIsolatedClonesAndProductionReopen(t *testing.T) {
	var template databaseTemplate
	t.Cleanup(func() {
		if err := template.close(); err != nil {
			t.Fatal(err)
		}
	})
	calls := 0
	opener := func(path string) (*sql.DB, error) {
		calls++
		return database.Open(path)
	}
	path := filepath.Join(t.TempDir(), "first.db")
	first, err := template.open(path, opener)
	if err != nil {
		t.Fatal(err)
	}
	var firstGeneration string
	if err := first.QueryRow(`SELECT generation FROM history_sync_state`).Scan(&firstGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.NewQueries(first).CreateUser(database.User{ID: "alice", Username: "alice", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := template.open(filepath.Join(t.TempDir(), "second.db"), opener)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDatabase(t, second)
	var users, migrations, triggers int
	var secondGeneration string
	if err := second.QueryRow(`SELECT (SELECT COUNT(*) FROM users),(SELECT COUNT(*) FROM schema_migrations),(SELECT COUNT(*) FROM sqlite_schema WHERE type='trigger'),generation FROM history_sync_state`).Scan(&users, &migrations, &triggers, &secondGeneration); err != nil {
		t.Fatal(err)
	}
	if users != 0 || migrations == 0 || triggers == 0 || secondGeneration == firstGeneration {
		t.Fatalf("clone shares data/generation or lost schema: %d users, %d migrations, %d triggers", users, migrations, triggers)
	}
	reopened, err := template.open(path, opener)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDatabase(t, reopened)
	var reopenedGeneration string
	if err := reopened.QueryRow(`SELECT (SELECT COUNT(*) FROM users WHERE id='alice'),generation FROM history_sync_state`).Scan(&users, &reopenedGeneration); err != nil {
		t.Fatal(err)
	}
	if users != 1 || reopenedGeneration == firstGeneration || calls != 4 {
		t.Fatalf("reopen overwrote data or bypassed production opener: %d users, %d calls", users, calls)
	}
	for _, privatePath := range []string{template.path, path} {
		info, err := os.Stat(privatePath)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private database permissions: %v / %v", info, err)
		}
	}
}

func TestDatabaseTemplateParallelClones(t *testing.T) {
	var template databaseTemplate
	t.Cleanup(func() {
		if err := template.close(); err != nil {
			t.Fatal(err)
		}
	})
	var calls atomic.Int32
	var generations sync.Map
	opener := func(path string) (*sql.DB, error) {
		calls.Add(1)
		return database.Open(path)
	}
	t.Run("parallel", func(t *testing.T) {
		for i := 0; i < 8; i++ {
			t.Run(string(rune('a'+i)), func(t *testing.T) {
				t.Parallel()
				db, err := template.open(filepath.Join(t.TempDir(), "clone.db"), opener)
				if err != nil {
					t.Fatal(err)
				}
				defer closeDatabase(t, db)
				var generation string
				var users int
				if err := db.QueryRow(`SELECT generation,(SELECT COUNT(*) FROM users) FROM history_sync_state`).Scan(&generation, &users); err != nil {
					t.Fatal(err)
				}
				if _, shared := generations.LoadOrStore(generation, true); shared || users != 0 {
					t.Fatal("parallel clone shares generation or users")
				}
			})
		}
	})
	if calls.Load() != 9 {
		t.Fatalf("template initialized more than once: %d calls", calls.Load())
	}
}

func TestDatabaseTemplateInitializationFailureRemovesFiles(t *testing.T) {
	var template databaseTemplate
	cause := errors.New("migration failed")
	calls := 0
	opener := func(path string) (*sql.DB, error) {
		calls++
		if err := os.WriteFile(path, []byte("partial database"), 0600); err != nil {
			t.Fatal(err)
		}
		return nil, cause
	}
	for i := 0; i < 2; i++ {
		path := filepath.Join(t.TempDir(), "clone.db")
		if _, err := template.open(path, opener); !errors.Is(err, cause) {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("failed initialization created clone", err)
		}
	}
	if _, err := os.Stat(template.dir); !os.IsNotExist(err) || calls != 1 {
		t.Fatal("failed template leaked or retried initialization", calls, err)
	}
}
