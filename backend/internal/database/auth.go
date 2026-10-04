package database

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	Disabled           bool   `json:"disabled"`
	PasswordHash       []byte `json:"-"`
	MustChangePassword bool   `json:"must_change_password"`
}
type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"-"`
	DeviceName string    `json:"device_name"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

var ErrAdminTransfer = errors.New("administrator accounts cannot transfer files")
var ErrPasswordChangeRequired = errors.New("password change required")

var ErrLastAdmin = errors.New("cannot disable the last enabled administrator")

func (q *Queries) UserCount() (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}
func (q *Queries) CreateUser(u User, bootstrap bool, actors ...*AdminActor) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = ValidateAdminActor(tx, optionalAdminActor(actors)); err != nil {
		return err
	}
	query := `INSERT INTO users (id,username,role,password_hash,must_change_password) VALUES (?,?,?,?,?)`
	if bootstrap {
		query = `INSERT INTO users (id,username,role,password_hash,must_change_password) SELECT ?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM users)`
	}
	_, err = tx.Exec(query, u.ID, u.Username, u.Role, u.PasswordHash, u.MustChangePassword && u.Role != "admin")
	if err != nil {
		return err
	}
	return tx.Commit()
}
func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.PasswordHash, &u.MustChangePassword)
	return u, err
}
func (q *Queries) UserByName(name string) (*User, error) {
	return scanUser(q.db.QueryRow(`SELECT id,username,role,disabled,password_hash,must_change_password FROM users WHERE username=? COLLATE NOCASE`, name))
}
func (q *Queries) UserByID(id string) (*User, error) {
	return scanUser(q.db.QueryRow(`SELECT id,username,role,disabled,password_hash,must_change_password FROM users WHERE id=?`, id))
}
func (q *Queries) Users() ([]User, error) {
	rows, err := q.db.Query(`SELECT id,username,role,disabled,password_hash,must_change_password FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}
func (q *Queries) UpdateUser(id string, disabled *bool, password []byte, actors ...*AdminActor) error {
	return q.updateUser(id, disabled, password, nil, "", optionalAdminActor(actors))
}

// ChangePassword compares the authenticated hash under the same writer lock as
// replacement, so a concurrent administrator reset cannot be overwritten.
func (q *Queries) ChangePassword(id string, expectedHash, password []byte) error {
	return q.updateUser(id, nil, password, expectedHash, "", nil)
}

func (q *Queries) ChangeAdminPassword(id, session string, expectedHash, password []byte) error {
	return q.updateUser(id, nil, password, expectedHash, session, nil)
}

func (q *Queries) updateUser(id string, disabled *bool, password, expectedHash []byte, adminSession string, actor *AdminActor) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the SQLite writer lock before evaluating the last-admin invariant.
	res, err := tx.Exec(`UPDATE users SET disabled=disabled WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	if err := ValidateAdminActor(tx, actor); err != nil {
		return err
	}
	if adminSession != "" {
		if err := requireRecentAdmin(tx, id, adminSession, time.Now()); err != nil {
			return err
		}
	}
	if expectedHash != nil {
		var current []byte
		if err := tx.QueryRow(`SELECT password_hash FROM users WHERE id=? AND disabled=0`, id).Scan(&current); err != nil {
			return err
		}
		if !bytes.Equal(current, expectedHash) {
			return sql.ErrNoRows
		}
	}
	if disabled != nil {
		if *disabled {
			var role string
			var count int
			if err := tx.QueryRow(`SELECT role FROM users WHERE id=?`, id).Scan(&role); err != nil {
				return err
			}
			if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0 AND id!=?`, id).Scan(&count); err != nil {
				return err
			}
			if role == "admin" && count == 0 {
				return ErrLastAdmin
			}
		}
		if _, err := tx.Exec(`UPDATE users SET disabled=? WHERE id=?`, *disabled, id); err != nil {
			return err
		}
	}
	if len(password) > 0 {
		if _, err := tx.Exec(`UPDATE users SET password_hash=?,must_change_password=CASE WHEN role='user' AND ? THEN 1 ELSE 0 END WHERE id=?`, password, expectedHash == nil, id); err != nil {
			return err
		}
	}
	if len(password) > 0 || (disabled != nil && *disabled) {
		if _, err := tx.Exec(`UPDATE admin_security SET revision=revision+1 WHERE user_id=?`, id); err != nil {
			return err
		}
		for _, table := range []string{"sessions", "pairings"} {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE user_id=?`, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (q *Queries) CreateSession(s Session, hash []byte, passwordHash []byte) error {
	res, err := q.db.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) SELECT ?,id,?,?,?,? FROM users WHERE id=? AND disabled=0 AND password_hash=? AND NOT EXISTS(SELECT 1 FROM admin_security a WHERE a.user_id=users.id AND a.secret!='')`, s.ID, hash, s.DeviceName, s.CreatedAt.UTC(), s.ExpiresAt.UTC(), s.UserID, passwordHash)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
func (q *Queries) SessionByHash(hash []byte) (*Session, *User, error) {
	s := &Session{}
	var id string
	err := q.db.QueryRow(`SELECT id,user_id,device_name,created_at,expires_at FROM sessions WHERE token_hash=?`, hash).Scan(&s.ID, &id, &s.DeviceName, &s.CreatedAt, &s.ExpiresAt)
	if err != nil {
		return nil, nil, err
	}
	u, err := q.UserByID(id)
	s.UserID = id
	return s, u, err
}
func (q *Queries) Sessions(user string) ([]Session, error) {
	rows, err := q.db.Query(`SELECT id,user_id,device_name,created_at,expires_at FROM sessions WHERE user_id=?`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.UserID, &s.DeviceName, &s.CreatedAt, &s.ExpiresAt); err != nil {
			return nil, err
		}
		if time.Now().Before(s.ExpiresAt) {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}
func (q *Queries) DeleteSession(id, user string, actors ...*AdminActor) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = ValidateAdminActor(tx, optionalAdminActor(actors)); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM sessions WHERE id=? AND user_id=?`, id, user); err != nil {
		return err
	}
	return tx.Commit()
}

// PairingStatus never includes a login secret or a device session credential.
type PairingStatus struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
	DeviceName string    `json:"device_name,omitempty"`
}

var ErrPairingConnected = errors.New("pairing already connected")

// CreatePairing preserves callers that do not need to observe the grant.
func (q *Queries) CreatePairing(hash []byte, user, session string, expiry time.Time) error {
	return q.CreateTrackedPairing(uuid.NewString(), hash, user, session, expiry, "")
}

// CreateTrackedPairing replaces only the specified flow, in the same transaction.
// Taking the writer lock before inspecting a grant serializes cancellation with redemption.
func (q *Queries) CreateTrackedPairing(id string, hash []byte, user, session string, expiry time.Time, replaceID string) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if replaceID != "" {
		if err := cancelPairing(tx, replaceID, user, session); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`INSERT INTO pairings(id,code_hash,user_id,session_id,expires_at)
 SELECT ?,?,s.user_id,s.id,? FROM sessions s JOIN users u ON u.id=s.user_id
 WHERE s.id=? AND s.user_id=? AND u.disabled=0 AND u.role='user' AND u.must_change_password=0`, id, hash, expiry.UTC(), session, user)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	var parentExpiry time.Time
	if err := tx.QueryRow(`SELECT expires_at FROM sessions WHERE id=?`, session).Scan(&parentExpiry); err != nil {
		return err
	}
	if !time.Now().Before(parentExpiry) {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (q *Queries) PairingStatus(id, user, session string) (*PairingStatus, error) {
	p := &PairingStatus{}
	err := q.db.QueryRow(`SELECT id,status,expires_at,device_name FROM pairings WHERE id=? AND user_id=? AND session_id=?`, id, user, session).Scan(&p.ID, &p.Status, &p.ExpiresAt, &p.DeviceName)
	if err != nil {
		return nil, err
	}
	if p.Status == "pending" && !time.Now().Before(p.ExpiresAt) {
		p.Status = "expired"
	}
	return p, nil
}

func cancelPairing(tx *sql.Tx, id, user, session string) error {
	var status string
	err := tx.QueryRow(`UPDATE pairings SET status=CASE WHEN status='pending' THEN 'canceled' ELSE status END
 WHERE id=? AND user_id=? AND session_id=? RETURNING status`, id, user, session).Scan(&status)
	if err != nil {
		return err
	}
	if status == "connected" {
		return ErrPairingConnected
	}
	return nil
}

func (q *Queries) CancelPairing(id, user, session string) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := cancelPairing(tx, id, user, session); err != nil {
		return err
	}
	return tx.Commit()
}
func (q *Queries) RedeemPairing(hash, tokenHash []byte, s Session) (*User, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var user, parentSession string
	var expiry time.Time
	err = tx.QueryRow(`UPDATE pairings SET status='connected',device_name=? WHERE code_hash=? AND status='pending' RETURNING user_id,session_id,expires_at`, s.DeviceName, hash).Scan(&user, &parentSession, &expiry)
	if err != nil {
		return nil, err
	}
	if !time.Now().Before(expiry) {
		return nil, sql.ErrNoRows
	}
	var parentExpiry time.Time
	if err := tx.QueryRow(`SELECT expires_at FROM sessions WHERE id=?`, parentSession).Scan(&parentExpiry); err != nil {
		return nil, err
	}
	if !time.Now().Before(parentExpiry) {
		return nil, sql.ErrNoRows
	}
	u, err := scanUser(tx.QueryRow(`SELECT id,username,role,disabled,password_hash,must_change_password FROM users WHERE id=? AND disabled=0`, user))
	if err != nil {
		return nil, err
	}
	if u.Role == "admin" {
		return nil, ErrAdminTransfer
	}
	if u.MustChangePassword {
		return nil, ErrPasswordChangeRequired
	}
	_, err = tx.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES(?,?,?,?,?,?)`, s.ID, u.ID, tokenHash, s.DeviceName, s.CreatedAt.UTC(), s.ExpiresAt.UTC())
	if err != nil {
		return nil, err
	}
	return u, tx.Commit()
}
func (q *Queries) Owner(kind, id string) (string, error) {
	var owner sql.NullString
	query := `SELECT owner_id FROM transfers WHERE id=?`
	if kind == "slot" {
		query = `SELECT owner_id FROM slots WHERE id=?`
	}
	err := q.db.QueryRow(query, id).Scan(&owner)
	return owner.String, err
}
func (q *Queries) OwnedIDs(kind, user string) ([]string, error) {
	query := `SELECT id FROM transfers WHERE owner_id=? AND NOT EXISTS(SELECT 1 FROM slot_transfers WHERE transfer_id=transfers.id) ORDER BY created_at DESC`
	if kind == "slot" {
		query = `SELECT id FROM slots WHERE owner_id=? ORDER BY created_at DESC`
	}
	var rows *sql.Rows
	var err error
	if user == "" {
		query = strings.Replace(query, "owner_id=?", "1=1", 1)
		rows, err = q.db.Query(query)
	} else {
		rows, err = q.db.Query(query, user)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PruneAuthentication removes expired session and pairing credentials. Session
// foreign keys also invalidate outstanding pairing grants from those sessions.
func (q *Queries) PruneAuthentication() error {
	if _, err := q.db.Exec(`DELETE FROM admin_pending_factors WHERE expires_at<=?`, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := q.db.Exec(`DELETE FROM sessions WHERE julianday(substr(expires_at,1,19))<=julianday('now')`); err != nil {
		return err
	}
	// UTC timestamps include a Go zone suffix; SQLite parses the date/time prefix.
	// Retain terminal status briefly so an open pairing view can explain expiry.
	// Expired grants are never redeemable during this observation window.
	_, err := q.db.Exec(`DELETE FROM pairings WHERE julianday(substr(expires_at,1,19))<=julianday('now','-15 minutes')`)
	return err
}
