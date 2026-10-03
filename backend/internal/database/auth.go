package database

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	Disabled     bool   `json:"disabled"`
	PasswordHash []byte `json:"-"`
}
type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"-"`
	DeviceName string    `json:"device_name"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

var ErrLastAdmin = errors.New("cannot disable the last enabled administrator")

func (q *Queries) UserCount() (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}
func (q *Queries) CreateUser(u User, bootstrap bool) error {
	query := `INSERT INTO users (id,username,role,password_hash) VALUES (?,?,?,?)`
	if bootstrap {
		query = `INSERT INTO users (id,username,role,password_hash) SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM users)`
	}
	_, err := q.db.Exec(query, u.ID, u.Username, u.Role, u.PasswordHash)
	return err
}
func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.PasswordHash)
	return u, err
}
func (q *Queries) UserByName(name string) (*User, error) {
	return scanUser(q.db.QueryRow(`SELECT id,username,role,disabled,password_hash FROM users WHERE username=? COLLATE NOCASE`, name))
}
func (q *Queries) UserByID(id string) (*User, error) {
	return scanUser(q.db.QueryRow(`SELECT id,username,role,disabled,password_hash FROM users WHERE id=?`, id))
}
func (q *Queries) Users() ([]User, error) {
	rows, err := q.db.Query(`SELECT id,username,role,disabled,password_hash FROM users ORDER BY username`)
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
func (q *Queries) UpdateUser(id string, disabled *bool, password []byte) error {
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
		if _, err := tx.Exec(`UPDATE users SET password_hash=? WHERE id=?`, password, id); err != nil {
			return err
		}
	}
	if len(password) > 0 || (disabled != nil && *disabled) {
		for _, table := range []string{"sessions", "pairings"} {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE user_id=?`, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (q *Queries) CreateSession(s Session, hash []byte, passwordHash []byte) error {
	res, err := q.db.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) SELECT ?,id,?,?,?,? FROM users WHERE id=? AND disabled=0 AND password_hash=?`, s.ID, hash, s.DeviceName, s.CreatedAt.UTC(), s.ExpiresAt.UTC(), s.UserID, passwordHash)
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
func (q *Queries) DeleteSession(id, user string) error {
	_, err := q.db.Exec(`DELETE FROM sessions WHERE id=? AND user_id=?`, id, user)
	return err
}
func (q *Queries) CreatePairing(hash []byte, user, session string, expiry time.Time) error {
	_, err := q.db.Exec(`INSERT INTO pairings(code_hash,user_id,session_id,expires_at) SELECT ?,user_id,id,? FROM sessions WHERE id=? AND user_id=?`, hash, expiry.UTC(), session, user)
	return err
}
func (q *Queries) RedeemPairing(hash, tokenHash []byte, s Session) (*User, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var user, parentSession string
	var expiry time.Time
	err = tx.QueryRow(`DELETE FROM pairings WHERE code_hash=? RETURNING user_id,session_id,expires_at`, hash).Scan(&user, &parentSession, &expiry)
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
	u, err := scanUser(tx.QueryRow(`SELECT id,username,role,disabled,password_hash FROM users WHERE id=? AND disabled=0`, user))
	if err != nil {
		return nil, err
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
	for _, table := range []string{"sessions", "pairings"} {
		if _, err := q.db.Exec(`DELETE FROM ` + table + ` WHERE julianday(expires_at)<=julianday('now')`); err != nil {
			return err
		}
	}
	return nil
}
