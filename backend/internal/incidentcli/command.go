// Package incidentcli provides recovery when the HTTP administration UI is unavailable.
package incidentcli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/endorses/psst.zip/backend/internal/database"
)

const Usage = "Usage: server [pause|resume|incident-status]\nUses DB_PATH (the same database as the running server). Pause/resume persists across restarts.\nThe running server observes changes and cancels payload streams within its monitor interval.\n"

func Run(path, action string, out io.Writer) error {
	if action != "pause" && action != "resume" && action != "incident-status" {
		return errors.New("unknown incident command")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("existing server database is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return errors.New("invalid server database path")
	}
	uri := url.URL{Scheme: "file", Path: absolute}
	query := url.Values{"mode": []string{"rw"}, "_pragma": []string{"busy_timeout(5000)", "foreign_keys(1)"}}
	if action == "incident-status" {
		query.Set("mode", "ro")
	}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return errors.New("could not open server database")
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	if err := q.ValidateIncidentSchema(); err != nil {
		return errors.New("database is not a supported psst.zip server database; no change made")
	}
	if action != "incident-status" {
		if err := q.SetTransfersPausedLocal(action == "pause"); err != nil {
			return errors.New("could not persist transfer control")
		}
	}
	state, err := q.IncidentState()
	if err != nil {
		return errors.New("could not read transfer control")
	}
	return json.NewEncoder(out).Encode(struct {
		database.IncidentState
		AuditDegraded bool `json:"audit_degraded"`
	}{state, q.SecurityAuditDegraded()})
}
