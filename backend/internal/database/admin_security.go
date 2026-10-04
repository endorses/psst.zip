package database

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
)

const AdminRecentDuration = 5 * time.Minute
const AdminEnrollmentDuration = 5 * time.Minute
const adminMaximumFailures = 5

var ErrAdminFactorRequired = errors.New("administrator factor required")
var ErrAdminFactorInvalid = errors.New("administrator factor invalid")
var ErrAdminAuthenticationChanged = errors.New("administrator authentication changed")
var ErrAdminRecentRequired = errors.New("recent authentication required")
var ErrAdminFactorEnabled = errors.New("administrator factor already enabled")
var ErrAdminFactorDisabled = errors.New("administrator factor is not enabled")
var ErrAdminEnrollmentPending = errors.New("administrator enrollment already pending")
var ErrAdminEnrollmentExpired = errors.New("administrator enrollment expired")

type AdminAuthenticationLocked struct{ RetryAt time.Time }

func (e *AdminAuthenticationLocked) Error() string {
	return "administrator authentication temporarily locked"
}

func adminSecurityMigration() string {
	return `
 ALTER TABLE sessions ADD COLUMN recent_until INTEGER NOT NULL DEFAULT 0;
 ALTER TABLE sessions ADD COLUMN security_revision INTEGER NOT NULL DEFAULT 0;
 CREATE TABLE admin_security(user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,secret TEXT NOT NULL DEFAULT '',last_counter INTEGER NOT NULL DEFAULT -1 CHECK(last_counter>=-1),revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),failures INTEGER NOT NULL DEFAULT 0 CHECK(failures BETWEEN 0 AND 5),locked_until INTEGER NOT NULL DEFAULT 0 CHECK(locked_until>=0));
 INSERT INTO admin_security(user_id) SELECT id FROM users WHERE role='admin';
 CREATE TRIGGER initialize_admin_security AFTER INSERT ON users WHEN NEW.role='admin' BEGIN INSERT INTO admin_security(user_id) VALUES(NEW.id); END;
 CREATE TRIGGER initialize_promoted_admin_security AFTER UPDATE OF role ON users WHEN NEW.role='admin' AND OLD.role!='admin' BEGIN INSERT INTO admin_security(user_id) VALUES(NEW.id) ON CONFLICT(user_id) DO NOTHING; END;
 CREATE TABLE admin_pending_factors(user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id) ON DELETE CASCADE,secret TEXT NOT NULL,expires_at INTEGER NOT NULL,revision INTEGER NOT NULL,attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5));
 CREATE TABLE admin_recovery_codes(user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,hash BLOB NOT NULL CHECK(length(hash)=32),PRIMARY KEY(user_id,hash));
 CREATE TRIGGER bound_admin_recovery_codes BEFORE INSERT ON admin_recovery_codes WHEN (SELECT COUNT(*) FROM admin_recovery_codes WHERE user_id=NEW.user_id)>=10 BEGIN SELECT RAISE(ABORT,'recovery code capacity'); END;
`
}

type AdminSecurityState struct {
	Secret      string
	LastCounter int64
	Revision    int64
	Failures    int
	LockedUntil int64
}

func readAdminSecurity(q trafficQuerier, user string) (AdminSecurityState, error) {
	var s AdminSecurityState
	err := q.QueryRow(`SELECT a.secret,a.last_counter,a.revision,a.failures,a.locked_until FROM admin_security a JOIN users u ON u.id=a.user_id WHERE a.user_id=? AND u.role='admin'`, user).Scan(&s.Secret, &s.LastCounter, &s.Revision, &s.Failures, &s.LockedUntil)
	return s, err
}
func (q *Queries) AdminSecurity(user string) (AdminSecurityState, error) {
	return readAdminSecurity(q.db, user)
}
func lockAdmin(tx *sql.Tx, user string, hash []byte, revision int64) (AdminSecurityState, error) {
	if _, err := tx.Exec(`UPDATE users SET disabled=disabled WHERE id=?`, user); err != nil {
		return AdminSecurityState{}, err
	}
	var current []byte
	if err := tx.QueryRow(`SELECT password_hash FROM users WHERE id=? AND role='admin' AND disabled=0`, user).Scan(&current); err != nil {
		return AdminSecurityState{}, ErrAdminAuthenticationChanged
	}
	if !bytes.Equal(current, hash) {
		return AdminSecurityState{}, ErrAdminAuthenticationChanged
	}
	s, err := readAdminSecurity(tx, user)
	if err != nil {
		return s, err
	}
	if s.Revision != revision {
		return s, ErrAdminAuthenticationChanged
	}
	return s, nil
}
func checkAdminLock(tx *sql.Tx, user string, s *AdminSecurityState, now time.Time) error {
	if s.LockedUntil > now.Unix() {
		return &AdminAuthenticationLocked{time.Unix(s.LockedUntil, 0).UTC()}
	}
	if s.LockedUntil != 0 {
		if _, err := tx.Exec(`UPDATE admin_security SET failures=0,locked_until=0 WHERE user_id=?`, user); err != nil {
			return err
		}
		s.Failures = 0
		s.LockedUntil = 0
	}
	return nil
}
func failAdminProof(tx *sql.Tx, user string, s AdminSecurityState, now time.Time, reason error) error {
	failures := min(adminMaximumFailures, s.Failures+1)
	until := int64(0)
	if failures >= adminMaximumFailures {
		until = now.Add(5 * time.Minute).Unix()
		reason = &AdminAuthenticationLocked{time.Unix(until, 0).UTC()}
	}
	if _, err := tx.Exec(`UPDATE admin_security SET failures=?,locked_until=? WHERE user_id=?`, failures, until, user); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return reason
}
func (q *Queries) RecordAdminAuthenticationFailure(user string, hash []byte, revision int64, now time.Time) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, err := lockAdmin(tx, user, hash, revision)
	if err != nil {
		return err
	}
	if err = checkAdminLock(tx, user, &s, now); err != nil {
		return err
	}
	return failAdminProof(tx, user, s, now, ErrAdminFactorInvalid)
}

// Validation delegates RFC 4226 computation to the pinned OTP library. The
// accepted counter, not just a boolean, is committed atomically with the proof.
func adminTOTPCounter(secret, code string, now time.Time, last int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	current := now.Unix() / 30
	for _, counter := range []int64{current, current - 1, current + 1} {
		if counter < 0 || counter <= last {
			continue
		}
		ok, err := hotp.ValidateCustom(code, uint64(counter), secret, hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err == nil && ok {
			return counter, true
		}
	}
	return 0, false
}
func verifyAdminFactor(tx *sql.Tx, user string, s AdminSecurityState, code, recovery string, now time.Time) error {
	if s.Secret == "" {
		if code != "" || recovery != "" {
			return failAdminProof(tx, user, s, now, ErrAdminFactorInvalid)
		}
		return nil
	}
	if code == "" && recovery == "" {
		return ErrAdminFactorRequired
	}
	if code != "" && recovery != "" {
		return failAdminProof(tx, user, s, now, ErrAdminFactorInvalid)
	}
	if recovery != "" {
		sum := sha256.Sum256([]byte(recovery))
		res, err := tx.Exec(`DELETE FROM admin_recovery_codes WHERE user_id=? AND hash=?`, user, sum[:])
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 1 {
			return nil
		}
		return failAdminProof(tx, user, s, now, ErrAdminFactorInvalid)
	}
	counter, valid := adminTOTPCounter(s.Secret, code, now, s.LastCounter)
	if !valid {
		return failAdminProof(tx, user, s, now, ErrAdminFactorInvalid)
	}
	_, err := tx.Exec(`UPDATE admin_security SET last_counter=? WHERE user_id=?`, counter, user)
	return err
}
func (q *Queries) CreateAdminSession(s Session, tokenHash, passwordHash []byte, revision int64, code, recovery string, now time.Time) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	security, err := lockAdmin(tx, s.UserID, passwordHash, revision)
	if err != nil {
		return err
	}
	if err = checkAdminLock(tx, s.UserID, &security, now); err != nil {
		return err
	}
	if err = verifyAdminFactor(tx, s.UserID, security, code, recovery, now); err != nil {
		return err
	}
	if err = reserveAuthSession(tx, s.UserID, "", now); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO sessions(id,user_id,token_hash,device_name,created_at,expires_at,recent_until,security_revision) VALUES(?,?,?,?,?,?,?,?)`, s.ID, s.UserID, tokenHash, s.DeviceName, s.CreatedAt.UTC(), s.ExpiresAt.UTC(), now.Add(AdminRecentDuration).Unix(), revision)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE admin_security SET failures=0,locked_until=0 WHERE user_id=?`, s.UserID); err != nil {
		return err
	}
	return tx.Commit()
}
func activeAdminSession(q trafficQuerier, user, session string, now time.Time) (int64, int64, error) {
	var expires time.Time
	var until, revision int64
	err := q.QueryRow(`SELECT s.expires_at,s.recent_until,s.security_revision FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id=? AND s.user_id=? AND u.role='admin' AND u.disabled=0`, session, user).Scan(&expires, &until, &revision)
	if err != nil || !now.Before(expires) {
		return 0, 0, ErrAdminAuthenticationChanged
	}
	return until, revision, nil
}
func requireRecentAdmin(q trafficQuerier, user, session string, now time.Time) error {
	until, revision, err := activeAdminSession(q, user, session, now)
	if err != nil {
		return err
	}
	security, err := readAdminSecurity(q, user)
	if err != nil {
		return err
	}
	if now.Unix() >= until || revision != security.Revision {
		return ErrAdminRecentRequired
	}
	return nil
}
func (q *Queries) RequireRecentAdmin(user, session string, now time.Time) error {
	return requireRecentAdmin(q.db, user, session, now)
}
func (q *Queries) ReauthenticateAdmin(user, session string, passwordHash []byte, revision int64, code, recovery string, now time.Time) (time.Time, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	s, err := lockAdmin(tx, user, passwordHash, revision)
	if err != nil {
		return time.Time{}, err
	}
	if _, _, err = activeAdminSession(tx, user, session, now); err != nil {
		return time.Time{}, err
	}
	if err = checkAdminLock(tx, user, &s, now); err != nil {
		return time.Time{}, err
	}
	if err = verifyAdminFactor(tx, user, s, code, recovery, now); err != nil {
		return time.Time{}, err
	}
	until := now.UTC().Add(AdminRecentDuration).Truncate(time.Second)
	if _, err = tx.Exec(`UPDATE sessions SET recent_until=?,security_revision=? WHERE id=? AND user_id=?`, until.Unix(), revision, session, user); err != nil {
		return time.Time{}, err
	}
	if _, err = tx.Exec(`UPDATE admin_security SET failures=0,locked_until=0 WHERE user_id=?`, user); err != nil {
		return time.Time{}, err
	}
	return until, tx.Commit()
}

type AdminSecurityMetadata struct {
	Enabled                bool       `json:"enabled"`
	RecoveryCodesRemaining int        `json:"recovery_codes_remaining"`
	RecentUntil            *time.Time `json:"recent_until"`
}

func (q *Queries) AdminSecurityMetadata(user, session string, now time.Time) (AdminSecurityMetadata, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return AdminSecurityMetadata{}, err
	}
	defer tx.Rollback()
	s, err := readAdminSecurity(tx, user)
	if err != nil {
		return AdminSecurityMetadata{}, err
	}
	until, revision, err := activeAdminSession(tx, user, session, now)
	if err != nil {
		return AdminSecurityMetadata{}, err
	}
	out := AdminSecurityMetadata{Enabled: s.Secret != ""}
	if until > 0 && revision == s.Revision {
		at := time.Unix(until, 0).UTC()
		out.RecentUntil = &at
	}
	err = tx.QueryRow(`SELECT COUNT(*) FROM admin_recovery_codes WHERE user_id=?`, user).Scan(&out.RecoveryCodesRemaining)
	return out, err
}
func (q *Queries) BeginAdminEnrollment(user, session string, passwordHash []byte, revision int64, secret string, now time.Time) (time.Time, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	s, err := lockAdmin(tx, user, passwordHash, revision)
	if err != nil {
		return time.Time{}, err
	}
	if err = requireRecentAdmin(tx, user, session, now); err != nil {
		return time.Time{}, err
	}
	if s.Secret != "" {
		return time.Time{}, ErrAdminFactorEnabled
	}
	if err = checkAdminLock(tx, user, &s, now); err != nil {
		return time.Time{}, err
	}
	var exists int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM admin_pending_factors WHERE user_id=? AND expires_at>?`, user, now.Unix()).Scan(&exists); err != nil {
		return time.Time{}, err
	}
	if exists != 0 {
		return time.Time{}, ErrAdminEnrollmentPending
	}
	expires := now.UTC().Add(AdminEnrollmentDuration).Truncate(time.Second)
	_, err = tx.Exec(`INSERT INTO admin_pending_factors(user_id,session_id,secret,expires_at,revision,attempts) VALUES(?,?,?,?,?,0) ON CONFLICT(user_id) DO UPDATE SET session_id=excluded.session_id,secret=excluded.secret,expires_at=excluded.expires_at,revision=excluded.revision,attempts=0`, user, session, secret, expires.Unix(), revision)
	if err != nil {
		return time.Time{}, err
	}
	return expires, tx.Commit()
}
func (q *Queries) CancelAdminEnrollment(user, session string) error {
	_, err := q.db.Exec(`DELETE FROM admin_pending_factors WHERE user_id=? AND session_id=?`, user, session)
	return err
}
func newAdminRecoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, 10)
	hashes := make([][]byte, 10)
	for i := range codes {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, nil, err
		}
		codes[i] = base64.RawURLEncoding.EncodeToString(random[:])
		sum := sha256.Sum256([]byte(codes[i]))
		hashes[i] = sum[:]
	}
	return codes, hashes, nil
}
func replaceAdminRecovery(tx *sql.Tx, user string, hashes [][]byte) error {
	if _, err := tx.Exec(`DELETE FROM admin_recovery_codes WHERE user_id=?`, user); err != nil {
		return err
	}
	for _, hash := range hashes {
		if _, err := tx.Exec(`INSERT INTO admin_recovery_codes(user_id,hash) VALUES(?,?)`, user, hash); err != nil {
			return err
		}
	}
	return nil
}
func revokeAdminCredentials(tx *sql.Tx, user string) error {
	for _, table := range []string{"admin_pending_factors", "pairings", "sessions"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE user_id=?`, user); err != nil {
			return err
		}
	}
	return nil
}
func (q *Queries) ConfirmAdminEnrollment(user, session string, passwordHash []byte, revision int64, code string, now time.Time) ([]string, error) {
	codes, hashes, err := newAdminRecoveryCodes()
	if err != nil {
		return nil, err
	}
	tx, err := q.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	s, err := lockAdmin(tx, user, passwordHash, revision)
	if err != nil {
		return nil, err
	}
	if err = requireRecentAdmin(tx, user, session, now); err != nil {
		return nil, err
	}
	if s.Secret != "" {
		return nil, ErrAdminFactorEnabled
	}
	if err = checkAdminLock(tx, user, &s, now); err != nil {
		return nil, err
	}
	var secret string
	var expires, pendingRevision int64
	var attempts int
	err = tx.QueryRow(`SELECT secret,expires_at,revision,attempts FROM admin_pending_factors WHERE user_id=? AND session_id=?`, user, session).Scan(&secret, &expires, &pendingRevision, &attempts)
	if err == sql.ErrNoRows {
		return nil, ErrAdminEnrollmentExpired
	}
	if err != nil {
		return nil, err
	}
	if expires <= now.Unix() || pendingRevision != revision || attempts >= adminMaximumFailures {
		if _, err = tx.Exec(`DELETE FROM admin_pending_factors WHERE user_id=?`, user); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, ErrAdminEnrollmentExpired
	}
	counter, valid := adminTOTPCounter(secret, code, now, -1)
	if !valid {
		if attempts+1 >= adminMaximumFailures {
			_, err = tx.Exec(`DELETE FROM admin_pending_factors WHERE user_id=?`, user)
		} else {
			_, err = tx.Exec(`UPDATE admin_pending_factors SET attempts=attempts+1 WHERE user_id=?`, user)
		}
		if err != nil {
			return nil, err
		}
		return nil, failAdminProof(tx, user, s, now, ErrAdminFactorInvalid)
	}
	if _, err = tx.Exec(`UPDATE admin_security SET secret=?,last_counter=?,revision=revision+1,failures=0,locked_until=0 WHERE user_id=?`, secret, counter, user); err != nil {
		return nil, err
	}
	if err = replaceAdminRecovery(tx, user, hashes); err != nil {
		return nil, err
	}
	if err = revokeAdminCredentials(tx, user); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}
func (q *Queries) ChangeAdminFactor(user, session string, passwordHash []byte, revision int64, disable bool, now time.Time) ([]string, error) {
	var codes []string
	var hashes [][]byte
	var err error
	if !disable {
		codes, hashes, err = newAdminRecoveryCodes()
		if err != nil {
			return nil, err
		}
	}
	tx, err := q.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	s, err := lockAdmin(tx, user, passwordHash, revision)
	if err != nil {
		return nil, err
	}
	if err = requireRecentAdmin(tx, user, session, now); err != nil {
		return nil, err
	}
	if s.Secret == "" {
		return nil, ErrAdminFactorDisabled
	}
	if disable {
		_, err = tx.Exec(`UPDATE admin_security SET secret='',last_counter=-1,revision=revision+1,failures=0,locked_until=0 WHERE user_id=?`, user)
	} else {
		_, err = tx.Exec(`UPDATE admin_security SET revision=revision+1,failures=0,locked_until=0 WHERE user_id=?`, user)
	}
	if err != nil {
		return nil, err
	}
	if err = replaceAdminRecovery(tx, user, hashes); err != nil {
		return nil, err
	}
	if err = revokeAdminCredentials(tx, user); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}

// ResetAdminFactor is the local-operator escape hatch; it never creates users or
// alters the password, role, disabled flag, links, or emergency pause state.
func (q *Queries) ResetAdminFactor(username string) error {
	if username == "" || strings.TrimSpace(username) != username {
		return errors.New("exact administrator username required")
	}
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE users SET disabled=disabled WHERE username=? COLLATE BINARY`, username); err != nil {
		return err
	}
	var user string
	if err = tx.QueryRow(`SELECT id FROM users WHERE username=? COLLATE BINARY AND role='admin'`, username).Scan(&user); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE admin_security SET secret='',last_counter=-1,revision=revision+1,failures=0,locked_until=0 WHERE user_id=?`, user)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("unsupported administrator security state")
	}
	if err = replaceAdminRecovery(tx, user, nil); err != nil {
		return err
	}
	if err = revokeAdminCredentials(tx, user); err != nil {
		return err
	}
	return tx.Commit()
}

// AdminActor binds an HTTP mutation to its currently valid authenticated session.
// Internal cleanup/bootstrap/local-operator paths pass nil explicitly or omit it.
type AdminActor struct {
	UserID    string
	SessionID string
}

func ValidateAdminActor(tx *sql.Tx, actor *AdminActor) error {
	if actor == nil {
		return nil
	}
	// Take the writer lock before reading session/factor state. Factor changes,
	// password resets and this mutation therefore have a single commit order.
	if _, err := tx.Exec(`UPDATE users SET disabled=disabled WHERE id=?`, actor.UserID); err != nil {
		return err
	}
	return requireRecentAdmin(tx, actor.UserID, actor.SessionID, time.Now())
}
func optionalAdminActor(actors []*AdminActor) *AdminActor {
	if len(actors) > 0 {
		return actors[0]
	}
	return nil
}
