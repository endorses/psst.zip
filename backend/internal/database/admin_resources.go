package database

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"
)

// Current counters are derived transactionally; lifetime inbox allowances remain
// separate. Parent updates use the reverse membership index, not a table scan.
func adminResourcesMigration() string {
	s := `CREATE INDEX slot_transfers_transfer ON slot_transfers(transfer_id,slot_id);
 UPDATE transfers SET owner_id=(SELECT s.owner_id FROM slot_transfers st JOIN slots s ON s.id=st.slot_id WHERE st.transfer_id=transfers.id AND s.owner_id IS NOT NULL ORDER BY st.slot_id LIMIT 1) WHERE owner_id IS NULL;
 CREATE TRIGGER admin_resource_inherit_owner AFTER INSERT ON slot_transfers BEGIN UPDATE transfers SET owner_id=(SELECT owner_id FROM slots WHERE id=NEW.slot_id) WHERE id=NEW.transfer_id AND owner_id IS NULL; END;
 CREATE INDEX admin_transfers_history ON transfers(created_at DESC,id DESC);
 CREATE INDEX admin_slots_history ON slots(created_at DESC,id DESC);
 CREATE INDEX admin_transfers_owner_history ON transfers(owner_id,created_at DESC,id DESC);
 CREATE INDEX admin_slots_owner_history ON slots(owner_id,created_at DESC,id DESC);
 CREATE INDEX admin_transfers_status_history ON transfers(status,created_at DESC,id DESC);
 CREATE INDEX admin_slots_status_history ON slots(status,created_at DESC,id DESC);
 CREATE INDEX admin_transfers_owner_status_history ON transfers(owner_id,status,created_at DESC,id DESC);
 CREATE INDEX admin_slots_owner_status_history ON slots(owner_id,status,created_at DESC,id DESC);
 CREATE INDEX security_events_target ON security_events(target_type,target_id,id DESC);
 CREATE TABLE admin_resource_totals(kind TEXT NOT NULL CHECK(kind IN ('transfer','slot')),resource_id TEXT NOT NULL,
 file_count INTEGER NOT NULL DEFAULT 0 CHECK(typeof(file_count)='integer' AND file_count>=0),child_transfer_count INTEGER NOT NULL DEFAULT 0 CHECK(typeof(child_transfer_count)='integer' AND child_transfer_count>=0),
 reserved_bytes INTEGER NOT NULL DEFAULT 0 CHECK(typeof(reserved_bytes)='integer' AND reserved_bytes>=0),occupied_bytes INTEGER NOT NULL DEFAULT 0 CHECK(typeof(occupied_bytes)='integer' AND occupied_bytes>=0),manifest_bytes INTEGER NOT NULL DEFAULT 0 CHECK(typeof(manifest_bytes)='integer' AND manifest_bytes>=0),
 PRIMARY KEY(kind,resource_id));
 INSERT INTO admin_resource_totals(kind,resource_id,file_count,reserved_bytes,occupied_bytes,manifest_bytes)
 SELECT 'transfer',t.id,COUNT(f.id),COALESCE(SUM(CASE WHEN f.payload_deleted=0 THEN f.size ELSE 0 END),0)+COALESCE(length(m.data),0),COALESCE(SUM(CASE WHEN f.payload_deleted=0 THEN f.upload_offset ELSE 0 END),0)+COALESCE(length(m.data),0),COALESCE(length(m.data),0)
 FROM transfers t LEFT JOIN files f ON f.transfer_id=t.id LEFT JOIN manifests m ON m.transfer_id=t.id GROUP BY t.id;
 INSERT INTO admin_resource_totals(kind,resource_id,file_count,child_transfer_count,reserved_bytes,occupied_bytes,manifest_bytes)
 SELECT 'slot',s.id,COALESCE(SUM(t.file_count),0),COUNT(st.transfer_id),COALESCE(SUM(t.reserved_bytes),0),COALESCE(SUM(t.occupied_bytes),0),COALESCE(SUM(t.manifest_bytes),0)
 FROM slots s LEFT JOIN slot_transfers st ON st.slot_id=s.id LEFT JOIN admin_resource_totals t ON t.kind='transfer' AND t.resource_id=st.transfer_id GROUP BY s.id;
 CREATE TRIGGER admin_totals_transfer_create AFTER INSERT ON transfers BEGIN INSERT INTO admin_resource_totals(kind,resource_id) VALUES('transfer',NEW.id); END;
 CREATE TRIGGER admin_totals_slot_create AFTER INSERT ON slots BEGIN INSERT INTO admin_resource_totals(kind,resource_id) VALUES('slot',NEW.id); END;
 CREATE TRIGGER admin_totals_transfer_delete AFTER DELETE ON transfers BEGIN DELETE FROM admin_resource_totals WHERE kind='transfer' AND resource_id=OLD.id; END;
 CREATE TRIGGER admin_totals_slot_delete AFTER DELETE ON slots BEGIN DELETE FROM admin_resource_totals WHERE kind='slot' AND resource_id=OLD.id; END;
 `
	fileDelta := func(row, sign string) string {
		return `UPDATE admin_resource_totals SET file_count=file_count` + sign + `1,reserved_bytes=reserved_bytes` + sign + `(CASE WHEN ` + row + `.payload_deleted=0 THEN ` + row + `.size ELSE 0 END),occupied_bytes=occupied_bytes` + sign + `(CASE WHEN ` + row + `.payload_deleted=0 THEN ` + row + `.upload_offset ELSE 0 END) WHERE (kind='transfer' AND resource_id=` + row + `.transfer_id) OR (kind='slot' AND resource_id IN (SELECT slot_id FROM slot_transfers WHERE transfer_id=` + row + `.transfer_id));`
	}
	manifestDelta := func(row, sign string) string {
		return `UPDATE admin_resource_totals SET reserved_bytes=reserved_bytes` + sign + `length(` + row + `.data),occupied_bytes=occupied_bytes` + sign + `length(` + row + `.data),manifest_bytes=manifest_bytes` + sign + `length(` + row + `.data) WHERE (kind='transfer' AND resource_id=` + row + `.transfer_id) OR (kind='slot' AND resource_id IN (SELECT slot_id FROM slot_transfers WHERE transfer_id=` + row + `.transfer_id));`
	}
	for _, table := range []string{"files", "manifests"} {
		delta := fileDelta
		columns := "transfer_id,size,upload_offset,payload_deleted"
		if table == "manifests" {
			delta = manifestDelta
			columns = "transfer_id,data"
		}
		s += `CREATE TRIGGER admin_totals_` + table + `_insert AFTER INSERT ON ` + table + ` BEGIN ` + delta("NEW", "+") + ` END;`
		s += `CREATE TRIGGER admin_totals_` + table + `_delete AFTER DELETE ON ` + table + ` BEGIN ` + delta("OLD", "-") + ` END;`
		s += `CREATE TRIGGER admin_totals_` + table + `_update AFTER UPDATE OF ` + columns + ` ON ` + table + ` BEGIN ` + delta("OLD", "-") + delta("NEW", "+") + ` END;`
	}
	membershipDelta := func(row, sign string) string {
		delta := `UPDATE admin_resource_totals SET child_transfer_count=child_transfer_count` + sign + `1`
		for _, column := range []string{"file_count", "reserved_bytes", "occupied_bytes", "manifest_bytes"} {
			delta += `,` + column + `=` + column + sign + `COALESCE((SELECT ` + column + ` FROM admin_resource_totals WHERE kind='transfer' AND resource_id=` + row + `.transfer_id),0)`
		}
		return delta + ` WHERE kind='slot' AND resource_id=` + row + `.slot_id;`
	}
	s += `CREATE TRIGGER admin_totals_membership_insert AFTER INSERT ON slot_transfers BEGIN ` + membershipDelta("NEW", "+") + ` END;`
	s += `CREATE TRIGGER admin_totals_membership_delete AFTER DELETE ON slot_transfers BEGIN ` + membershipDelta("OLD", "-") + ` END;`
	s += `CREATE TRIGGER admin_totals_membership_update AFTER UPDATE OF slot_id,transfer_id ON slot_transfers BEGIN ` + membershipDelta("OLD", "-") + membershipDelta("NEW", "+") + ` UPDATE transfers SET owner_id=(SELECT owner_id FROM slots WHERE id=NEW.slot_id) WHERE id=NEW.transfer_id AND owner_id IS NULL; END;`
	return s
}

type AdminResource struct {
	Type                  string                `json:"type"`
	ID                    string                `json:"id"`
	ParentSlotID          *string               `json:"parent_slot_id"`
	OwnerID               *string               `json:"owner_id"`
	OwnerUsername         *string               `json:"owner_username"`
	OwnerDisabled         bool                  `json:"owner_disabled"`
	CreatedAt             time.Time             `json:"created_at"`
	ExpiresAt             time.Time             `json:"expires_at"`
	PendingExpiresAt      *time.Time            `json:"pending_expires_at"`
	Status                string                `json:"status"`
	FileCount             int64                 `json:"file_count"`
	ChildTransferCount    int64                 `json:"child_transfer_count"`
	ReservedBytes         int64                 `json:"reserved_bytes"`
	OccupiedBytesEstimate int64                 `json:"occupied_bytes_estimate"`
	ManifestBytes         int64                 `json:"manifest_bytes"`
	Cleanup               ResourceCleanupStatus `json:"cleanup"`
	createdText           string
}

type AdminResourceFilter struct {
	Type    string `json:"type"`
	OwnerID string `json:"owner_id"`
	Status  string `json:"status"`
}
type adminResourceCursor struct {
	Version int                 `json:"v"`
	Created string              `json:"created"`
	ID      string              `json:"id"`
	Type    string              `json:"type"`
	Filter  AdminResourceFilter `json:"filter"`
}
type AdminResourcePage struct {
	Resources  []AdminResource `json:"resources"`
	NextCursor *string         `json:"next_cursor"`
}

func (f AdminResourceFilter) valid() bool {
	if f.Type != "" && f.Type != "transfer" && f.Type != "slot" {
		return false
	}
	if !validAuditID(f.OwnerID) {
		return false
	}
	switch f.Status {
	case "", "pending", "complete", "waiting", "revoked":
		return true
	}
	return false
}
func parseAdminResourceCursor(raw string, f AdminResourceFilter) (*adminResourceCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 1024 {
		return nil, ErrInvalidPage
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidPage
	}
	var c adminResourceCursor
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF || c.Version != 1 || c.Filter != f || len(c.Created) == 0 || len(c.Created) > 64 || c.ID == "" || !validAuditID(c.ID) || (c.Type != "transfer" && c.Type != "slot") {
		return nil, ErrInvalidPage
	}
	return &c, nil
}
func adminResourceBefore(a, b AdminResource) bool {
	if a.createdText != b.createdText {
		return a.createdText > b.createdText
	}
	if a.ID != b.ID {
		return a.ID > b.ID
	}
	return a.Type > b.Type
}

func adminResourceSelect(kind string, indexes ...string) string {
	index := ""
	if len(indexes) > 0 {
		index = " INDEXED BY " + indexes[0]
	}
	parent, pending := "NULL", "NULL"
	if kind == "transfer" {
		parent = `(SELECT slot_id FROM slot_transfers WHERE transfer_id=r.id ORDER BY slot_id LIMIT 1)`
		pending = "r.pending_expires_at"
	}
	return `SELECT r.id,` + parent + `,r.owner_id,u.username,COALESCE(u.disabled,0),r.created_at,r.expires_at,` + pending + `,r.status,c.file_count,c.child_transfer_count,c.reserved_bytes,c.occupied_bytes,c.manifest_bytes,CAST(r.created_at AS TEXT) FROM ` + kind + `s r` + index + ` JOIN admin_resource_totals c ON c.kind='` + kind + `' AND c.resource_id=r.id LEFT JOIN users u ON u.id=r.owner_id`
}
func scanAdminResource(row interface{ Scan(...any) error }, kind string) (AdminResource, error) {
	item := AdminResource{Type: kind}
	var parent, owner, name sql.NullString
	var pending sql.NullTime
	err := row.Scan(&item.ID, &parent, &owner, &name, &item.OwnerDisabled, &item.CreatedAt, &item.ExpiresAt, &pending, &item.Status, &item.FileCount, &item.ChildTransferCount, &item.ReservedBytes, &item.OccupiedBytesEstimate, &item.ManifestBytes, &item.createdText)
	if parent.Valid {
		item.ParentSlotID = &parent.String
	}
	if owner.Valid {
		item.OwnerID = &owner.String
	}
	if name.Valid {
		item.OwnerUsername = &name.String
	}
	if pending.Valid {
		item.PendingExpiresAt = &pending.Time
	}
	return item, err
}

// Each branch uses an ordered index and stops at limit+1. Only those at most 202
// rows are merged; correlated metadata lookups use primary/foreign-key indexes.
func (q *Queries) AdminResources(f AdminResourceFilter, limit int, after string) (AdminResourcePage, error) {
	p := AdminResourcePage{Resources: []AdminResource{}}
	if !f.valid() || limit < 1 || limit > 100 {
		return p, ErrInvalidPage
	}
	cursor, err := parseAdminResourceCursor(after, f)
	if err != nil {
		return p, err
	}
	for _, kind := range []string{"transfer", "slot"} {
		if f.Type != "" && f.Type != kind {
			continue
		}
		index := "admin_" + kind + "s_history"
		if f.OwnerID != "" && f.Status != "" {
			index = "admin_" + kind + "s_owner_status_history"
		} else if f.OwnerID != "" {
			index = "admin_" + kind + "s_owner_history"
		} else if f.Status != "" {
			index = "admin_" + kind + "s_status_history"
		}
		query := adminResourceSelect(kind, index) + ` WHERE 1=1`
		args := []any{}
		if f.OwnerID != "" {
			query += ` AND r.owner_id=?`
			args = append(args, f.OwnerID)
		}
		if f.Status != "" {
			query += ` AND r.status=?`
			args = append(args, f.Status)
		}
		if cursor != nil {
			operator := "<"
			if kind < cursor.Type {
				operator = "<="
			}
			query += ` AND (r.created_at,r.id)` + operator + `(?,?)`
			args = append(args, cursor.Created, cursor.ID)
		}
		query += ` ORDER BY r.created_at DESC,r.id DESC LIMIT ?`
		args = append(args, limit+1)
		rows, err := q.db.Query(query, args...)
		if err != nil {
			return p, err
		}
		for rows.Next() {
			item, err := scanAdminResource(rows, kind)
			if err != nil {
				rows.Close()
				return p, err
			}
			p.Resources = append(p.Resources, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return p, err
		}
	}
	sort.Slice(p.Resources, func(i, j int) bool { return adminResourceBefore(p.Resources[i], p.Resources[j]) })
	if len(p.Resources) > limit {
		p.Resources = p.Resources[:limit]
		last := p.Resources[len(p.Resources)-1]
		raw, _ := json.Marshal(adminResourceCursor{1, last.createdText, last.ID, last.Type, f})
		encoded := base64.RawURLEncoding.EncodeToString(raw)
		p.NextCursor = &encoded
	}
	for i := range p.Resources {
		p.Resources[i].Cleanup, err = q.ResourceCleanup(p.Resources[i].Type, p.Resources[i].ID)
		if err != nil {
			return p, err
		}
	}
	return p, nil
}
func (q *Queries) AdminResource(kind, id string) (AdminResource, error) {
	if (kind != "transfer" && kind != "slot") || id == "" || !validAuditID(id) {
		return AdminResource{}, ErrInvalidPage
	}
	item, err := scanAdminResource(q.db.QueryRow(adminResourceSelect(kind)+` WHERE r.id=?`, id), kind)
	if err != nil {
		return item, err
	}
	item.Cleanup, err = q.ResourceCleanup(kind, id)
	return item, err
}

// Direct events survive deletion. For extant submissions, include inbox denial;
// owner denial events explain why a previously usable capability stopped working.
func (q *Queries) AdminResourceEvents(kind, id string, before int64, limit int, now time.Time) (SecurityEventPage, error) {
	p := SecurityEventPage{Events: []SecurityEvent{}, RetentionDays: SecurityAuditRetentionDays, MaxEvents: SecurityAuditMaxEvents, Degraded: q.SecurityAuditDegraded()}
	if (kind != "transfer" && kind != "slot") || id == "" || !validAuditID(id) || before < 0 || limit < 1 || limit > 100 {
		return p, ErrInvalidPage
	}
	type target struct {
		kind, id string
		kinds    []string
	}
	targets := []target{{kind, id, nil}}
	item, err := q.AdminResource(kind, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if err == nil {
		if item.ParentSlotID != nil {
			targets = append(targets, target{"slot", *item.ParentSlotID, []string{"slot.revoked"}})
		}
		if item.OwnerID != nil {
			targets = append(targets, target{"user", *item.OwnerID, []string{"account.shutdown", "account.disabled", "account.enabled"}})
		}
	}
	for _, target := range targets {
		query := `SELECT id,occurred_at,kind,origin,actor_id,target_type,target_id,outcome,event_count FROM security_events WHERE target_type=? AND target_id=? AND occurred_at>=?`
		args := []any{target.kind, target.id, securityAuditFloor(now)}
		if before > 0 {
			query += ` AND id<?`
			args = append(args, before)
		}
		if len(target.kinds) > 0 {
			query += ` AND kind IN (` + strings.TrimRight(strings.Repeat("?,", len(target.kinds)), ",") + `)`
			for _, kind := range target.kinds {
				args = append(args, kind)
			}
		}
		query += ` ORDER BY id DESC LIMIT ?`
		args = append(args, limit+1)
		rows, err := q.db.Query(query, args...)
		if err != nil {
			q.MarkSecurityAuditDegraded()
			return p, err
		}
		for rows.Next() {
			var e SecurityEvent
			if err = rows.Scan(&e.ID, &e.OccurredAt, &e.Kind, &e.Origin, &e.ActorID, &e.TargetType, &e.TargetID, &e.Outcome, &e.Count); err != nil {
				rows.Close()
				q.MarkSecurityAuditDegraded()
				return p, err
			}
			p.Events = append(p.Events, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			q.MarkSecurityAuditDegraded()
			return p, err
		}
	}
	sort.Slice(p.Events, func(i, j int) bool { return p.Events[i].ID > p.Events[j].ID })
	if len(p.Events) > limit {
		p.Events = p.Events[:limit]
		cursor := p.Events[len(p.Events)-1].ID
		p.NextBefore = &cursor
	}
	return p, nil
}
