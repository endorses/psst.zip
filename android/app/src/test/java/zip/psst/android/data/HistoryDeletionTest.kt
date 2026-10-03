package zip.psst.android.data

import androidx.sqlite.db.SupportSQLiteDatabase
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.LinkDeletionException
import zip.psst.shared.model.Transfer
import zip.psst.shared.model.TransferStatus
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import java.lang.reflect.Proxy
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class HistoryDeletionTest {
    private val access = HistoryAccess("http://original.example:8080", "alice", isAdmin = true)

    private fun row(type: String = "sent", token: String? = "owner-secret") =
        TransferHistoryEntity(
            "owned",
            type,
            2,
            42,
            "http://original.example:8080",
            "encryption-secret",
            "complete",
            deletionToken = token,
            accountId = "alice",
        )

    @Test
    fun revokeUsesSavedOriginAndCorrectResourceBeforeLocalRemoval() = runTest {
        for (type in listOf("sent", "received")) {
            for (code in listOf(204, 404)) {
                val original = row(type)
                val events = mutableListOf<String>()
                val dao = MemoryDao(original, events)
                val http =
                    HttpClient(MockEngine) {
                        engine {
                            dispatcher = StandardTestDispatcher(testScheduler)
                            addHandler { request ->
                                assertEquals(original, dao.getById(original.id))
                                assertEquals(HttpMethod.Delete, request.method)
                                assertEquals("original.example", request.url.host)
                                assertEquals(8080, request.url.port)
                                assertEquals(
                                    "/api/v1/${if (type == "received") "slots" else "transfers"}/owned",
                                    request.url.encodedPath,
                                )
                                assertEquals(
                                    "Bearer owner-secret",
                                    request.headers[HttpHeaders.Authorization],
                                )
                                events += "remote"
                                respond("", HttpStatusCode.fromValue(code))
                            }
                        }
                    }
                revokeHistoryEntry(dao, original.id, { access }) { config ->
                    assertEquals(original.serverUrl, config.baseUrl)
                    ApiClient(config, http)
                }
                assertNull(dao.getById(original.id))
                assertEquals(listOf("remote", "local"), events)
            }
        }
    }

    @Test
    fun rejectedLegacyRevocationKeepsRowAndCanRetryWithoutAuthorization() = runTest {
        for (code in listOf(403, 405, 500)) {
            val original = row(token = null)
            val dao = MemoryDao(original)
            var attempts = 0
            suspend fun attempt() {
                val http =
                    HttpClient(MockEngine) {
                        engine {
                            dispatcher = StandardTestDispatcher(testScheduler)
                            addHandler { request ->
                                assertNull(request.headers[HttpHeaders.Authorization])
                                attempts++
                                respond(
                                    "",
                                    HttpStatusCode.fromValue(if (attempts == 1) code else 204),
                                )
                            }
                        }
                    }
                revokeHistoryEntry(dao, original.id, { access }) { ApiClient(it, http) }
            }
            var failure: LinkDeletionException? = null
            try {
                attempt()
            } catch (error: LinkDeletionException) {
                failure = error
            }
            assertEquals(code, failure?.statusCode)
            if (code == 403) assertTrue(failure?.message.orEmpty().contains("older transfer"))
            assertEquals(original, dao.getById(original.id))
            attempt()
            assertNull(dao.getById(original.id))
        }
    }

    @Test
    fun offlineAndCancelledDeletionPreserveLocalHistory() = runTest {
        for (cancel in listOf(false, true)) {
            val original = row()
            val dao = MemoryDao(original)
            val http =
                HttpClient(MockEngine) {
                    engine {
                        dispatcher = StandardTestDispatcher(testScheduler)
                        addHandler {
                            if (cancel) throw CancellationException("Cancelled")
                            else error("Offline")
                        }
                    }
                }
            var failure: Exception? = null
            try {
                revokeHistoryEntry(dao, original.id, { access }) { ApiClient(it, http) }
            } catch (error: Exception) {
                failure = error
            }
            assertTrue(failure != null)
            if (cancel) assertTrue(failure is CancellationException)
            assertEquals(original, dao.getById(original.id))
        }
    }

    @Test
    fun refreshPreservesTokenAndFailedUploadStatusUntilCompleted() {
        val failed = row().copy(status = "failed")
        val refreshed = mergeSentHistory(failed, Transfer("owned"))
        assertEquals("owner-secret", refreshed.deletionToken)
        assertEquals("failed", refreshed.status)
        assertEquals("Upload failed", historyStatusLabel("sent", refreshed.status))
        assertEquals(
            "complete",
            mergeSentHistory(refreshed, Transfer("owned", status = TransferStatus.COMPLETE)).status,
        )
        val received = mergeReceivedHistory(row("received"), ReceivedSnapshot(emptyMap()))
        assertEquals("owner-secret", received.deletionToken)
    }

    @Test
    fun migrationChainAddsNullableTokenWithoutReplacingHistory() {
        val sql = mutableListOf<String>()
        val database =
            Proxy.newProxyInstance(
                SupportSQLiteDatabase::class.java.classLoader,
                arrayOf(SupportSQLiteDatabase::class.java),
            ) { _, method, arguments ->
                check(method.name == "execSQL")
                sql += arguments!![0] as String
                null
            } as SupportSQLiteDatabase
        assertEquals(1, AppDatabase.MIGRATION_1_2.startVersion)
        assertEquals(2, AppDatabase.MIGRATION_1_2.endVersion)
        assertEquals(2, AppDatabase.MIGRATION_2_3.startVersion)
        assertEquals(3, AppDatabase.MIGRATION_2_3.endVersion)
        AppDatabase.MIGRATION_1_2.migrate(database)
        AppDatabase.MIGRATION_2_3.migrate(database)
        assertEquals(3, AppDatabase.MIGRATION_3_4.startVersion)
        assertEquals(4, AppDatabase.MIGRATION_3_4.endVersion)
        AppDatabase.MIGRATION_3_4.migrate(database)
        assertEquals(4, sql.size)
        assertTrue(sql.all { it.startsWith("ALTER TABLE transfer_history ADD COLUMN ") })
        assertEquals("ALTER TABLE transfer_history ADD COLUMN deletionToken TEXT", sql[2])
        assertEquals("ALTER TABLE transfer_history ADD COLUMN accountId TEXT", sql.last())
        assertNull(row().copy(accountId = null).accountId)
        assertNull(row(token = null).deletionToken)
    }

    @Test
    fun anotherAccountCannotUseStoredDeletionCapability() = runTest {
        val original = row()
        for (scope in
            listOf(
                HistoryAccess(),
                HistoryAccess(original.serverUrl, "bob"),
                HistoryAccess("https://other.example", "alice"),
            )) {
            val dao = MemoryDao(original)
            var requested = false
            var denied = false
            try {
                revokeHistoryEntry(dao, original.id, { scope }) {
                    requested = true
                    error("Must not create a client for inaccessible history")
                }
            } catch (_: IllegalArgumentException) {
                denied = true
            }
            assertTrue(denied)
            assertTrue(!requested)
            assertEquals(original, dao.getById(original.id))
        }
    }

    @Test
    fun accountSwitchDuringRevocationKeepsOtherAccountHistory() = runTest {
        val original = row()
        val dao = MemoryDao(original)
        var current = access
        val http =
            HttpClient(MockEngine) {
                engine {
                    dispatcher = StandardTestDispatcher(testScheduler)
                    addHandler {
                        current = HistoryAccess(original.serverUrl, "bob")
                        respond("", HttpStatusCode.NoContent)
                    }
                }
            }
        var denied = false
        try {
            revokeHistoryEntry(dao, original.id, { current }) { ApiClient(it, http) }
        } catch (_: IllegalArgumentException) {
            denied = true
        }
        assertTrue(denied)
        assertEquals(original, dao.getById(original.id))
    }

    private class MemoryDao(
        initial: TransferHistoryEntity,
        private val events: MutableList<String> = mutableListOf(),
    ) : TransferHistoryDao {
        private val rows = MutableStateFlow(listOf(initial))

        override fun getAll(): Flow<List<TransferHistoryEntity>> = rows

        override suspend fun getById(id: String) = rows.value.find { it.id == id }

        override suspend fun insert(entity: TransferHistoryEntity) {
            rows.value += entity
        }

        override suspend fun update(entity: TransferHistoryEntity) {
            rows.value = rows.value.map { if (it.id == entity.id) entity else it }
        }

        override suspend fun updateStatus(id: String, status: String) {
            rows.value = rows.value.map { if (it.id == id) it.copy(status = status) else it }
        }

        override suspend fun delete(id: String) {
            events += "local"
            rows.value = rows.value.filterNot { it.id == id }
        }
    }
}
