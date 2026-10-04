package incidentcli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestIncidentCLIStateAndMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	if err := Run(path, "pause", &bytes.Buffer{}); err == nil {
		t.Fatal("created nonexistent server database")
	}
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	for _, step := range []struct{ action, want string }{{"pause", `"public_transfers_paused":true`}, {"incident-status", `"public_transfers_paused":true`}, {"resume", `"public_transfers_paused":false`}} {
		var out bytes.Buffer
		if err := Run(path, step.action, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), step.want) {
			t.Fatalf("%s: %s", step.action, out.String())
		}
	}
	if err := Run(path, "delete-everything", &bytes.Buffer{}); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestIncidentCLIRejectsEmptyAndUnrelatedDatabasesWithoutMutation(t *testing.T) {
	for _, kind := range []string{"empty", "unrelated"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wrong.db")
			if kind == "empty" {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`CREATE TABLE unrelated(value TEXT); INSERT INTO unrelated VALUES('keep this data')`); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"incident-status", "pause", "resume"} {
				var out bytes.Buffer
				if err := Run(path, action, &out); err == nil {
					t.Fatalf("%s accepted wrong database", action)
				}
				if out.Len() != 0 {
					t.Fatalf("%s printed a misleading state: %s", action, out.String())
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatalf("%s modified %s database", action, kind)
				}
			}
		})
	}
}

func TestIncidentCLIWithoutAuditStoragePreservesJSONRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE security_events`); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"pause", "resume"} {
		var out bytes.Buffer
		if err := Run(path, action, &out); err != nil {
			t.Fatal(err)
		}
		var state map[string]any
		if err := json.Unmarshal(out.Bytes(), &state); err != nil {
			t.Fatal("recovery output is not JSON", err)
		}
		if state["audit_degraded"] != true || state["public_transfers_paused"] != (action == "pause") {
			t.Fatal(state)
		}
	}
}
