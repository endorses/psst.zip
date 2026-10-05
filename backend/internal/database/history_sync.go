package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// ErrHistorySyncReset asks a client to obtain a bounded snapshot in this generation.
var ErrHistorySyncReset = errors.New("history synchronization requires a fresh snapshot")

type historySyncCursor struct {
	Version    int    `json:"v"`
	Actor      string `json:"actor"`
	Generation string `json:"generation"`
	Revision   int64  `json:"revision"`
}

// HistoryChange describes one authoritative resource update or removal.
type HistoryChange struct {
	Kind     string
	ID       string
	Revision int64
	Action   string
	Resource *HistoryResource
}

// HistoryChanges is a bounded account feed page and its examined continuation.
type HistoryChanges struct {
	Generation string
	Changes    []HistoryChange
	NextCursor string
	HasMore    bool
}

func encodeHistorySyncCursor(actor, generation string, revision int64) string {
	raw, _ := json.Marshal(historySyncCursor{1, actor, generation, revision})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeHistorySyncCursor(value, actor string) (historySyncCursor, error) {
	var cursor historySyncCursor
	if len(value) == 0 || len(value) > 512 {
		return cursor, ErrInvalidPage
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != value {
		return cursor, ErrInvalidPage
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Actor != actor || cursor.Actor == "" || cursor.Revision < 0 || cursor.Revision > 9007199254740991 || len(cursor.Generation) != 36 {
		return cursor, ErrInvalidPage
	}
	canonical, _ := json.Marshal(cursor)
	if !bytes.Equal(raw, canonical) {
		return cursor, ErrInvalidPage
	}
	if cursor.Version != 1 {
		return cursor, ErrHistorySyncReset
	}
	return cursor, nil
}

// AccountHistoryChanges advances through at most limit indexed events. Resource
// summaries and revisions are read in the same snapshot as the feed watermark.
// An identity changed again later in that snapshot is returned at its latest
// revision; replay from the examined cursor remains safe and idempotent.
func (q *Queries) AccountHistoryChanges(ctx context.Context, actor string, limit int, value string, sessionIDs ...string) (HistoryChanges, error) {
	out := HistoryChanges{Changes: []HistoryChange{}}
	if !validAuditID(actor) || limit < 1 || limit > 100 {
		return out, ErrInvalidPage
	}
	cursor, err := decodeHistorySyncCursor(value, actor)
	if err != nil {
		return out, err
	}
	tx, err := q.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	session := ""
	if len(sessionIDs) > 0 {
		session = sessionIDs[0]
	}
	if err = checkHistoryAccess(ctx, tx, actor, session, false); err != nil {
		return out, err
	}

	var watermark, floor, accountFloor, ageFloor int64
	if err = tx.QueryRowContext(ctx, `SELECT generation,revision,global_floor FROM history_sync_state WHERE id=1`).Scan(&out.Generation, &watermark, &floor); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT floor FROM history_sync_accounts WHERE owner_id=?),0),COALESCE((SELECT MAX(revision) FROM history_sync_events INDEXED BY history_sync_events_account_age WHERE owner_id=? AND created_at<?),0)`, actor, actor, time.Now().Add(-7*24*time.Hour).Unix()).Scan(&accountFloor, &ageFloor); err != nil {
		return out, err
	}
	if floor > watermark || accountFloor > watermark {
		return out, errors.New("invalid history retention state")
	}
	if cursor.Generation != out.Generation || cursor.Revision < max(floor, accountFloor, ageFloor) || cursor.Revision > watermark {
		return out, ErrHistorySyncReset
	}
	rows, err := tx.QueryContext(ctx, `SELECT revision,kind,resource_id,action FROM history_sync_events INDEXED BY history_sync_events_account WHERE owner_id=? AND revision>? ORDER BY revision LIMIT ?`, actor, cursor.Revision, limit+1)
	if err != nil {
		return out, err
	}
	events := []HistoryChange{}
	for rows.Next() {
		var event HistoryChange
		if err = rows.Scan(&event.Revision, &event.Kind, &event.ID, &event.Action); err != nil {
			_ = rows.Close()
			return out, err
		}
		events = append(events, event)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, err
	}
	if len(events) > limit {
		out.HasMore = true
		events = events[:limit]
	}
	examined := cursor.Revision
	latest := map[string]int{}
	for i, event := range events {
		latest[event.Kind+":"+event.ID] = i
		examined = event.Revision
	}
	for i, event := range events {
		if latest[event.Kind+":"+event.ID] != i {
			continue
		}
		item, readErr := readHistoryResource(ctx, tx, event.Kind, event.ID)
		if errors.Is(readErr, sql.ErrNoRows) || (readErr == nil && item.OwnerID != actor) {
			event.Action = "remove"
		} else if readErr != nil {
			return out, readErr
		} else {
			event.Action = "upsert"
			setHistoryAnchors(&item, event.Kind, event.ID, actor, false)
			event.Resource = &item
			event.Revision = max(event.Revision, item.Revision)
		}
		out.Changes = append(out.Changes, event)
	}
	if !out.HasMore {
		examined = watermark
	}
	out.NextCursor = encodeHistorySyncCursor(actor, out.Generation, examined)
	return out, tx.Commit()
}

// PruneHistoryChanges removes one bounded age batch. Count bounds are maintained
// atomically by event triggers; floors survive removal and invalidate old cursors.
func (q *Queries) PruneHistoryChanges(ctx context.Context, now time.Time) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM history_sync_events WHERE revision IN (SELECT revision FROM history_sync_events INDEXED BY history_sync_events_age WHERE created_at<? ORDER BY created_at,revision LIMIT 256)`, now.Add(-7*24*time.Hour).Unix())
	if err != nil {
		return err
	}
	_, err = q.db.ExecContext(ctx, `DELETE FROM history_sync_accounts WHERE owner_id IN (SELECT a.owner_id FROM history_sync_accounts a LEFT JOIN users u ON u.id=a.owner_id WHERE a.event_count=0 AND u.id IS NULL LIMIT 256)`)
	return err
}

func checkHistoryAccess(ctx context.Context, tx *sql.Tx, actor, session string, all bool) error {
	var role string
	var disabled, password bool
	if err := tx.QueryRowContext(ctx, `SELECT role,disabled,must_change_password FROM users WHERE id=?`, actor).Scan(&role, &disabled, &password); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHistoryAccess
		}
		return err
	}
	if disabled || password || (all && role != "admin") || (!all && role != "user") {
		return ErrHistoryAccess
	}
	if session != "" {
		var expires time.Time
		if err := tx.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE id=? AND user_id=?`, session, actor).Scan(&expires); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrHistoryAccess
			}
			return err
		}
		if !time.Now().Before(expires) {
			return ErrHistoryAccess
		}
	}
	return nil
}
