package database

import "database/sql"

// beginAdminMutation serializes a control write with credential revocation. A
// missing actor is reserved for internal/CLI callers and ordinary capability
// operations; authenticated administrator HTTP handlers always provide one.
func (q *Queries) beginAdminMutation(actors []*AdminActor) (*sql.Tx, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return nil, err
	}
	actor := optionalAdminActor(actors)
	if err = ValidateAdminActor(tx, actor); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}
