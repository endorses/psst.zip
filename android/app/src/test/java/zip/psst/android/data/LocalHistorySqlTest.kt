package zip.psst.android.data

import java.sql.Connection
import java.sql.DriverManager
import org.junit.Assert.*
import org.junit.Test

/** Exercise the production SQL against SQLite; Android lifecycle/Keystore needs a device. */
class LocalHistorySqlTest {
    private fun database(): Connection = DriverManager.getConnection("jdbc:sqlite::memory:")

    private fun Connection.exec(sql: String, vararg args: Any?) =
        prepareStatement(sql).use { statement ->
            args.forEachIndexed { index, value -> statement.setObject(index + 1, value) }
            statement.executeUpdate()
            Unit
        }

    private fun Connection.query(sql: String, vararg args: Any?): List<List<String?>> =
        prepareStatement(sql).use { statement ->
            args.forEachIndexed { index, value -> statement.setObject(index + 1, value) }
            statement.executeQuery().use { result ->
                buildList {
                    while (result.next()) add(
                        (1..result.metaData.columnCount).map(result::getString)
                    )
                }
            }
        }

    @Test
    fun filterSeeksMatchingRowsBeyondFirstPageAndKeepsAccountScope() =
        database().use { db ->
            db.exec(
                "CREATE TABLE transfer_history(id TEXT PRIMARY KEY,accountId TEXT,originScope TEXT,type TEXT,createdAt INTEGER)"
            )
            db.exec(ACCOUNT_FILTERED_INDEX_SQL)
            repeat(150) {
                db.exec(
                    "INSERT INTO transfer_history VALUES(?,?,?,?,?)",
                    "id-$it",
                    "owner",
                    "https://host",
                    if (it < 10) "received" else "sent",
                    it,
                )
            }
            db.exec(
                "INSERT INTO transfer_history VALUES('foreign','other','https://host','received',999)"
            )
            val sql =
                ACCOUNT_FILTERED_PAGE_SQL.replace(HISTORY_METADATA_PROJECTION, "id,type")
                    .replace(":accountId", "?")
                    .replace(":originScope", "?")
                    .replace(":kind", "?")
                    .replace(":beforeTime", "?")
                    .replace(":beforeId", "?")
            val rows = db.query(sql, "owner", "https://host", "received", Long.MAX_VALUE, "\uffff")
            assertEquals(10, rows.size)
            assertTrue(rows.all { it[1] == "received" && it[0] != "foreign" })
            val plan =
                db.query(
                        "EXPLAIN QUERY PLAN $sql",
                        "owner",
                        "https://host",
                        "received",
                        Long.MAX_VALUE,
                        "\uffff",
                    )
                    .flatten()
                    .joinToString()
            assertTrue(
                plan.contains("index_transfer_history_accountId_originScope_type_createdAt_id")
            )
            assertFalse(plan.contains("TEMP B-TREE"))
        }

    @Test
    fun accountMigrationAndSeekKeepKeysReceiptsAndAccountIsolation() =
        database().use { db ->
            db.exec(
                "CREATE TABLE transfer_history(id TEXT PRIMARY KEY, accountId TEXT, serverUrl TEXT, createdAt INTEGER, encryptionKey TEXT, receivedTransfersJson TEXT)"
            )
            repeat(203) { number ->
                db.exec(
                    "INSERT INTO transfer_history VALUES(?,?,?,?,?,?)",
                    "%04d".format(number),
                    "owner",
                    "HTTPS://Example.COM/",
                    number / 3,
                    "key-$number",
                    "saved-$number",
                )
            }
            db.exec(
                "INSERT INTO transfer_history VALUES('foreign','other','HTTPS://Example.COM/',9999,'foreign-key','receipt')"
            )
            db.exec(
                "INSERT INTO transfer_history VALUES('other-origin','owner','https://other.example',9999,'foreign-key','receipt')"
            )
            db.exec(
                "INSERT INTO transfer_history VALUES('legacy',NULL,'HTTPS://Example.COM/',9999,'legacy-key','receipt')"
            )
            db.exec(ACCOUNT_SCOPE_COLUMN_SQL)
            db.exec(ACCOUNT_SCOPE_BACKFILL_SQL)
            db.exec(ACCOUNT_PAGE_INDEX_SQL)
            val sql =
                ACCOUNT_LOCAL_PAGE_SQL.replace(
                        HISTORY_METADATA_PROJECTION,
                        "id,accountId,serverUrl,createdAt,encryptionKey,receivedTransfersJson",
                    )
                    .replace(":accountId", "?")
                    .replace(":originScope", "?")
                    .replace(":beforeTime", "?")
                    .replace(":beforeId", "?")
            val first = db.query(sql, "owner", "https://example.com", Long.MAX_VALUE, "\uffff")
            assertEquals(51, first.size)
            val anchor = first[49]
            db.exec("DELETE FROM transfer_history WHERE id=?", anchor[0])
            db.exec(
                "INSERT INTO transfer_history VALUES('new','owner','HTTPS://Example.COM/',9999,'new-key','receipt','https://example.com')"
            )
            val next = db.query(sql, "owner", "https://example.com", anchor[3], anchor[0])
            assertEquals(51, next.size)
            assertTrue(next.none { row -> first.take(50).any { it[0] == row[0] } })
            assertTrue(
                next.all {
                    it[1] == "owner" &&
                        it[2] == "HTTPS://Example.COM/" &&
                        it[4] == "key-${it[0]!!.toInt()}" &&
                        it[5] == "saved-${it[0]!!.toInt()}"
                }
            )
            val plan =
                db.query(
                        "EXPLAIN QUERY PLAN $sql",
                        "owner",
                        "https://example.com",
                        anchor[3],
                        anchor[0],
                    )
                    .flatten()
                    .joinToString()
            assertTrue(plan.contains("index_transfer_history_accountId_originScope_createdAt_id"))
            assertFalse(plan.contains("TEMP B-TREE"))
            assertEquals(
                "legacy-key",
                db.query("SELECT encryptionKey FROM transfer_history WHERE id='legacy'")
                    .single()
                    .single(),
            )
        }

    @Test
    fun importRestartCannotOverwriteNewCheckpointOrResurrectDeletion() =
        database().use { db ->
            GUEST_HISTORY_SCHEMA.forEach { db.exec(it) }
            db.exec(GUEST_IMPORT_SQL, "json", "a", 2, 1, 0, "old receipt")
            db.exec(GUEST_SAVE_SQL, "json", "a", 2, 0, 0, "new saved paths")
            db.exec(GUEST_IMPORT_SQL, "json", "a", 2, 1, 0, "old receipt")
            assertEquals("new saved paths", db.query(GUEST_FIRST_PAGE_SQL).single()[2])
            db.exec(GUEST_DELETE_SQL, "json", "a")
            db.exec(GUEST_IMPORT_SQL, "json", "a", 2, 1, 0, "old receipt")
            assertTrue(db.query(GUEST_FIRST_PAGE_SQL).isEmpty())
            db.exec(GUEST_SAVE_SQL, "json", "a", 3, 0, 0, "explicit reopening")
            assertEquals("explicit reopening", db.query(GUEST_FIRST_PAGE_SQL).single()[2])
        }

    @Test
    fun guestPagesCoverEqualTimestampsAndCorruptPlaceholdersExactlyOnce() =
        database().use { db ->
            GUEST_HISTORY_SCHEMA.forEach { db.exec(it) }
            repeat(1203) { n ->
                db.exec(
                    GUEST_IMPORT_SQL,
                    "json",
                    "%04d".format(n),
                    42,
                    0,
                    if (n % 7 == 0) 1 else 0,
                    if (n % 7 == 0) null else "saved-$n",
                )
            }
            db.exec(GUEST_SAVE_SQL, "upload", "upload-a", 9999, 0, 0, "cleanup")
            var rows = db.query(GUEST_FIRST_PAGE_SQL)
            val seen = mutableSetOf<String>()
            do {
                assertTrue(rows.size <= 51)
                rows.take(50).forEach { assertTrue(seen.add(it[0]!!)) }
                if (rows.size <= 50) break
                val anchor = rows[49]
                rows = db.query(GUEST_NEXT_PAGE_SQL, anchor[1], anchor[0])
            } while (true)
            assertEquals(1203, seen.size)
            val plan =
                db.query("EXPLAIN QUERY PLAN $GUEST_NEXT_PAGE_SQL", 42, "0500")
                    .flatten()
                    .joinToString()
            assertTrue(plan.contains("guest_history_page"))
            assertFalse(plan.contains("TEMP B-TREE"))
        }
}
