package database

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

var ErrInvalidPage = errors.New("invalid page limit or cursor")

type resourceCursor struct {
	Created string `json:"created"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
}
type ResourcePage struct {
	Transfers  []string
	Slots      []string
	NextCursor *string
}

func (q *Queries) ResourceIDsPage(owner string, limit int, after string) (ResourcePage, error) {
	page := ResourcePage{Transfers: []string{}, Slots: []string{}}
	if limit < 1 || limit > 100 {
		return page, ErrInvalidPage
	}
	query := `SELECT kind,id,created_at FROM (SELECT 'transfer' AS kind,id,CAST(created_at AS TEXT) AS created_at,owner_id FROM transfers WHERE NOT EXISTS(SELECT 1 FROM slot_transfers WHERE transfer_id=transfers.id) UNION ALL SELECT 'slot',id,CAST(created_at AS TEXT),owner_id FROM slots) WHERE 1=1`
	args := []any{}
	if owner != "" {
		query += ` AND owner_id=?`
		args = append(args, owner)
	}
	if after != "" {
		var cursor resourceCursor
		raw, err := base64.RawURLEncoding.Strict().DecodeString(after)
		if len(after) > 512 || err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Created == "" || cursor.ID == "" || (cursor.Kind != "transfer" && cursor.Kind != "slot") {
			return page, ErrInvalidPage
		}
		query += ` AND (created_at,id,kind)<(?,?,?)`
		args = append(args, cursor.Created, cursor.ID, cursor.Kind)
	}
	query += ` ORDER BY created_at DESC,id DESC,kind DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := q.db.Query(query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	var last resourceCursor
	count := 0
	for rows.Next() {
		var item resourceCursor
		if err := rows.Scan(&item.Kind, &item.ID, &item.Created); err != nil {
			return page, err
		}
		if count == limit {
			encoded, _ := json.Marshal(last)
			next := base64.RawURLEncoding.EncodeToString(encoded)
			page.NextCursor = &next
			break
		}
		if item.Kind == "transfer" {
			page.Transfers = append(page.Transfers, item.ID)
		} else {
			page.Slots = append(page.Slots, item.ID)
		}
		last = item
		count++
	}
	return page, rows.Err()
}

// User pages contain metadata only; password hashes remain excluded by User's
// JSON tags exactly as on the existing administrator endpoint.
func (q *Queries) UsersPage(limit int, after string) ([]User, *string, error) {
	users := []User{}
	if limit < 1 || limit > 100 {
		return nil, nil, ErrInvalidPage
	}
	last := ""
	if after != "" {
		raw, err := base64.RawURLEncoding.Strict().DecodeString(after)
		if err != nil || len(after) > 512 || len(raw) == 0 {
			return nil, nil, ErrInvalidPage
		}
		last = string(raw)
	}
	rows, err := q.db.Query(`SELECT id,username,role,disabled,password_hash,must_change_password FROM users WHERE id>? ORDER BY id LIMIT ?`, last, limit+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var next *string
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, nil, err
		}
		if len(users) == limit {
			encoded := base64.RawURLEncoding.EncodeToString([]byte(users[len(users)-1].ID))
			next = &encoded
			break
		}
		users = append(users, *user)
	}
	return users, next, rows.Err()
}

// SlotResourceCounts aggregates only current rows; permanent receive allowances
// are stored separately in slots.reserved_files/reserved_bytes.
func (q *Queries) SlotResourceCounts(slot string) (files, completed, size int64, err error) {
	err = q.db.QueryRow(`SELECT COUNT(f.id),COALESCE(SUM(CASE WHEN t.status='complete' AND f.upload_complete=1 THEN 1 ELSE 0 END),0),COALESCE(SUM(f.size),0)
 FROM slot_transfers st JOIN transfers t ON t.id=st.transfer_id LEFT JOIN files f ON f.transfer_id=t.id WHERE st.slot_id=?`, slot).Scan(&files, &completed, &size)
	return
}
