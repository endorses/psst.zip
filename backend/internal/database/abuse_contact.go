package database

import (
	"errors"
	"strings"
)

var ErrAbuseContactEmail = errors.New("invalid abuse contact email")

const AbuseContactChangedEvent = "settings.abuse_contact_changed"

func abuseContactMigration() string {
	kinds := append(append([]string{}, securityEventKinds...), AbuseContactChangedEvent)
	return `CREATE TABLE abuse_contact(id INTEGER PRIMARY KEY CHECK(id=1),email TEXT NOT NULL DEFAULT '' CHECK(length(email)<=254));
 INSERT INTO abuse_contact(id) VALUES(1);
 CREATE TABLE security_audit_sequence_restore(sequence INTEGER NOT NULL);
 INSERT INTO security_audit_sequence_restore SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name='security_events'),0);
 ` + securityAuditEventsTable("security_events_expanded", kinds) + `
 INSERT INTO security_events_expanded SELECT * FROM security_events;
 DROP TABLE security_events;
 ALTER TABLE security_events_expanded RENAME TO security_events;
 INSERT INTO sqlite_sequence(name,seq) SELECT 'security_events',sequence FROM security_audit_sequence_restore WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='security_events');
 UPDATE sqlite_sequence SET seq=MAX(seq,(SELECT sequence FROM security_audit_sequence_restore)) WHERE name='security_events';
 DROP TABLE security_audit_sequence_restore;
 ` + securityAuditIndexesAndTriggers(true)
}

// This intentionally supports one ordinary ASCII mailbox, not display names,
// address lists, mailto URLs, or arbitrary mail headers. '%' is a literal mailbox
// character; clients must percent-encode it when constructing a mailto URL.
func ValidAbuseContactEmail(email string) bool {
	if email == "" {
		return true
	}
	if len(email) > 254 || strings.Count(email, "@") != 1 {
		return false
	}
	local, domain, _ := strings.Cut(email, "@")
	if len(local) < 1 || len(local) > 64 || local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, "..") {
		return false
	}
	alnum := func(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
	for i := range local {
		c := local[i]
		if !alnum(c) && !strings.ContainsRune("._+%-", rune(c)) {
			return false
		}
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 || !alnum(label[0]) || !alnum(label[len(label)-1]) {
			return false
		}
		for i := range label {
			if !alnum(label[i]) && label[i] != '-' {
				return false
			}
		}
	}
	return true
}

func (q *Queries) AbuseContactEmail() (string, error) {
	var email string
	err := q.db.QueryRow(`SELECT email FROM abuse_contact WHERE id=1`).Scan(&email)
	if err == nil && !ValidAbuseContactEmail(email) {
		return "", ErrAbuseContactEmail
	}
	return email, err
}
func (q *Queries) SetAbuseContactEmail(email string, actors ...*AdminActor) error {
	if !ValidAbuseContactEmail(email) {
		return ErrAbuseContactEmail
	}
	tx, err := q.beginAdminMutation(actors)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	if err = tx.QueryRow(`SELECT email FROM abuse_contact WHERE id=1`).Scan(&previous); err != nil {
		return err
	}
	if previous == email {
		return tx.Commit()
	}
	if _, err = tx.Exec(`UPDATE abuse_contact SET email=? WHERE id=1`, email); err != nil {
		return err
	}
	if err = q.auditAdminMutation(tx, actors, AbuseContactChangedEvent, "server", "", false); err != nil {
		return err
	}
	return tx.Commit()
}
