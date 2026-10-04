package zip.psst.android.data

import androidx.sqlite.db.SupportSQLiteDatabase
import zip.psst.shared.api.ApiClient
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
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class HistoryDeletionTest {
    private val access = HistoryAccess("http://original.example:8080", "alice")

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
    fun remoteHistoryWithoutKeyOrDeletionCapabilityUsesOwnerSession() = runTest {
        val original = row(token = null).copy(encryptionKey = "")
        val dao = MemoryDao(original)
        val http =
            HttpClient(MockEngine) {
                engine {
                    dispatcher = StandardTestDispatcher(testScheduler)
                    addHandler { request ->
                        assertEquals(
                            "Bearer account-session",
                            request.headers[HttpHeaders.Authorization],
                        )
                        assertEquals("original.example", request.url.host)
                        respond("", HttpStatusCode.NoContent)
                    }
                }
            }
        revokeHistoryEntry(dao, original.id, { access }) { config ->
            ApiClient(config, http, "account-session")
        }
        assertNull(dao.getById(original.id))
    }

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
    fun administrationOnlyAccountCannotUseLegacyPersonalHistoryRevocation() = runTest {
        val original = row(token = null).copy(accountId = null)
        val dao = MemoryDao(original)
        var requests = 0
        var denied = false
        try {
            revokeHistoryEntry(dao, original.id, { access.copy(isAdmin = true) }) {
                requests++
                error("Should be denied before creating a client")
            }
        } catch (_: IllegalArgumentException) {
            denied = true
        }
        assertTrue(denied)
        assertEquals(0, requests)
        assertEquals(original, dao.getById(original.id))
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
        AppDatabase.MIGRATION_6_7.migrate(database)
        assertEquals(
            "ALTER TABLE transfer_history ADD COLUMN summaryUpdating INTEGER NOT NULL DEFAULT 0",
            sql.last(),
        )
        assertFalse(row().summaryUpdating)
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
    ) : CheckpointTestDao() {
        private val rows = MutableStateFlow(listOf(initial))

        override fun observePage(
            ids: List<String>,
            serverUrl: String,
            accountId: String,
        ): Flow<List<TransferHistoryEntity>> =
            getAll().let { source ->
                source.map { rows ->
                    rows.filter {
                        it.id in ids && it.serverUrl == serverUrl && it.accountId == accountId
                    }
                }
            }

        private fun getAll(): Flow<List<TransferHistoryEntity>> = rows

        override fun hasLegacy(): Flow<Boolean> =
            rows.map { all -> all.any { it.accountId == null } }

        override fun observeLocalPage(
            accountId: String,
            originScope: String,
            beforeTime: Long,
            beforeId: String,
        ): Flow<List<TransferHistoryEntity>> =
            rows.map { all ->
                all.filter {
                        it.accountId == accountId &&
                            it.originScope == originScope &&
                            (it.createdAt < beforeTime ||
                                (it.createdAt == beforeTime && it.id < beforeId))
                    }
                    .sortedWith(
                        compareByDescending<TransferHistoryEntity> { it.createdAt }
                            .thenByDescending { it.id }
                    )
                    .take(51)
            }

        override suspend fun getById(id: String) = rows.value.find { it.id == id }

        override suspend fun insert(entity: TransferHistoryEntity) {
            rows.value += entity
        }

        override suspend fun update(entity: TransferHistoryEntity) {
            rows.value = rows.value.map { if (it.id == entity.id) entity else it }
        }

        override suspend fun rename(
            id: String,
            serverUrl: String,
            accountId: String,
            type: String,
            title: String?,
        ) {
            rows.value =
                rows.value.map {
                    if (
                        it.id == id &&
                            it.serverUrl == serverUrl &&
                            it.accountId == accountId &&
                            it.type == type
                    )
                        it.copy(title = title)
                    else it
                }
        }

        override suspend fun setTitleIfEmpty(id: String, title: String) {
            rows.value =
                rows.value.map {
                    if (it.id == id && it.automaticTitle == null) it.copy(automaticTitle = title)
                    else it
                }
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
