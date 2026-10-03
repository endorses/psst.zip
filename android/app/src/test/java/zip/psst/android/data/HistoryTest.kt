package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.CryptoProvider
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
import kotlin.io.encoding.Base64
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
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
        assertEquals("Ready to download", historyStatusLabel("sent", sent.status))
        val started =
            mergeSentHistory(
                sent,
                Transfer("slot", status = TransferStatus.COMPLETE, downloadCount = 1),
            )
        assertEquals("Download started", historyStatusLabel("sent", started.status))
        assertEquals(
            "download_started",
            mergeSentHistory(started, Transfer("slot", status = TransferStatus.COMPLETE)).status,
        )
        assertEquals("Saved", historyStatusLabel("received", "complete"))
        assertEquals("Uploads received", historyStatusLabel("received", "has_uploads"))
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
        assertEquals("Downloaded", historyStatusLabel("sent", downloaded.status))
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
    fun refreshUsesStoredOriginAndEncryptedManifestWithoutDownloadingFiles() = runTest {
        val key = ByteArray(32) { it.toByte() }
        val nonce = ByteArray(12) { it.toByte() }
        val manifest =
            """{"files":[{"name":"test.txt","size":7,"blob_id":"blob"}]}""".encodeToByteArray()
        val encrypted = nonce + CryptoProvider.encrypt(key, nonce, manifest)
        val dao =
            MemoryDao(
                row("has_uploads")
                    .copy(
                        encryptionKey =
                            Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).encode(key)
                    )
            )
        val paths = mutableListOf<String>()
        val http =
            HttpClient(MockEngine) {
                engine {
                    dispatcher = StandardTestDispatcher(testScheduler)
                    addHandler { request ->
                        assertEquals(HttpMethod.Get, request.method)
                        assertEquals("original.example", request.url.host)
                        paths += request.url.encodedPath
                        when (request.url.encodedPath) {
                            "/api/v1/slots/slot" ->
                                respond(
                                    """{"id":"slot","status":"has_uploads","transfers":[{"transfer_id":"a","status":"complete","file_count":1}]}""",
                                    headers = headersOf(HttpHeaders.ContentType, "application/json"),
                                )
                            "/api/v1/transfers/a/manifest" -> respond(encrypted)
                            else -> error("File blobs must not be downloaded by history")
                        }
                    }
                }
                install(ContentNegotiation) { json() }
            }
        val updated =
            refreshHistoryEntry(dao, "slot") { config ->
                assertEquals("http://original.example:8080", config.baseUrl)
                ApiClient(config, http)
            }!!
        assertEquals(1, updated.fileCount)
        assertEquals(7L, updated.totalSize)
        assertEquals("has_uploads", updated.status)
        assertEquals(2, paths.size)
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
            assertEquals(
                setOf("child/blob-a"),
                kotlinx.serialization.json.Json.decodeFromString<Set<String>>(
                    stored.savedFileIdsJson
                ),
            )
            assertEquals(1, stored.savedFileCount())
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

    private class MemoryDao(initial: TransferHistoryEntity) : TransferHistoryDao {
        private val rows = MutableStateFlow(listOf(initial))

        override fun getAll(): Flow<List<TransferHistoryEntity>> = rows

        override suspend fun insert(entity: TransferHistoryEntity) {
            rows.value += entity
        }

        override suspend fun delete(id: String) {
            rows.value = rows.value.filterNot { it.id == id }
        }

        override suspend fun update(entity: TransferHistoryEntity) {
            rows.value = rows.value.map { if (it.id == entity.id) entity else it }
        }

        override suspend fun setTitleIfEmpty(id: String, title: String) {
            rows.value =
                rows.value.map {
                    if (it.id == id && it.title == null) it.copy(title = title) else it
                }
        }

        override suspend fun updateStatus(id: String, status: String) {
            rows.value = rows.value.map { if (it.id == id) it.copy(status = status) else it }
        }

        override suspend fun getById(id: String) = rows.value.find { it.id == id }
    }
}
