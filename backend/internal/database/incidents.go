package database

import (
	"database/sql"
	"errors"
	"strings"
)

type IncidentState struct {
	PublicTransfersPaused bool   `json:"public_transfers_paused"`
	UpdatedAt             string `json:"updated_at"`
}

var ErrTransfersPaused = errors.New("public transfers are paused")
var ErrAccountDisabled = errors.New("account is disabled")
var ErrResourceRevoked = errors.New("resource has been revoked")

func IncidentError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case strings.Contains(err.Error(), "incident:paused"):
		return ErrTransfersPaused
	case strings.Contains(err.Error(), "incident:account_disabled"):
		return ErrAccountDisabled
	case strings.Contains(err.Error(), "incident:revoked"):
		return ErrResourceRevoked
	default:
		return err
	}
}
func (q *Queries) IncidentState() (IncidentState, error) {
	var state IncidentState
	err := q.db.QueryRow(`SELECT public_transfers_paused,updated_at FROM incident_state WHERE id=1`).Scan(&state.PublicTransfersPaused, &state.UpdatedAt)
	return state, err
}
func (q *Queries) SetTransfersPaused(paused bool) error {
	result, err := q.db.Exec(`UPDATE incident_state SET public_transfers_paused=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=1`, paused)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

type AccountShutdown struct {
	User             *User `json:"user"`
	RevokedSessions  int64 `json:"revoked_sessions"`
	RevokedPairings  int64 `json:"revoked_pairings"`
	RevokedTransfers int64 `json:"revoked_transfers"`
	RevokedSlots     int64 `json:"revoked_slots"`
	CleanupPending   bool  `json:"cleanup_pending"`
}

// ShutdownAccount persists denial before any asynchronous IO cancellation or
// disk cleanup. Re-enabling a login never reverses the resource revocations.
func (q *Queries) ShutdownAccount(id string) (AccountShutdown, error) {
	result := AccountShutdown{CleanupPending: true}
	tx, err := q.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE users SET disabled=disabled WHERE id=?`, id)
	if err != nil {
		return result, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return result, err
	}
	if n == 0 {
		return result, sql.ErrNoRows
	}
	var role string
	var otherAdmins int
	if err := tx.QueryRow(`SELECT role FROM users WHERE id=?`, id).Scan(&role); err != nil {
		return result, err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0 AND id!=?`, id).Scan(&otherAdmins); err != nil {
		return result, err
	}
	if role == "admin" && otherAdmins == 0 {
		return result, ErrLastAdmin
	}
	if _, err := tx.Exec(`UPDATE users SET disabled=1 WHERE id=?`, id); err != nil {
		return result, err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, id).Scan(&result.RevokedSessions); err != nil {
		return result, err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pairings WHERE user_id=?`, id).Scan(&result.RevokedPairings); err != nil {
		return result, err
	}
	// Count pairings before session deletion cascades outstanding pairing grants.
	if _, err := tx.Exec(`DELETE FROM pairings WHERE user_id=?`, id); err != nil {
		return result, err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, id); err != nil {
		return result, err
	}
	res, err = tx.Exec(`UPDATE transfers SET status='revoked' WHERE status!='revoked' AND (owner_id=? OR id IN (SELECT st.transfer_id FROM slot_transfers st JOIN slots s ON s.id=st.slot_id WHERE s.owner_id=?))`, id, id)
	if err != nil {
		return result, err
	}
	result.RevokedTransfers, err = res.RowsAffected()
	if err != nil {
		return result, err
	}
	res, err = tx.Exec(`UPDATE slots SET status='revoked' WHERE owner_id=? AND status!='revoked'`, id)
	if err != nil {
		return result, err
	}
	result.RevokedSlots, err = res.RowsAffected()
	if err != nil {
		return result, err
	}
	result.User, err = scanUser(tx.QueryRow(`SELECT id,username,role,disabled,password_hash,must_change_password FROM users WHERE id=?`, id))
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// StreamNamespace separates independent servers/databases in the same process.
func (q *Queries) StreamNamespace() string { return q.incidentNamespace }

func incidentMigration() string {
	sql := `CREATE TABLE incident_state(id INTEGER PRIMARY KEY CHECK(id=1),public_transfers_paused INTEGER NOT NULL DEFAULT 0 CHECK(public_transfers_paused IN (0,1)),updated_at TEXT NOT NULL);
 INSERT INTO incident_state(id,updated_at) VALUES(1,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
 CREATE INDEX transfers_revoked_cleanup ON transfers(status,id);
 CREATE INDEX slots_revoked_cleanup ON slots(status,id);
 `
	for _, table := range []string{"transfers", "slots", "files", "manifests"} {
		sql += `CREATE TRIGGER incident_create_` + table + ` BEFORE INSERT ON ` + table + ` BEGIN SELECT CASE WHEN (SELECT public_transfers_paused FROM incident_state)=1 THEN RAISE(ABORT,'incident:paused') END; END;`
	}
	for _, table := range []string{"transfers", "slots"} {
		sql += `CREATE TRIGGER incident_owner_` + table + ` BEFORE INSERT ON ` + table + ` BEGIN SELECT CASE WHEN EXISTS(SELECT 1 FROM users WHERE id=NEW.owner_id AND disabled=1) THEN RAISE(ABORT,'incident:account_disabled') END; END;`
	}
	for _, table := range []string{"files", "manifests"} {
		sql += `CREATE TRIGGER incident_parent_` + table + ` BEFORE INSERT ON ` + table + ` BEGIN SELECT CASE WHEN EXISTS(SELECT 1 FROM transfers WHERE id=NEW.transfer_id AND status='revoked') THEN RAISE(ABORT,'incident:revoked') END; END;`
	}
	sql += `CREATE TRIGGER incident_manifest_update BEFORE UPDATE OF data ON manifests BEGIN
 SELECT CASE WHEN (SELECT public_transfers_paused FROM incident_state)=1 THEN RAISE(ABORT,'incident:paused') END;
 SELECT CASE WHEN EXISTS(SELECT 1 FROM transfers WHERE id=NEW.transfer_id AND status='revoked') THEN RAISE(ABORT,'incident:revoked') END;
 END;
 CREATE TRIGGER incident_complete BEFORE UPDATE OF status ON transfers WHEN NEW.status='complete' AND OLD.status!='complete' BEGIN
 SELECT CASE WHEN (SELECT public_transfers_paused FROM incident_state)=1 THEN RAISE(ABORT,'incident:paused') END;
 END;`
	return sql
}

func (q *Queries) RevokedResourceBatch(kind, after string, limit int) ([]string, error) {
	table := "transfers"
	if kind == "slot" {
		table = "slots"
	}
	if limit < 1 || limit > 100 {
		limit = 16
	}
	rows, err := q.db.Query(`SELECT id FROM `+table+` WHERE status='revoked' AND id>? ORDER BY id LIMIT ?`, after, limit)
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
func (q *Queries) SlotHasTransfers(id string) (bool, error) {
	var exists bool
	err := q.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM slot_transfers WHERE slot_id=?)`, id).Scan(&exists)
	return exists, err
}

// ValidateIncidentSchema recognizes a fully migrated server database without
// creating or upgrading anything. Recovery commands must not initialize the
// wrong file and then falsely report that the running service is paused.
func (q *Queries) ValidateIncidentSchema() error {
	var tables, versions, maxVersion, states int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('users','sessions','pairings','transfers','files','manifests','slots','slot_transfers','schema_migrations','incident_state')`).Scan(&tables); err != nil {
		return err
	}
	if tables != 10 {
		return errors.New("unsupported server schema")
	}
	if err := q.db.QueryRow(`SELECT COUNT(*),COALESCE(MAX(version),0) FROM schema_migrations WHERE version>0`).Scan(&versions, &maxVersion); err != nil {
		return err
	}
	if versions != len(migrations)-1 || maxVersion != len(migrations)-1 {
		return errors.New("unsupported server schema version")
	}
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM incident_state`).Scan(&states); err != nil {
		return err
	}
	if states != 1 {
		return errors.New("invalid incident control singleton")
	}
	_, err := q.IncidentState()
	return err
}
