package database

// inboxTotalsMigration adds derived, retained inbox counters without scanning
// historical payload metadata. The bounded counter worker supplies old values.
func inboxTotalsMigration() string {
	s := `ALTER TABLE admin_resource_totals ADD COLUMN inbox_known INTEGER NOT NULL DEFAULT 0;
ALTER TABLE admin_resource_totals ADD COLUMN total_file_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE admin_resource_totals ADD COLUMN uploaded_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE admin_resource_totals ADD COLUMN completed_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE counter_rebuild_jobs ADD COLUMN total_file_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE counter_rebuild_jobs ADD COLUMN uploaded_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE counter_rebuild_jobs ADD COLUMN completed_files INTEGER NOT NULL DEFAULT 0;
DELETE FROM counter_rebuild_jobs;
UPDATE counter_rebuild_progress SET phase='summaries',cursor='',cursor_kind='',last_scan_completed_at=NULL,rescan_required=0;
DROP TRIGGER admin_totals_transfer_create;
DROP TRIGGER admin_totals_slot_create;
CREATE TRIGGER admin_totals_transfer_create AFTER INSERT ON transfers BEGIN INSERT INTO admin_resource_totals(kind,resource_id,inbox_known) VALUES('transfer',NEW.id,1); END;
CREATE TRIGGER admin_totals_slot_create AFTER INSERT ON slots BEGIN INSERT INTO admin_resource_totals(kind,resource_id,inbox_known) VALUES('slot',NEW.id,1); END;
`
	parents := func(transfer string) string {
		return `SELECT slot_id FROM slot_transfers WHERE transfer_id=` + transfer
	}
	fileDelta := func(row, sign string) string {
		invalid := `UPDATE admin_resource_totals SET inbox_known=0 WHERE (` + row + `.size<0 OR ` + row + `.upload_complete NOT IN (0,1)) AND ((kind='transfer' AND resource_id=` + row + `.transfer_id) OR (kind='slot' AND resource_id IN (` + parents(row+`.transfer_id`) + `)));`
		return invalid + `UPDATE admin_resource_totals SET total_file_bytes=total_file_bytes` + sign + row + `.size,uploaded_files=uploaded_files` + sign + row + `.upload_complete,completed_files=completed_files` + sign + `(` + row + `.upload_complete*COALESCE((SELECT status='complete' FROM transfers WHERE id=` + row + `.transfer_id),0)) WHERE inbox_known=1 AND ((kind='transfer' AND resource_id=` + row + `.transfer_id) OR (kind='slot' AND resource_id IN (` + parents(row+`.transfer_id`) + `)));`
	}
	s += `CREATE TRIGGER inbox_totals_file_insert AFTER INSERT ON files BEGIN ` + fileDelta("NEW", "+") + ` END;
CREATE TRIGGER inbox_totals_file_delete AFTER DELETE ON files BEGIN ` + fileDelta("OLD", "-") + ` END;
CREATE TRIGGER inbox_totals_file_update AFTER UPDATE OF transfer_id,size,upload_complete ON files BEGIN ` + fileDelta("OLD", "-") + fileDelta("NEW", "+") + ` END;`
	// Completion is independent of upload_offset; invalidate staged scans for
	// completion-only writes and changes to the child's retained status.
	bump := func(transfer string) string {
		return `UPDATE counter_rebuild_sources SET revision=revision+1 WHERE (kind='transfer' AND resource_id=` + transfer + `) OR (kind='slot' AND resource_id IN (` + parents(transfer) + `));`
	}
	s += `CREATE TRIGGER inbox_counter_file_complete AFTER UPDATE OF upload_complete ON files WHEN NEW.upload_complete IS NOT OLD.upload_complete BEGIN ` + bump("NEW.transfer_id") + ` END;`
	wake := `UPDATE counter_rebuild_progress SET phase=CASE WHEN phase='idle' THEN 'summaries' ELSE phase END,cursor=CASE WHEN phase='idle' THEN '' ELSE cursor END,cursor_kind=CASE WHEN phase='idle' THEN '' ELSE cursor_kind END,rescan_required=1,last_scan_completed_at=NULL WHERE id=1 AND `
	s += `CREATE TRIGGER inbox_totals_transfer_status AFTER UPDATE OF status ON transfers WHEN NEW.status IS NOT OLD.status BEGIN
UPDATE admin_resource_totals SET completed_files=completed_files+COALESCE((SELECT uploaded_files FROM admin_resource_totals WHERE kind='transfer' AND resource_id=NEW.id AND inbox_known=1),0)*((NEW.status='complete')-(OLD.status='complete')),inbox_known=inbox_known*COALESCE((SELECT inbox_known FROM admin_resource_totals WHERE kind='transfer' AND resource_id=NEW.id),0) WHERE kind='slot' AND inbox_known=1 AND resource_id IN (` + parents("NEW.id") + `);
UPDATE admin_resource_totals SET completed_files=uploaded_files*(NEW.status='complete') WHERE kind='transfer' AND resource_id=NEW.id AND inbox_known=1;
` + bump("NEW.id") + `
UPDATE counter_rebuild_progress SET rescan_required=1,phase=CASE WHEN phase='idle' THEN 'summaries' ELSE phase END,cursor=CASE WHEN phase='idle' THEN '' ELSE cursor END,last_scan_completed_at=NULL WHERE id=1 AND NOT EXISTS(SELECT 1 FROM admin_resource_totals WHERE kind='transfer' AND resource_id=NEW.id AND inbox_known=1);
END;`
	membershipDelta := func(row, sign string) string {
		delta := `UPDATE admin_resource_totals SET inbox_known=inbox_known*COALESCE((SELECT inbox_known FROM admin_resource_totals WHERE kind='transfer' AND resource_id=` + row + `.transfer_id),0)`
		for _, column := range []string{"total_file_bytes", "uploaded_files", "completed_files"} {
			delta += `,` + column + `=` + column + sign + `COALESCE((SELECT ` + column + ` FROM admin_resource_totals WHERE kind='transfer' AND resource_id=` + row + `.transfer_id AND inbox_known=1),0)`
		}
		return delta + ` WHERE kind='slot' AND resource_id=` + row + `.slot_id AND inbox_known=1;`
	}
	for _, event := range []string{"insert", "delete", "update"} {
		s += `CREATE TRIGGER inbox_totals_membership_` + event + ` AFTER `
		switch event {
		case "insert":
			s += `INSERT ON slot_transfers BEGIN ` + membershipDelta("NEW", "+")
		case "delete":
			s += `DELETE ON slot_transfers BEGIN ` + membershipDelta("OLD", "-")
		case "update":
			s += `UPDATE OF slot_id,transfer_id ON slot_transfers BEGIN ` + membershipDelta("OLD", "-") + membershipDelta("NEW", "+")
		}
		// Existing counter discovery already replays membership changes during a
		// pass. A newly unknown parent also wakes an otherwise idle worker.
		unknown := func(row string) string {
			return `(EXISTS(SELECT 1 FROM slots WHERE id=` + row + `.slot_id) AND NOT EXISTS(SELECT 1 FROM admin_resource_totals WHERE kind='slot' AND resource_id=` + row + `.slot_id AND inbox_known=1))`
		}
		condition := unknown("NEW")
		if event == "delete" {
			condition = unknown("OLD")
		}
		if event == "update" {
			condition = "(" + unknown("NEW") + " OR " + unknown("OLD") + ")"
		}
		s += wake + condition + `; END;`
	}
	return s
}
