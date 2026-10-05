package database

import (
	"encoding/base64"
	"errors"
)

var ErrInvalidPage = errors.New("invalid page limit or cursor")

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
	defer func() { _ = rows.Close() }()
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
