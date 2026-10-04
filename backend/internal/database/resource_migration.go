package database

import "fmt"

// Derived usage cannot drift after a crash or failed cleanup. Physical payload
// reservations stay charged until deletion succeeds; inbox lifetime allowances
// remain independent and are never refunded here.
func resourceMigration() string {
	sql := `CREATE TABLE resource_policy (
 id INTEGER PRIMARY KEY CHECK(id=1),
 server_storage_bytes INTEGER NOT NULL DEFAULT 10737418240 CHECK(server_storage_bytes BETWEEN 1048576 AND 1125899906842624),
 account_storage_bytes INTEGER NOT NULL DEFAULT 2147483648 CHECK(account_storage_bytes BETWEEN 1048576 AND 1125899906842624),
 server_files INTEGER NOT NULL DEFAULT 10000 CHECK(server_files BETWEEN 1 AND 1000000),
 account_files INTEGER NOT NULL DEFAULT 1000 CHECK(account_files BETWEEN 1 AND 1000000),
 server_transfers INTEGER NOT NULL DEFAULT 2000 CHECK(server_transfers BETWEEN 1 AND 1000000),
 account_transfers INTEGER NOT NULL DEFAULT 200 CHECK(account_transfers BETWEEN 1 AND 1000000),
 server_slots INTEGER NOT NULL DEFAULT 500 CHECK(server_slots BETWEEN 1 AND 1000000),
 account_slots INTEGER NOT NULL DEFAULT 50 CHECK(account_slots BETWEEN 1 AND 1000000),
 max_retention_seconds INTEGER NOT NULL DEFAULT 604800 CHECK(max_retention_seconds BETWEEN 60 AND 31536000),
 pending_upload_seconds INTEGER NOT NULL DEFAULT 86400 CHECK(pending_upload_seconds BETWEEN 60 AND max_retention_seconds),
 reserve_disk_bytes INTEGER NOT NULL DEFAULT 268435456 CHECK(reserve_disk_bytes BETWEEN 1048576 AND 1099511627776),
 reserve_disk_percent INTEGER NOT NULL DEFAULT 5 CHECK(reserve_disk_percent BETWEEN 1 AND 50)
 );
 INSERT INTO resource_policy(id) VALUES(1);
 ALTER TABLE files ADD COLUMN payload_deleted INTEGER NOT NULL DEFAULT 0 CHECK(payload_deleted IN (0,1));
 ALTER TABLE transfers ADD COLUMN pending_expires_at DATETIME;
 UPDATE transfers SET pending_expires_at=datetime(substr(created_at,1,19),'+86400 seconds') WHERE status='pending';
 CREATE INDEX files_transfer ON files(transfer_id);
 CREATE INDEX transfers_owner ON transfers(owner_id,id);
 CREATE INDEX slots_owner ON slots(owner_id,id);
 CREATE INDEX transfers_history ON transfers(CAST(created_at AS TEXT) DESC,id DESC);
 CREATE INDEX slots_history ON slots(CAST(created_at AS TEXT) DESC,id DESC);
 CREATE INDEX transfers_owner_history ON transfers(owner_id,CAST(created_at AS TEXT) DESC,id DESC);
 CREATE INDEX slots_owner_history ON slots(owner_id,CAST(created_at AS TEXT) DESC,id DESC);
 CREATE VIEW resource_usage AS
 SELECT owner_id,SUM(reserved_bytes) AS reserved_bytes,SUM(occupied_bytes) AS occupied_bytes,SUM(files) AS files,SUM(transfers) AS transfers,SUM(slots) AS slots FROM (
 SELECT COALESCE(t.owner_id,'') AS owner_id,COALESCE(SUM(CASE WHEN f.payload_deleted=0 THEN f.size ELSE 0 END),0) AS reserved_bytes,COALESCE(SUM(CASE WHEN f.payload_deleted=0 THEN f.upload_offset ELSE 0 END),0) AS occupied_bytes,COUNT(f.id) AS files,0 AS transfers,0 AS slots FROM transfers t JOIN files f ON f.transfer_id=t.id GROUP BY t.owner_id
 UNION ALL SELECT COALESCE(t.owner_id,''),SUM(length(m.data)),SUM(length(m.data)),0,0,0 FROM transfers t JOIN manifests m ON m.transfer_id=t.id GROUP BY t.owner_id
 UNION ALL SELECT COALESCE(owner_id,''),0,0,0,COUNT(*),0 FROM transfers GROUP BY owner_id
 UNION ALL SELECT COALESCE(owner_id,''),0,0,0,0,COUNT(*) FROM slots GROUP BY owner_id
 ) GROUP BY owner_id;
 CREATE TRIGGER pending_upload_deadline AFTER INSERT ON transfers BEGIN
 UPDATE transfers SET pending_expires_at=datetime('now','+'||(SELECT pending_upload_seconds FROM resource_policy WHERE id=1)||' seconds') WHERE id=NEW.id;
 END;
 `
	for _, table := range []string{"transfers", "slots"} {
		sql += fmt.Sprintf(`CREATE TRIGGER budget_%[1]s BEFORE INSERT ON %[1]s BEGIN
 SELECT CASE WHEN (SELECT COUNT(*) FROM %[1]s)>=(SELECT server_%[1]s FROM resource_policy) THEN RAISE(ABORT,'resource_limit: server %[1]s') END;
 SELECT CASE WHEN (SELECT COUNT(*) FROM %[1]s WHERE COALESCE(owner_id,'')=COALESCE(NEW.owner_id,''))>=(SELECT account_%[1]s FROM resource_policy) THEN RAISE(ABORT,'resource_limit: account %[1]s') END;
 END;`, table)
	}
	sql += `CREATE TRIGGER budget_files BEFORE INSERT ON files BEGIN
 SELECT CASE WHEN NEW.size<0 THEN RAISE(ABORT,'resource_limit: invalid size') END;
 SELECT CASE WHEN (SELECT COUNT(*) FROM files)>=(SELECT server_files FROM resource_policy) THEN RAISE(ABORT,'resource_limit: server files') END;
 SELECT CASE WHEN (SELECT COALESCE(SUM(files),0) FROM resource_usage WHERE owner_id=COALESCE((SELECT owner_id FROM transfers WHERE id=NEW.transfer_id),''))>=(SELECT account_files FROM resource_policy) THEN RAISE(ABORT,'resource_limit: account files') END;
 ` + storageChecks("NEW.size") + ` END;
 CREATE TRIGGER budget_manifest BEFORE INSERT ON manifests BEGIN
 ` + storageChecks("MAX(0,length(NEW.data)-COALESCE((SELECT length(data) FROM manifests WHERE transfer_id=NEW.transfer_id),0))") + ` END;
 CREATE TRIGGER budget_manifest_update BEFORE UPDATE OF data ON manifests BEGIN
 ` + storageChecks("MAX(0,length(NEW.data)-length(OLD.data))") + ` END;`
	return sql
}
func storageChecks(delta string) string {
	return fmt.Sprintf(`SELECT CASE WHEN %[1]s>0 AND (SELECT COALESCE(SUM(reserved_bytes),0) FROM resource_usage)>(SELECT server_storage_bytes FROM resource_policy)-(%[1]s) THEN RAISE(ABORT,'resource_limit: server storage') END;
 SELECT CASE WHEN %[1]s>0 AND (SELECT COALESCE(SUM(reserved_bytes),0) FROM resource_usage WHERE owner_id=COALESCE((SELECT owner_id FROM transfers WHERE id=NEW.transfer_id),''))>(SELECT account_storage_bytes FROM resource_policy)-(%[1]s) THEN RAISE(ABORT,'resource_limit: account storage') END;`, delta)
}
