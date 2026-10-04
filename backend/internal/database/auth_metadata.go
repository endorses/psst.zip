package database

import (
	"database/sql"
	"errors"
	"time"
)

const (
	MaxAuthUsers                  = 1000
	MaxAuthSessionsPerUser        = 32
	MaxAuthPendingPairingsPerUser = 8
	MaxAuthPairingsPerUser        = 64
	authPruneBatch                = 256
	authRotationBatch             = 128
)

var ErrAccountCapacity = errors.New("account capacity reached")
var ErrPairingCapacity = errors.New("pairing capacity reached")

func authMetadataMigration() string {
	return `
 CREATE INDEX auth_session_expiry ON sessions(substr(expires_at,1,19),id);
 CREATE INDEX auth_session_user_expiry ON sessions(user_id,expires_at,id);
 CREATE INDEX auth_pairing_expiry ON pairings(substr(expires_at,1,19),id);
 CREATE INDEX auth_pairing_user ON pairings(user_id,status,substr(expires_at,1,19));
 CREATE INDEX auth_pairing_user_expiry ON pairings(user_id,substr(expires_at,1,19),id);
 CREATE INDEX auth_pairing_parent ON pairings(session_id,id);
 CREATE INDEX auth_pending_expiry ON admin_pending_factors(expires_at,user_id);
 CREATE TABLE auth_metadata_cleanup(id INTEGER PRIMARY KEY CHECK(id=1),user_cursor TEXT NOT NULL DEFAULT '');
 INSERT INTO auth_metadata_cleanup(id) VALUES(1);
 `
}

// All stored credentials use UTC Go timestamps. Comparing their date/time prefix
// is indexable and deliberately waits until the next whole second to prune:
// fractional expiry never permits an early credential deletion.
func authTimePrefix(at time.Time) string { return at.UTC().Format("2006-01-02 15:04:05") }

func authCountAtLeast(tx *sql.Tx, table, user string, maximum int) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM (SELECT 1 FROM ` + table
	args := []any{}
	if user != "" {
		query += ` WHERE user_id=?`
		args = append(args, user)
	}
	query += ` LIMIT ?)`
	args = append(args, maximum)
	err := tx.QueryRow(query, args...).Scan(&count)
	return count >= maximum, err
}

// retireAuthSessions revokes authority without cascading through a legacy
// account's potentially enormous pairing history. Deletion happens separately.
func retireAuthSessions(tx *sql.Tx, user, preserve string, keep, batch int, now time.Time) error {
	if preserve != "" {
		keep--
	}
	_, err := tx.Exec(`UPDATE sessions SET expires_at=? WHERE id IN (
 SELECT id FROM sessions WHERE user_id=? AND id!=? AND expires_at>?
 AND id NOT IN (SELECT id FROM sessions WHERE user_id=? AND id!=? AND expires_at>? ORDER BY expires_at DESC,id DESC LIMIT ?)
 ORDER BY expires_at,id LIMIT ?)`, now.Add(-time.Second).UTC(), user, preserve, now.UTC(), user, preserve, now.UTC(), keep, batch)
	return err
}

func pruneUserAuth(tx *sql.Tx, user string, now time.Time) error {
	_, err := tx.Exec(`DELETE FROM pairings WHERE id IN (SELECT id FROM pairings WHERE user_id=? AND substr(expires_at,1,19)<? ORDER BY substr(expires_at,1,19),id LIMIT ?)`, user, authTimePrefix(now.Add(-15*time.Minute)), MaxAuthPairingsPerUser)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM pairings WHERE id IN (SELECT p.id FROM (SELECT id FROM sessions WHERE user_id=? AND expires_at<? ORDER BY expires_at,id LIMIT ?) s JOIN pairings p ON p.session_id=s.id LIMIT ?)`, user, now.UTC(), authRotationBatch, MaxAuthPairingsPerUser)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM sessions WHERE id IN (SELECT s.id FROM (SELECT id FROM sessions WHERE user_id=? AND expires_at<? ORDER BY expires_at,id LIMIT ?) s WHERE NOT EXISTS(SELECT 1 FROM pairings p WHERE p.session_id=s.id))`, user, now.UTC(), authRotationBatch)
	return err
}

// Must run after credential validation under the same writer transaction as
// insertion. New sign-ins replace earliest-expiring authority; failed proofs never do.
func reserveAuthSession(tx *sql.Tx, user, preserve string, now time.Time) error {
	if err := pruneUserAuth(tx, user, now); err != nil {
		return err
	}
	if err := retireAuthSessions(tx, user, preserve, MaxAuthSessionsPerUser-1, authRotationBatch, now); err != nil {
		return err
	}
	return pruneUserAuth(tx, user, now)
}

// AuthenticationSessionList bounds even legacy overages while including the
// requesting session. Counts make partial upgrade views explicit to clients.
type AuthenticationSessionList struct {
	Sessions         []Session
	TotalActive      int
	TotalActiveExact bool
	Limited          bool
}

func (q *Queries) AuthenticationSessions(user, current string) (AuthenticationSessionList, error) {
	out := AuthenticationSessionList{Sessions: []Session{}}
	now := time.Now()
	tx, err := q.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	// Go's UTC timestamp encoding sorts chronologically, including variable
	// fractional precision. Verify selected rows with time.Time as well.
	rows, err := tx.Query(`SELECT id,user_id,device_name,created_at,expires_at FROM sessions WHERE user_id=? AND expires_at>? ORDER BY expires_at DESC,id DESC LIMIT ?`, user, now.UTC(), MaxAuthSessionsPerUser+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var s Session
		if err = rows.Scan(&s.ID, &s.UserID, &s.DeviceName, &s.CreatedAt, &s.ExpiresAt); err != nil {
			rows.Close()
			return out, err
		}
		if now.Before(s.ExpiresAt) {
			out.Sessions = append(out.Sessions, s)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.TotalActive = len(out.Sessions)
	out.Limited = out.TotalActive > MaxAuthSessionsPerUser
	out.TotalActiveExact = !out.Limited
	if len(out.Sessions) > MaxAuthSessionsPerUser {
		out.Sessions = out.Sessions[:MaxAuthSessionsPerUser]
	}
	found := false
	for _, s := range out.Sessions {
		found = found || s.ID == current
	}
	if current != "" && !found {
		var s Session
		err = tx.QueryRow(`SELECT id,user_id,device_name,created_at,expires_at FROM sessions WHERE id=? AND user_id=?`, current, user).Scan(&s.ID, &s.UserID, &s.DeviceName, &s.CreatedAt, &s.ExpiresAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if err == nil && now.Before(s.ExpiresAt) {
			if len(out.Sessions) == MaxAuthSessionsPerUser {
				out.Sessions = out.Sessions[:len(out.Sessions)-1]
			}
			out.Sessions = append(out.Sessions, s)
		}
	}
	return out, tx.Commit()
}

// PruneAuthentication has fixed row budgets and a durable account cursor. It
// never deletes accounts or recovery factors and does not cascade through an
// unbounded pre-upgrade pairing tree. Repeated sweeps converge legacy overages.
func (q *Queries) PruneAuthentication() error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	if _, err = tx.Exec(`UPDATE auth_metadata_cleanup SET user_cursor=user_cursor WHERE id=1`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM admin_pending_factors WHERE user_id IN (SELECT user_id FROM admin_pending_factors WHERE expires_at<=? ORDER BY expires_at,user_id LIMIT ?)`, now.Unix(), authPruneBatch); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM pairings WHERE id IN (SELECT id FROM pairings WHERE substr(expires_at,1,19)<? ORDER BY substr(expires_at,1,19),id LIMIT ?)`, authTimePrefix(now.Add(-15*time.Minute)), authPruneBatch); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM pairings WHERE id IN (SELECT p.id FROM (SELECT id FROM sessions WHERE substr(expires_at,1,19)<? ORDER BY substr(expires_at,1,19),id LIMIT ?) s JOIN pairings p ON p.session_id=s.id LIMIT ?)`, authTimePrefix(now), authPruneBatch, authPruneBatch); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM sessions WHERE id IN (SELECT s.id FROM (SELECT id FROM sessions WHERE substr(expires_at,1,19)<? ORDER BY substr(expires_at,1,19),id LIMIT ?) s WHERE NOT EXISTS(SELECT 1 FROM pairings p WHERE p.session_id=s.id))`, authTimePrefix(now), authPruneBatch); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id FROM users WHERE id>(SELECT user_cursor FROM auth_metadata_cleanup WHERE id=1) ORDER BY id LIMIT 8`)
	if err != nil {
		return err
	}
	users := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		users = append(users, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, user := range users {
		if err = retireAuthSessions(tx, user, "", MaxAuthSessionsPerUser, 32, now); err != nil {
			return err
		}
	}
	cursor := ""
	if len(users) == 8 {
		cursor = users[len(users)-1]
	}
	if _, err = tx.Exec(`UPDATE auth_metadata_cleanup SET user_cursor=? WHERE id=1`, cursor); err != nil {
		return err
	}
	return tx.Commit()
}
