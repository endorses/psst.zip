package zip.psst.android.data

import java.sql.Connection
import java.sql.DriverManager
import org.junit.Assert.*
import org.junit.Test

class InboxCheckpointSqlTest {
    private fun Connection.exec(sql: String, vararg args: Any?) =
        prepareStatement(sql).use { q ->
            args.forEachIndexed { n, v -> q.setObject(n + 1, v) }
            q.executeUpdate()
            Unit
        }

    private fun Connection.value(sql: String, vararg args: Any?): String? =
        prepareStatement(sql).use { q ->
            args.forEachIndexed { n, v -> q.setObject(n + 1, v) }
            q.executeQuery().use { r -> if (r.next()) r.getString(1) else null }
        }

    @Test
    fun archiveClearRollbackPreservesOriginalAndProjectionNeverLoadsLegacyPayload() {
        DriverManager.getConnection("jdbc:sqlite::memory:").use { db ->
            db.exec("PRAGMA foreign_keys=ON")
            db.exec(
                "CREATE TABLE transfer_history (id TEXT PRIMARY KEY NOT NULL,type TEXT NOT NULL,fileCount INTEGER NOT NULL,totalSize INTEGER NOT NULL,serverUrl TEXT NOT NULL,encryptionKey TEXT NOT NULL,status TEXT NOT NULL,createdAt INTEGER NOT NULL,expiresAt INTEGER,deletionToken TEXT,accountId TEXT,title TEXT,automaticTitle TEXT,originScope TEXT NOT NULL DEFAULT '',summaryUpdating INTEGER NOT NULL DEFAULT 0,savedFileIdsJson TEXT NOT NULL DEFAULT '[]',receivedTransfersJson TEXT NOT NULL DEFAULT '{}',savedTransferIdsJson TEXT NOT NULL DEFAULT '[]')"
            )
            val original = "{\"child\":{\"fileCount\":1}}" + " ".repeat(2 * 1024 * 1024)
            db.exec(
                "INSERT INTO transfer_history(id,type,fileCount,totalSize,serverUrl,encryptionKey,status,createdAt,accountId,originScope,receivedTransfersJson,savedFileIdsJson,savedTransferIdsJson) VALUES('slot','received',1,7,'https://host','secret','complete',1,'owner','https://host',?,'[\"child/blob\"]','[\"child\"]')",
                original,
            )
            INBOX_CHECKPOINT_SCHEMA.forEach { db.exec(it) }
            val archive =
                "INSERT OR IGNORE INTO inbox_legacy_checkpoint SELECT 'scope',id,receivedTransfersJson,savedFileIdsJson,savedTransferIdsJson FROM transfer_history WHERE id='slot'"
            val clear =
                "UPDATE transfer_history SET receivedTransfersJson='{}',savedFileIdsJson='[]',savedTransferIdsJson='[]' WHERE id='slot'"
            db.autoCommit = false
            db.exec(archive)
            db.exec(clear)
            db.rollback()
            assertEquals(original, db.value("SELECT receivedTransfersJson FROM transfer_history"))
            assertNull(db.value("SELECT receivedTransfersJson FROM inbox_legacy_checkpoint"))
            db.exec(archive)
            db.exec(clear)
            db.commit()
            // Restart/replay cannot replace the archived source with now-empty active columns.
            db.exec(archive)
            db.commit()
            assertEquals(
                original,
                db.value("SELECT receivedTransfersJson FROM inbox_legacy_checkpoint"),
            )
            assertEquals("{}", db.value("SELECT receivedTransfersJson FROM transfer_history"))
            db.prepareStatement("SELECT $HISTORY_METADATA_PROJECTION FROM transfer_history").use { q
                ->
                q.executeQuery().use { r ->
                    assertTrue(r.next())
                    assertEquals("secret", r.getString("encryptionKey"))
                    assertEquals("[]", r.getString("savedFileIdsJson"))
                    assertEquals("{}", r.getString("receivedTransfersJson"))
                    assertEquals("pending", r.getString("checkpointState"))
                }
            }
            assertEquals(
                original.take(16384),
                db.value(
                    "SELECT CAST(substr(CAST(receivedTransfersJson AS BLOB),1,16384) AS TEXT) FROM inbox_legacy_checkpoint WHERE scope='scope' AND slotId='slot'"
                ),
            )
            db.exec("INSERT INTO inbox_child_checkpoint VALUES('scope','slot','child',1,7,1,1)")
            db.exec(
                "INSERT INTO inbox_file_checkpoint VALUES('scope','slot','child','blob','content://saved')"
            )
            val plan =
                db.value(
                    "EXPLAIN QUERY PLAN SELECT * FROM inbox_file_checkpoint WHERE scope='scope' AND slotId='slot' AND childId='child' AND blobId='blob'"
                )
            assertNotNull(plan)
            db.exec("DELETE FROM transfer_history WHERE id='slot'")
            db.commit()
            assertNull(db.value("SELECT slotId FROM inbox_legacy_checkpoint"))
            assertNull(db.value("SELECT slotId FROM inbox_child_checkpoint"))
            assertNull(db.value("SELECT slotId FROM inbox_file_checkpoint"))
        }
    }
}
