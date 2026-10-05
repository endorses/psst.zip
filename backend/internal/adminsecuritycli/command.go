// Package adminsecuritycli supports local recovery without an email service.
package adminsecuritycli

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/endorses/psst.zip/backend/internal/database"
)

const Usage = "Usage: server admin-factor-reset --username NAME --confirm\nUses the existing DB_PATH. Resets only this administrator's second factor and recovery codes, and revokes their sessions and pairing grants. Password, role and disabled state remain unchanged.\n"

func Run(path string, args []string, out io.Writer) error {
	// Deliberately require explicit confirmation and exactly one named account.
	// Never consume a password, factor secret or recovery code on the command line.
	if len(args) != 3 || args[0] != "--username" || args[2] != "--confirm" ||
		len(args[1]) < 3 || len(args[1]) > 64 || strings.TrimSpace(args[1]) != args[1] {
		return errors.New("an administrator username and --confirm are required")
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
	uri.RawQuery = url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(5000)", "foreign_keys(1)"}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return errors.New("could not open server database")
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	if err := q.ValidateIncidentSchema(); err != nil {
		return errors.New("database is not a supported psst.zip server database; no change made")
	}
	if err := q.ResetAdminFactor(args[1]); err != nil {
		return errors.New("could not reset administrator factor; verify the account and database")
	}
	if q.SecurityAuditDegraded() {
		if _, err := fmt.Fprintln(out, "Security audit is unavailable; this recovery action may not have been recorded."); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(out, "Administrator second factor reset. Existing sessions and recovery codes are invalid. Sign in with the existing password and enroll a new authenticator.")
	return err
}
