package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.SlotTransfer
import zip.psst.shared.model.Transfer
import zip.psst.shared.model.TransferStatus
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class HistoryTest {
    private fun row(status: String = "waiting") =
        TransferHistoryEntity(
            id = "slot",
            type = "received",
            fileCount = 0,
            totalSize = 0,
            serverUrl = "http://original.example:8080",
            encryptionKey = "",
            status = status,
        )

    private fun snapshot(id: String, count: Int = 1, size: Long? = null) =
        ReceivedSnapshot(mapOf(id to ReceivedChild(count, size)))

    @Test
    fun completedSummaryExcludesPendingChildren() {
        val slot =
            DropSlot(
                "slot",
                transfers =
                    listOf(
                        SlotTransfer("ready", TransferStatus.COMPLETE, 2),
                        SlotTransfer("pending", TransferStatus.PENDING, 9),
                    ),
            )
        val result = mergeReceivedHistory(row(), slot.receivedSnapshot())
        assertEquals(2, result.fileCount)
        assertEquals("has_uploads", result.status)
        assertTrue(result.savedTransferIds().isEmpty())
    }

    @Test
    fun savedStatusAndBytesSurviveDelayedPolls() {
        val saved = mergeReceivedHistory(row(), snapshot("a", 2, 42), saved = true)
        val polled = mergeReceivedHistory(saved, snapshot("a", 2))
        assertEquals("complete", polled.status)
        assertEquals(42L, polled.totalSize)
        assertEquals(2, polled.fileCount)
        assertEquals(saved.savedTransferIds(), polled.savedTransferIds())
    }

    @Test
    fun replacementChildWithSameCountIsNotFalselySaved() {
        val savedA = mergeReceivedHistory(row(), snapshot("a", 1, 10), saved = true)
        val newB = mergeReceivedHistory(savedA, snapshot("b", 1, 20))
        assertEquals("has_uploads", newB.status)
        assertEquals(2, newB.fileCount)
        assertEquals(30L, newB.totalSize)
        assertFalse("b" in newB.savedTransferIds())
        val delayed = mergeReceivedHistory(newB, snapshot("a"))
        assertEquals("has_uploads", delayed.status)
        val savedB = mergeReceivedHistory(delayed, snapshot("b", 1, 20), saved = true)
        assertEquals("complete", savedB.status)
    }

    @Test
    fun successfulChildSurvivesLaterChildFailureWithoutCompletingSlot() {
        val known =
            mergeReceivedHistory(
                row(),
                ReceivedSnapshot(mapOf("a" to ReceivedChild(1), "b" to ReceivedChild(1))),
            )
        val savedA = mergeReceivedHistory(known, snapshot("a", 1, 10), saved = true)
        assertEquals(setOf("a"), savedA.savedTransferIds())
        assertEquals("has_uploads", savedA.status)
        assertEquals("has_uploads", mergeReceivedHistory(savedA, snapshot("a")).status)
        assertEquals(
            "complete",
            mergeReceivedHistory(savedA, snapshot("b", 1, 20), saved = true).status,
        )
    }

    @Test
    fun legacyBackfillDoesNotInventSavedConfirmation() {
        val available = mergeReceivedHistory(row("has_uploads"), snapshot("a", 3, 90))
        assertEquals(3, available.fileCount)
        assertEquals(90L, available.totalSize)
        assertEquals("has_uploads", available.status)
        assertEquals(
            "complete",
            mergeReceivedHistory(
                    row("complete").copy(fileCount = 3, totalSize = 90),
                    ReceivedSnapshot(emptyMap()),
                )
                .status,
        )
    }

    @Test
    fun unknownLegacyFilesCannotBeConfirmedBySavingOnlyReplacement() {
        val legacy = row("has_uploads").copy(fileCount = 3)
        val savedReplacement = mergeReceivedHistory(legacy, snapshot("new", 1, 10), saved = true)
        assertEquals(3, savedReplacement.fileCount)
        assertEquals("has_uploads", savedReplacement.status)
    }

    @Test
    fun senderShowsAvailabilityAndDownloadActivityWithoutReceiptClaim() {
        val sent = row("complete").copy(type = "sent", fileCount = 2, totalSize = 42)
        assertEquals("Ready to download", historyStatusLabel("sent", sent.status).english())
        val started =
            mergeSentHistory(
                sent,
                Transfer("slot", status = TransferStatus.COMPLETE, downloadCount = 1),
            )
        assertEquals("Download started", historyStatusLabel("sent", started.status).english())
        assertEquals(
            "download_started",
            mergeSentHistory(started, Transfer("slot", status = TransferStatus.COMPLETE)).status,
        )
        assertEquals("Saved", historyStatusLabel("received", "complete").english())
        assertEquals("Uploads received", historyStatusLabel("received", "has_uploads").english())
    }

    @Test
    fun explicitAcknowledgementWinsOverStartedAndStaleResponses() {
        val sent = row("download_started").copy(type = "sent")
        val downloaded =
            mergeSentHistory(
                sent,
                Transfer(
                    "slot",
                    status = TransferStatus.COMPLETE,
                    downloadedAt = "2026-10-03T16:00:00Z",
                ),
            )
        assertEquals("Downloaded", historyStatusLabel("sent", downloaded.status).english())
        assertEquals(
            "downloaded",
            mergeSentHistory(
                    downloaded,
                    Transfer("slot", status = TransferStatus.COMPLETE, downloadCount = 1),
                )
                .status,
        )
        assertEquals(
            "downloaded",
            mergeSentHistory(downloaded, Transfer("slot", status = TransferStatus.COMPLETE)).status,
        )
    }

    @Test
    fun refreshUsesBoundedMetadataPageWithoutDownloadingManifests() = runTest {
        val slot = "11111111-1111-1111-1111-111111111111"
        val child = "22222222-2222-2222-2222-222222222222"
        val dao = MemoryDao(row("has_uploads").copy(id = slot))
        val paths = mutableListOf<String>()
        val http =
            HttpClient(MockEngine) {
                engine {
                    dispatcher = StandardTestDispatcher(testScheduler)
                    addHandler { request ->
                        assertEquals(HttpMethod.Get, request.method)
                        assertEquals("original.example", request.url.host)
                        paths += request.url.encodedPath
                        assertEquals("/api/v1/slots/$slot/inbox", request.url.encodedPath)
                        respond(
                            """{"id":"$slot","status":"has_uploads","receive_protocol":1,"recipient_public_key":"","max_files":0,"reserved_files":0,"remaining_files":null,"paginated":true,"next_cursor":"later","summary":{"state":"ready","completed_files":400,"file_count":500,"total_size":60000},"transfers":[{"transfer_id":"$child","status":"complete","file_count":1}]}""",
                            headers = headersOf(HttpHeaders.ContentType, "application/json"),
                        )
                    }
                }
                install(ContentNegotiation) { json() }
            }
        val updated =
            refreshHistoryEntry(dao, slot) { config ->
                assertEquals("http://original.example:8080", config.baseUrl)
                ApiClient(config, http)
            }!!
        assertEquals(400, updated.fileCount)
        assertEquals(0L, updated.totalSize)
        assertEquals("has_uploads", updated.status)
        assertEquals(1, paths.size)
    }

    @Test
    fun offlineRefreshPreservesKnownSavedFacts() = runTest {
        val saved = mergeReceivedHistory(row(), snapshot("a", 2, 42), saved = true)
        val dao = MemoryDao(saved)
        val http =
            HttpClient(MockEngine) {
                engine {
                    dispatcher = StandardTestDispatcher(testScheduler)
                    addHandler { error("Offline") }
                }
            }
        assertEquals(saved, refreshHistoryEntry(dao, "slot") { ApiClient(it, http) })
    }

    @Test
    fun durablePerFileSavesSurviveStaleMetadataAndDuplicateRecording() =
        kotlinx.coroutines.test.runTest {
            val row =
                TransferHistoryEntity("slot", "received", 0, 0, "https://host", "key", "waiting")
            val dao = MemoryDao(row)
            dao.recordSavedFile("slot", "child/blob-a")
            dao.recordSavedFile("slot", "child/blob-a")
            dao.mergeReceived("slot", ReceivedSnapshot(mapOf("child" to ReceivedChild(2))))
            val stored = dao.getById("slot")!!
            assertEquals(setOf("child/blob-a"), dao.savedFiles(stored, listOf("child")))
            assertEquals(1L, stored.checkpointSavedFiles)
            assertEquals("has_uploads", stored.status)
        }

    @Test
    fun legacySavedChildrenStillReportTheirSavedFileCount() {
        val row = TransferHistoryEntity("slot", "received", 0, 0, "https://host", "key", "waiting")
        val saved =
            mergeReceivedHistory(
                row,
                ReceivedSnapshot(mapOf("child" to ReceivedChild(3, 99))),
                saved = true,
            )
        assertEquals(3, saved.savedFileCount())
    }

    @Test
    fun absenceVerificationMarksOnlyConfirmedMissingLinksUnavailable() =
        kotlinx.coroutines.test.runTest {
            for (missing in listOf(true, false)) {
                val row =
                    TransferHistoryEntity(
                        "sent",
                        "sent",
                        1,
                        12,
                        "https://host",
                        "",
                        "complete",
                        accountId = "alice",
                    )
                val dao = MemoryDao(row)
                val http =
                    io.ktor.client.HttpClient(io.ktor.client.engine.mock.MockEngine) {
                        expectSuccess = true
                        engine {
                            dispatcher =
                                kotlinx.coroutines.test.StandardTestDispatcher(testScheduler)
                            addHandler {
                                if (!missing) throw java.io.IOException("Offline")
                                respond("", io.ktor.http.HttpStatusCode.NotFound)
                            }
                        }
                    }
                var failed = false
                try {
                    refreshHistoryEntry(dao, row.id, reportFailure = true) {
                        zip.psst.shared.api.ApiClient(it, http)
                    }
                } catch (_: java.io.IOException) {
                    failed = true
                }
                assertEquals(
                    if (missing) "unavailable" else "complete",
                    dao.getById(row.id)?.status,
                )
                assertEquals(!missing, failed)
            }
        }

    @Test
    fun partialAccountPagesNeverRevokeUnloadedRowsAndRejectCredentialChanges() = runTest {
        val scope = HistoryAccess("https://host", "alice", credentialVersion = 1)
        val unseen =
            row("complete")
                .copy(
                    id = "unseen",
                    serverUrl = scope.serverUrl,
                    accountId = scope.accountId,
                    encryptionKey = "local-key",
                    savedFileIdsJson = "[\"child/blob\"]",
                )
        val dao = MemoryDao(unseen)
        val page =
            zip.psst.shared.api.AuthResources(
                slots =
                    listOf(
                        zip.psst.shared.api.AuthResourceSlot(
                            "shown",
                            "waiting",
                            fileCount = 0,
                            completedFiles = 0,
                            totalSize = 0,
                            summary = zip.psst.shared.model.InboxSummary("ready", 0, 0, 0),
                        )
                    ),
                nextCursor = "later",
                paginated = true,
            )
        syncAccountHistory(dao, page, scope) { scope }
        assertEquals(unseen, dao.getById("unseen"))
        assertEquals("", dao.getById("shown")!!.encryptionKey)
        var canceled = false
        try {
            syncAccountHistory(
                dao,
                page.copy(slots = listOf(page.slots.single().copy(id = "stale"))),
                scope,
            ) {
                scope.copy(credentialVersion = 2)
            }
        } catch (_: kotlinx.coroutines.CancellationException) {
            canceled = true
        }
        assertTrue(canceled)
        assertEquals(null, dao.getById("stale"))
        assertEquals(unseen, dao.getById("unseen"))
    }

    internal open class MemoryDao(initial: TransferHistoryEntity) : CheckpointTestDao() {
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

        override suspend fun insert(entity: TransferHistoryEntity) {
            rows.value += entity
        }

        override suspend fun delete(id: String) {
            rows.value = rows.value.filterNot { it.id == id }
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

        override suspend fun getById(id: String) = rows.value.find { it.id == id }
    }
}
