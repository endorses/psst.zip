package database

import "database/sql"

// MaxFileSize is the administrator's plaintext-byte limit; zero means unset.
func (q *Queries) MaxFileSize() (int64, error) {
	var value int64
	err := q.db.QueryRow("SELECT max_file_size FROM server_settings WHERE id=1").Scan(&value)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return value, err
}

func (q *Queries) SetMaxFileSize(value int64, actors ...*AdminActor) error {
	tx, err := q.beginAdminMutation(actors)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	auditChanged := false
	if optionalAdminActor(actors) != nil {
		var previous int64
		if err = tx.QueryRow(`SELECT max_file_size FROM server_settings WHERE id=1`).Scan(&previous); err != nil && err != sql.ErrNoRows {
			return err
		}
		auditChanged = previous != value
	}

	_, err = tx.Exec("INSERT INTO server_settings(id,max_file_size) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET max_file_size=excluded.max_file_size", value)
	if err != nil {
		return err
	}
	if auditChanged {
		if err = q.auditAdminMutation(tx, actors, "settings.file_size_changed", "server", "", false); err != nil {
			return err
		}
	}
	return tx.Commit()
}
