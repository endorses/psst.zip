package database

import "strings"

// historySyncMigration records identities only. Triggers run inside the metadata
// mutation's transaction, including counter repairs and administrative cleanup.
func historySyncMigration() string {
	s := `CREATE TABLE history_sync_state(id INTEGER PRIMARY KEY CHECK(id=1),generation TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 0 CHECK(revision BETWEEN 0 AND 9007199254740991),event_count INTEGER NOT NULL DEFAULT 0 CHECK(event_count>=0),global_floor INTEGER NOT NULL DEFAULT 0 CHECK(global_floor BETWEEN 0 AND revision));
INSERT INTO history_sync_state(id,generation) VALUES(1,'');
CREATE TABLE history_sync_accounts(owner_id TEXT PRIMARY KEY,event_count INTEGER NOT NULL DEFAULT 0 CHECK(event_count>=0),floor INTEGER NOT NULL DEFAULT 0 CHECK(floor>=0));
CREATE TABLE history_sync_private_transfers(transfer_id TEXT PRIMARY KEY REFERENCES transfers(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED);
INSERT INTO history_sync_private_transfers SELECT DISTINCT transfer_id FROM slot_transfers;
CREATE TABLE history_sync_revisions(kind TEXT NOT NULL CHECK(kind IN ('transfer','slot')),resource_id TEXT NOT NULL,revision INTEGER NOT NULL CHECK(revision BETWEEN 0 AND 9007199254740991),PRIMARY KEY(kind,resource_id));
CREATE TABLE history_sync_events(revision INTEGER PRIMARY KEY CHECK(revision BETWEEN 1 AND 9007199254740991),owner_id TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('transfer','slot')),resource_id TEXT NOT NULL,action TEXT NOT NULL CHECK(action IN ('upsert','remove')),created_at INTEGER NOT NULL DEFAULT (unixepoch()));
CREATE INDEX history_sync_events_account ON history_sync_events(owner_id,revision);
CREATE INDEX history_sync_events_age ON history_sync_events(created_at,revision);
CREATE INDEX history_sync_events_account_age ON history_sync_events(owner_id,created_at,revision);
CREATE TRIGGER history_sync_event_insert AFTER INSERT ON history_sync_events BEGIN
INSERT INTO history_sync_revisions(kind,resource_id,revision) VALUES(NEW.kind,NEW.resource_id,NEW.revision) ON CONFLICT(kind,resource_id) DO UPDATE SET revision=excluded.revision;
UPDATE history_sync_state SET event_count=event_count+1 WHERE id=1;
INSERT INTO history_sync_accounts(owner_id,event_count) VALUES(NEW.owner_id,1) ON CONFLICT(owner_id) DO UPDATE SET event_count=event_count+1;
UPDATE history_sync_accounts SET floor=MAX(floor,COALESCE((SELECT MAX(revision) FROM (SELECT revision FROM history_sync_events WHERE owner_id=NEW.owner_id ORDER BY revision LIMIT 256)),0)) WHERE owner_id=NEW.owner_id AND event_count>10000;
DELETE FROM history_sync_events WHERE owner_id=NEW.owner_id AND revision<=(SELECT floor FROM history_sync_accounts WHERE owner_id=NEW.owner_id);
UPDATE history_sync_state SET global_floor=MAX(global_floor,COALESCE((SELECT MAX(revision) FROM (SELECT revision FROM history_sync_events ORDER BY revision LIMIT 256)),0)) WHERE id=1 AND event_count>100000;
DELETE FROM history_sync_events WHERE revision<=(SELECT global_floor FROM history_sync_state WHERE id=1);
END;
CREATE TRIGGER history_sync_event_delete AFTER DELETE ON history_sync_events BEGIN
UPDATE history_sync_state SET event_count=event_count-1 WHERE id=1;
UPDATE history_sync_accounts SET event_count=event_count-1,floor=MAX(floor,OLD.revision) WHERE owner_id=OLD.owner_id;
END;
`

	emit := func(kind, id, owner, action string) string {
		eligible := `COALESCE(` + owner + `,'')!=''`
		if kind == "transfer" {
			private := `(EXISTS(SELECT 1 FROM history_sync_private_transfers WHERE transfer_id=` + id + `) OR EXISTS(SELECT 1 FROM slot_transfers WHERE transfer_id=` + id + `))`
			if action == "upsert" {
				eligible += ` AND NOT ` + private
			} else {
				eligible += ` AND (NOT ` + private + ` OR EXISTS(SELECT 1 FROM history_sync_revisions WHERE kind='transfer' AND resource_id=` + id + `))`
			}
		}
		return `SELECT CASE WHEN ` + eligible + ` AND (NOT EXISTS(SELECT 1 FROM history_sync_state WHERE id=1) OR COALESCE((SELECT floor FROM history_sync_accounts WHERE owner_id=` + owner + `),0)>(SELECT revision FROM history_sync_state WHERE id=1)) THEN RAISE(ABORT,'history synchronization unavailable') END; UPDATE history_sync_state SET revision=revision+1 WHERE id=1 AND ` + eligible + `; INSERT INTO history_sync_events(revision,owner_id,kind,resource_id,action) SELECT revision,` + owner + `,'` + kind + `',` + id + `,'` + action + `' FROM history_sync_state WHERE id=1 AND ` + eligible + `;`
	}

	for _, kind := range []string{"transfer", "slot"} {
		table := kind + "s"
		s += `CREATE TRIGGER history_sync_` + kind + `_insert AFTER INSERT ON ` + table + ` BEGIN ` + emit(kind, "NEW.id", "NEW.owner_id", "upsert") + ` END;`
		deleteTiming := "AFTER"
		if kind == "transfer" {
			deleteTiming = "BEFORE"
		}
		s += `CREATE TRIGGER history_sync_` + kind + `_delete ` + deleteTiming + ` DELETE ON ` + table + ` BEGIN ` + emit(kind, "OLD.id", "OLD.owner_id", "remove") + ` DELETE FROM history_sync_revisions WHERE kind='` + kind + `' AND resource_id=OLD.id; END;`
		columns := []string{"owner_id", "status", "expires_at", "created_at", "title"}
		if kind == "transfer" {
			columns = append(columns, "max_downloads", "download_count", "completed_at", "downloaded_at")
		} else {
			columns = append(columns, "receive_protocol", "recipient_public_key", "max_files", "reserved_files")
		}
		conditions := []string{}
		for _, col := range columns {
			conditions = append(conditions, "NEW."+col+" IS NOT OLD."+col)
		}
		s += `CREATE TRIGGER history_sync_` + kind + `_update AFTER UPDATE OF ` + strings.Join(columns, ",") + ` ON ` + table + ` WHEN ` + strings.Join(conditions, " OR ") + ` BEGIN `
		s += emit(kind, "OLD.id", "CASE WHEN OLD.owner_id IS NOT NEW.owner_id THEN OLD.owner_id ELSE NULL END", "remove")
		s += emit(kind, "NEW.id", "NEW.owner_id", "upsert") + ` END;`
	}
	// Derived totals are the source for both ordinary writes and bounded repair.
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		row := "NEW"
		if event == "DELETE" {
			row = "OLD"
		}
		s += `CREATE TRIGGER history_sync_totals_` + strings.ToLower(event) + ` AFTER ` + event + ` ON admin_resource_totals `
		if event == "UPDATE" {
			s += `WHEN NEW.inbox_known IS NOT OLD.inbox_known OR NEW.file_count IS NOT OLD.file_count OR NEW.completed_files IS NOT OLD.completed_files OR NEW.total_file_bytes IS NOT OLD.total_file_bytes `
		}
		s += `BEGIN `
		for _, kind := range []string{"transfer", "slot"} {
			owner := `(SELECT owner_id FROM ` + kind + `s WHERE id=` + row + `.resource_id AND ` + row + `.kind='` + kind + `')`
			s += emit(kind, row+`.resource_id`, owner, "upsert")
		}
		s += ` END;`
	}

	// Mark membership before inherited ownership can publish a private child.
	// A formerly public row still needs one removal, but new private submissions
	// never acquire a public revision or expose even their identity in this feed.
	s += `CREATE TRIGGER history_sync_membership_before_insert BEFORE INSERT ON slot_transfers BEGIN ` + emit("transfer", "NEW.transfer_id", `(SELECT owner_id FROM transfers WHERE id=NEW.transfer_id)`, "remove") + ` INSERT OR IGNORE INTO history_sync_private_transfers(transfer_id) VALUES(NEW.transfer_id); END;`
	s += `CREATE TRIGGER history_sync_membership_insert AFTER INSERT ON slot_transfers BEGIN ` + emit("slot", "NEW.slot_id", `(SELECT owner_id FROM slots WHERE id=NEW.slot_id)`, "upsert") + ` END;`
	s += `CREATE TRIGGER history_sync_membership_delete AFTER DELETE ON slot_transfers BEGIN ` + emit("slot", "OLD.slot_id", `(SELECT owner_id FROM slots WHERE id=OLD.slot_id)`, "upsert") + ` END;`
	s += `CREATE TRIGGER history_sync_membership_before_update BEFORE UPDATE OF slot_id,transfer_id ON slot_transfers WHEN NEW.slot_id IS NOT OLD.slot_id OR NEW.transfer_id IS NOT OLD.transfer_id BEGIN ` + emit("transfer", "NEW.transfer_id", `(SELECT owner_id FROM transfers WHERE id=NEW.transfer_id)`, "remove") + ` INSERT OR IGNORE INTO history_sync_private_transfers(transfer_id) VALUES(NEW.transfer_id); END;`
	s += `CREATE TRIGGER history_sync_membership_update AFTER UPDATE OF slot_id,transfer_id ON slot_transfers WHEN NEW.slot_id IS NOT OLD.slot_id OR NEW.transfer_id IS NOT OLD.transfer_id BEGIN ` + emit("slot", "OLD.slot_id", `(SELECT owner_id FROM slots WHERE id=OLD.slot_id)`, "upsert") + emit("slot", "NEW.slot_id", `(SELECT owner_id FROM slots WHERE id=NEW.slot_id)`, "upsert") + ` END;`

	// Exhaustion changes when a file attempt is admitted, not on payload bytes.
	s += `CREATE TRIGGER history_sync_file_download AFTER UPDATE OF download_count ON files WHEN NEW.download_count IS NOT OLD.download_count BEGIN ` + emit("transfer", "NEW.transfer_id", `(SELECT owner_id FROM transfers WHERE id=NEW.transfer_id)`, "upsert") + ` END;`
	for _, event := range []string{"INSERT", "DELETE"} {
		row := "NEW"
		if event == "DELETE" {
			row = "OLD"
		}
		s += `CREATE TRIGGER history_sync_manifest_` + strings.ToLower(event) + ` AFTER ` + event + ` ON manifests BEGIN ` + emit("transfer", row+".transfer_id", `(SELECT owner_id FROM transfers WHERE id=`+row+`.transfer_id)`, "upsert") + ` END;`
	}
	// Old derived-total triggers subtracted and re-added identical visible totals
	// on every upload offset. Preserve physical accounting without emitting these
	// transient file-count/size changes for a payload-only progress write.
	extractTrigger := func(sql, name string) string {
		start := strings.Index(sql, "CREATE TRIGGER "+name+" ")
		end := strings.Index(sql[start:], " END;") + start + len(" END;")
		return sql[start:end]
	}
	adminUpdate := extractTrigger(adminResourcesMigration(), "admin_totals_files_update")
	adminUpdate = strings.Replace(adminUpdate, " ON files BEGIN ", " ON files WHEN NEW.transfer_id IS NOT OLD.transfer_id OR NEW.size IS NOT OLD.size OR NEW.payload_deleted IS NOT OLD.payload_deleted BEGIN ", 1)
	s += `DROP TRIGGER admin_totals_files_update;` + adminUpdate
	s += `CREATE TRIGGER admin_totals_files_offset AFTER UPDATE OF upload_offset ON files WHEN NEW.transfer_id IS OLD.transfer_id AND NEW.size IS OLD.size AND NEW.payload_deleted IS OLD.payload_deleted AND NEW.upload_offset IS NOT OLD.upload_offset BEGIN UPDATE admin_resource_totals SET occupied_bytes=occupied_bytes+(CASE WHEN NEW.payload_deleted=0 THEN NEW.upload_offset-OLD.upload_offset ELSE 0 END) WHERE (kind='transfer' AND resource_id=NEW.transfer_id) OR (kind='slot' AND resource_id IN (SELECT slot_id FROM slot_transfers WHERE transfer_id=NEW.transfer_id)); END;`
	inboxUpdate := extractTrigger(inboxTotalsMigration(), "inbox_totals_file_update")
	inboxUpdate = strings.Replace(inboxUpdate, " ON files BEGIN ", " ON files WHEN NEW.transfer_id IS NOT OLD.transfer_id OR NEW.size IS NOT OLD.size OR NEW.upload_complete IS NOT OLD.upload_complete BEGIN ", 1)
	s += `DROP TRIGGER inbox_totals_file_update;` + inboxUpdate

	return s
}
