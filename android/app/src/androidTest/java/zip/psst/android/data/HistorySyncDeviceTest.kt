package zip.psst.android.data

import androidx.room.Room
import androidx.room.withTransaction
import androidx.test.platform.app.InstrumentationRegistry
import zip.psst.shared.api.*
import zip.psst.shared.model.InboxSummary
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test

/** Exercises actual Room transactions/migration-generated queries, not a mock storage facade. */
class HistorySyncDeviceTest {
    private val access = HistoryAccess("https://one.test", "owner")
    private val generation = "11111111-1111-1111-1111-111111111111"

    private fun transfer(n: Int, revision: Long = 1) =
        AuthResourceTransfer(
            id = "%08d-1111-1111-1111-111111111111".format(n),
            status = "complete",
            revision = revision,
            historyAfter = "after_$n",
            historyAfterKind = "kind_$n",
            fileCount = 1,
            totalSize = 1,
            summary = InboxSummary("ready", 1, 1, 1),
            createdAt = java.time.Instant.ofEpochMilli(1_700_000_000_000L + n * 1000).toString(),
            expiresAt = "2099-01-01T00:00:00Z",
        )

    private fun page(
        rows: List<AuthResourceTransfer>,
        cursor: String = "start",
        next: String? = null,
    ) =
        AuthResources(
            transfers = rows,
            paginated = true,
            syncCursor = cursor,
            generation = generation,
            nextCursor = next,
        )

    private fun database() =
        Room.inMemoryDatabaseBuilder(
                InstrumentationRegistry.getInstrumentation().targetContext,
                AppDatabase::class.java,
            )
            .build()

    @Test
    fun quietBatchDoesNotRewritePersistedFactsOrCursor() = runBlocking {
        val db = database()
        try {
            val cache = db.historySyncDao()
            cache.snapshot(page(listOf(transfer(1))), access, "", null)
            fun writes(): Long =
                db.openHelper.writableDatabase.query("SELECT total_changes()").use {
                    it.moveToFirst()
                    it.getLong(0)
                }
            db.withTransaction {
                val before = writes()
                val state = cache.state(access.syncScope())!!
                cache.batch(
                    HistoryChanges(1, generation, emptyList(), state.cursor, false),
                    access,
                    state,
                )
                assertNotNull(cache.cachedPage(access, "", null))
                assertEquals(before, writes())
            }
        } finally {
            db.close()
        }
    }

    @Test
    fun globalBudgetBoundsDormantScopesAndInvalidatesCoverage() = runBlocking {
        val db = database()
        try {
            val cache = db.historySyncDao()
            cache.snapshot(page(listOf(transfer(1))), access, "", null)
            val seed = cache.fact(access.syncScope(), "transfer", transfer(1).id)!!
            db.withTransaction {
                // Dormant accounts also count against the global metadata budget.
                for (n in 1..10_001) cache.writeFact(seed.copy(scope = "dormant_$n"))
            }
            val state = cache.state(access.syncScope())!!
            cache.batch(
                HistoryChanges(1, generation, emptyList(), state.cursor, false),
                access,
                state,
            )
            db.openHelper.readableDatabase.query("SELECT count(*) FROM history_server_facts").use {
                it.moveToFirst()
                assertEquals(10_000L, it.getLong(0))
            }
            assertNull(cache.state(access.syncScope()))
            assertNull(cache.window(access.syncScope(), "", ""))
        } finally {
            db.close()
        }
    }

    @Test
    fun cacheAndCursorCommitTogetherAndRollbackKeepsPrivateMaterial() = runBlocking {
        val db = database()
        try {
            val cache = db.historySyncDao()
            val row = transfer(1)
            db.transferHistoryDao()
                .insert(
                    TransferHistoryEntity(
                        row.id,
                        "sent",
                        1,
                        1,
                        access.serverUrl,
                        "private-key",
                        "complete",
                        accountId = access.accountId,
                    )
                )
            cache.snapshot(page(listOf(row)), access, "", null)
            val initial = cache.state(access.syncScope())!!
            val removed =
                HistoryChanges(
                    1,
                    generation,
                    listOf(HistoryChange("transfer", row.id, 2, "remove")),
                    "next",
                    false,
                )
            try {
                db.withTransaction {
                    cache.batch(removed, access, initial)
                    error("simulate interrupted commit")
                }
            } catch (_: IllegalStateException) {}
            assertEquals(initial, cache.state(access.syncScope()))
            assertEquals(row.id, cache.cachedPage(access, "", null)!!.transfers.single().id)
            cache.batch(removed, access, initial)
            assertTrue(cache.cachedPage(access, "", null)!!.transfers.isEmpty())
            assertEquals("private-key", db.transferHistoryDao().getById(row.id)!!.encryptionKey)
            cache.snapshot(page(listOf(row)), access, "", null)
            assertTrue(
                cache.cachedPage(access, "", null)!!.transfers.isEmpty()
            ) // late snapshot cannot resurrect
        } finally {
            db.close()
        }
    }

    @Test
    fun newRowsDoNotCreatePaginationGapsAndOlderBoundariesStayStable() = runBlocking {
        val db = database()
        try {
            val cache = db.historySyncDao()
            cache.snapshot(page((1..50).map(::transfer), next = "server_older"), access, "", null)
            val initial = cache.state(access.syncScope())!!
            val arrivals =
                (51..70).map { row ->
                    val fact = transfer(row, 2)
                    HistoryChange(
                        "transfer",
                        fact.id,
                        2,
                        "upsert",
                        AuthResources(transfers = listOf(fact)),
                    )
                }
            cache.batch(HistoryChanges(1, generation, arrivals, "after", false), access, initial)
            val first = cache.cachedPage(access, "", null)!!
            assertEquals(50, first.transfers.size)
            assertTrue(first.nextCursor!!.startsWith("local_"))
            assertEquals(
                "after_21",
                cache.window(access.syncScope(), "", first.nextCursor!!)!!.serverAfter,
            )
            val older = cache.cachedPage(access, "", first.nextCursor)!!
            assertEquals(20, older.transfers.size)
            assertEquals("server_older", older.nextCursor)
            assertEquals(70, (first.transfers + older.transfers).map { it.id }.toSet().size)
            val newest = transfer(71, 3)
            cache.batch(
                HistoryChanges(
                    1,
                    generation,
                    listOf(
                        HistoryChange(
                            "transfer",
                            newest.id,
                            3,
                            "upsert",
                            AuthResources(transfers = listOf(newest)),
                        )
                    ),
                    "again",
                    false,
                ),
                access,
                cache.state(access.syncScope())!!,
            )
            assertEquals(
                older.transfers,
                cache.cachedPage(access, "", first.nextCursor)!!.transfers,
            )
        } finally {
            db.close()
        }
    }

    @Test
    fun scopesAndResetNeverExposeAnotherAccountOrDeleteDeviceRows() = runBlocking {
        val db = database()
        try {
            val cache = db.historySyncDao()
            cache.snapshot(page(listOf(transfer(1))), access, "", null)
            assertNull(cache.cachedPage(access.copy(accountId = "other"), "", null))
            assertNull(cache.cachedPage(access.copy(serverUrl = "https://two.test"), "", null))
            db.transferHistoryDao()
                .insert(
                    TransferHistoryEntity(
                        transfer(1).id,
                        "sent",
                        1,
                        1,
                        access.serverUrl,
                        "key",
                        "complete",
                        accountId = access.accountId,
                    )
                )
            cache.snapshot(
                page(emptyList()).copy(generation = "22222222-2222-2222-2222-222222222222"),
                access,
                "",
                null,
                true,
            )
            assertTrue(cache.cachedPage(access, "", null)!!.transfers.isEmpty())
            assertEquals("key", db.transferHistoryDao().getById(transfer(1).id)!!.encryptionKey)
        } finally {
            db.close()
        }
    }

    @Test
    fun cacheBudgetInvalidatesCoverageInsteadOfEvictingPrivateKeys() = runBlocking {
        val db = database()
        try {
            val cache = db.historySyncDao()
            cache.snapshot(page(listOf(transfer(1))), access, "", null)
            val initial = cache.state(access.syncScope())!!
            db.transferHistoryDao()
                .insert(
                    TransferHistoryEntity(
                        transfer(1).id,
                        "sent",
                        1,
                        1,
                        access.serverUrl,
                        "key",
                        "complete",
                        accountId = access.accountId,
                    )
                )
            db.withTransaction {
                repeat(2001) { n ->
                    cache.writeFact(
                        HistoryServerFact(
                            access.syncScope(),
                            "transfer",
                            "extra-$n",
                            generation,
                            1,
                            n.toLong(),
                            "",
                            true,
                        )
                    )
                }
                cache.batch(
                    HistoryChanges(1, generation, emptyList(), "next", false),
                    access,
                    initial,
                )
            }
            assertNull(cache.state(access.syncScope()))
            assertNull(cache.cachedPage(access, "", null))
            db.openHelper.readableDatabase.query("SELECT COUNT(*) FROM history_server_facts").use {
                it.moveToFirst()
                assertEquals(2000, it.getInt(0))
            }
            assertEquals("key", db.transferHistoryDao().getById(transfer(1).id)!!.encryptionKey)
        } finally {
            db.close()
        }
    }

    @Test
    fun migrationFromVersionElevenPreservesKeysAndReceipts() = runBlocking {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val name = "history-sync-migration-test.db"
        context.deleteDatabase(name)
        val model = database()
        val schema = mutableListOf<String>()
        model.openHelper.writableDatabase
            .query(
                "SELECT sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' AND tbl_name NOT LIKE 'history_%' AND name NOT IN ('android_metadata','room_master_table')"
            )
            .use { while (it.moveToNext()) schema.add(it.getString(0)) }
        model.close()
        val file = context.getDatabasePath(name)
        file.parentFile!!.mkdirs()
        android.database.sqlite.SQLiteDatabase.openOrCreateDatabase(file, null).use { old ->
            schema.forEach(old::execSQL)
            old.execSQL(
                "INSERT INTO transfer_history(id,type,fileCount,totalSize,serverUrl,encryptionKey,status,createdAt,accountId,originScope,savedFileIdsJson) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
                arrayOf<Any?>(
                    transfer(1).id,
                    "sent",
                    1,
                    1,
                    access.serverUrl,
                    "key",
                    "complete",
                    1,
                    access.accountId,
                    localHistoryScope(access.serverUrl),
                    "[\"saved-receipt\"]",
                ),
            )
            old.version = 11
        }
        val db =
            Room.databaseBuilder(context, AppDatabase::class.java, name)
                .addMigrations(AppDatabase.MIGRATION_11_12)
                .build()
        try {
            val private = db.transferHistoryDao().getById(transfer(1).id)!!
            assertEquals("key", private.encryptionKey)
            db.openHelper.readableDatabase
                .query("SELECT savedFileIdsJson FROM transfer_history")
                .use {
                    it.moveToFirst()
                    assertEquals("[\"saved-receipt\"]", it.getString(0))
                }
            assertNull(db.historySyncDao().state(access.syncScope()))
        } finally {
            db.close()
            context.deleteDatabase(name)
        }
    }
}
