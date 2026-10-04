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
	// Serialize the capacity check with every account creator, including bootstrap.
	if _, err = tx.Exec(`UPDATE auth_metadata_cleanup SET user_cursor=user_cursor WHERE id=1`); err != nil {
		return err
	}
	full, err := authCountAtLeast(tx, "users", "", MaxAuthUsers)
	if err != nil {
		return err
	}
	if full && !bootstrap {
		return ErrAccountCapacity
	}
	query := `INSERT INTO users (id,username,role,password_hash,must_change_password) VALUES (?,?,?,?,?)`
	if bootstrap {
		query = `INSERT INTO users (id,username,role,password_hash,must_change_password) SELECT ?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM users)`
	}
	result, err := tx.Exec(query, u.ID, u.Username, u.Role, u.PasswordHash, u.MustChangePassword && u.Role != "admin")
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n > 0 && (bootstrap || optionalAdminActor(actors) != nil) {
		event := SecurityEvent{Kind: "account.created", Origin: "system", TargetType: "user", TargetID: u.ID, Outcome: "succeeded"}
		if actor := optionalAdminActor(actors); actor != nil {
			event.Origin, event.ActorID = "administrator", actor.UserID
		}
		if err = q.AppendSecurityEvent(tx, event); err != nil {
			return err
		}
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
	var previouslyDisabled bool
	if disabled != nil {
		if err := tx.QueryRow(`SELECT disabled FROM users WHERE id=?`, id).Scan(&previouslyDisabled); err != nil {
			return err
		}
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
	// Internal callers do not acquire an administrator identity by omission.
	origin, actorID := "", ""
	if actor != nil {
		origin, actorID = "administrator", actor.UserID
	} else if expectedHash != nil {
		origin, actorID = "account", id
		if adminSession != "" {
			origin = "administrator"
		}
	}
	if origin != "" {
		if disabled != nil && *disabled != previouslyDisabled {
			kind := "account.enabled"
			if *disabled {
				kind = "account.disabled"
			}
			event := SecurityEvent{Kind: kind, Origin: origin, ActorID: actorID, TargetType: "user", TargetID: id, Outcome: "succeeded"}
			if *disabled {
				err = q.AppendRecoverySecurityEvent(tx, event)
			} else {
				err = q.AppendSecurityEvent(tx, event)
			}
			if err != nil {
				return err
			}
		}
		if len(password) > 0 {
			kind := "account.password_reset"
			if expectedHash != nil {
				kind = "account.password_changed"
			}
			if err = q.AppendSecurityEvent(tx, SecurityEvent{Kind: kind, Origin: origin, ActorID: actorID, TargetType: "user", TargetID: id, Outcome: "succeeded"}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (q *Queries) CreateSession(s Session, hash []byte, passwordHash []byte) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE users SET disabled=disabled WHERE id=? AND disabled=0 AND password_hash=? AND NOT EXISTS(SELECT 1 FROM admin_security a WHERE a.user_id=users.id AND a.secret!='')`, s.UserID, passwordHash)
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
	if err = reserveAuthSession(tx, s.UserID, "", time.Now()); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES(?,?,?,?,?,?)`, s.ID, s.UserID, hash, s.DeviceName, s.CreatedAt.UTC(), s.ExpiresAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
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
	list, err := q.AuthenticationSessions(user, "")
	return list.Sessions, err
}
func (q *Queries) DeleteSession(id, user string, actors ...*AdminActor) error {
	return q.deleteSession(id, user, false, actors)
}

// DeleteAccountSession identifies an authenticated owner action explicitly;
// internal DeleteSession callers do not inherit an account audit identity.
func (q *Queries) DeleteAccountSession(id, user string, actors ...*AdminActor) error {
	return q.deleteSession(id, user, true, actors)
}

func (q *Queries) deleteSession(id, user string, accountAction bool, actors []*AdminActor) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = ValidateAdminActor(tx, optionalAdminActor(actors)); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM sessions WHERE id=? AND user_id=?`, id, user)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n > 0 && (accountAction || optionalAdminActor(actors) != nil) {
		var role string
		if err = tx.QueryRow(`SELECT role FROM users WHERE id=?`, user).Scan(&role); err != nil {
			return err
		}
		event := SecurityEvent{Kind: "session.revoked", Origin: "account", ActorID: user, TargetType: "session", TargetID: id, Outcome: "succeeded"}
		if role == "admin" {
			event.Origin = "administrator"
		}
		if actor := optionalAdminActor(actors); actor != nil {
			event.Origin, event.ActorID = "administrator", actor.UserID
		}
		if err = q.AppendRecoverySecurityEvent(tx, event); err != nil {
			return err
		}
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
	if _, err = tx.Exec(`UPDATE users SET disabled=disabled WHERE id=?`, user); err != nil {
		return err
	}
	if err = pruneUserAuth(tx, user, time.Now()); err != nil {
		return err
	}
	if replaceID != "" {
		var previous string
		if err := tx.QueryRow(`SELECT status FROM pairings WHERE id=? AND user_id=? AND session_id=?`, replaceID, user, session).Scan(&previous); err != nil {
			return err
		}
		if err := cancelPairing(tx, replaceID, user, session); err != nil {
			return err
		}
		if previous == "pending" {
			if err = q.AppendSecurityEvent(tx, SecurityEvent{Kind: "pairing.revoked", Origin: "account", ActorID: user, TargetType: "pairing", TargetID: replaceID, Outcome: "succeeded"}); err != nil {
				return err
			}
		}
	}
	full, err := authCountAtLeast(tx, "pairings", user, MaxAuthPairingsPerUser)
	if err != nil {
		return err
	}
	if full {
		return ErrPairingCapacity
	}
	var pending int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM pairings WHERE user_id=? AND status='pending' AND substr(expires_at,1,19)>=? LIMIT ?)`, user, authTimePrefix(time.Now()), MaxAuthPendingPairingsPerUser).Scan(&pending); err != nil {
		return err
	}
	if pending >= MaxAuthPendingPairingsPerUser {
		return ErrPairingCapacity
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
	if err = q.AppendSecurityEvent(tx, SecurityEvent{Kind: "pairing.created", Origin: "account", ActorID: user, TargetType: "pairing", TargetID: id, Outcome: "succeeded"}); err != nil {
		return err
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
	if _, err = tx.Exec(`UPDATE users SET disabled=disabled WHERE id=?`, user); err != nil {
		return err
	}
	var previous string
	if err = tx.QueryRow(`SELECT status FROM pairings WHERE id=? AND user_id=? AND session_id=?`, id, user, session).Scan(&previous); err != nil {
		return err
	}
	if err := cancelPairing(tx, id, user, session); err != nil {
		return err
	}
	if previous == "pending" {
		if err = q.AppendRecoverySecurityEvent(tx, SecurityEvent{Kind: "pairing.revoked", Origin: "account", ActorID: user, TargetType: "pairing", TargetID: id, Outcome: "succeeded"}); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (q *Queries) RedeemPairing(hash, tokenHash []byte, s Session) (*User, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var user, parentSession, pairingID string
	var expiry time.Time
	err = tx.QueryRow(`UPDATE pairings SET status='connected',device_name=? WHERE code_hash=? AND status='pending' RETURNING user_id,session_id,expires_at,id`, s.DeviceName, hash).Scan(&user, &parentSession, &expiry, &pairingID)
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
	if err = reserveAuthSession(tx, user, parentSession, time.Now()); err != nil {
		return nil, err
	}
	_, err = tx.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at) VALUES(?,?,?,?,?,?)`, s.ID, u.ID, tokenHash, s.DeviceName, s.CreatedAt.UTC(), s.ExpiresAt.UTC())
	if err != nil {
		return nil, err
	}
	if err = q.AppendSecurityEvent(tx, SecurityEvent{Kind: "pairing.redeemed", Origin: "account", ActorID: user, TargetType: "pairing", TargetID: pairingID, Outcome: "succeeded"}); err != nil {
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
