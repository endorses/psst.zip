package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.HttpRequestData
import io.ktor.client.request.HttpResponseData
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DownloadAcknowledgementTest {
    private val key = ByteArray(32) { it.toByte() }
    private val nonce = ByteArray(12) { it.toByte() }
    private val data = byteArrayOf(1, 2, 3)

    private val encryptionId = "1".repeat(32)

    private fun file(name: String, size: Long, blobId: String) =
        FileMetadata(
            name,
            size,
            blobId = blobId,
            encoding = "chunked-v1",
            chunkSize = 4194304,
            encryptionId = encryptionId,
        )

    private fun encrypted() = ChunkedFileCrypto.encrypt(key, encryptionId, 3, 0, data)

    private fun row() =
        TransferHistoryEntity("slot", "received", 0, 0, "https://example.com", "", "waiting")

    private fun TestScope.client(
        handler: suspend MockRequestHandleScope.(HttpRequestData) -> HttpResponseData
    ): ApiClient =
        ApiClient(
            ServerConfig("https://example.com"),
            HttpClient(MockEngine) {
                engine {
                    dispatcher = StandardTestDispatcher(testScheduler)
                    addHandler(handler)
                }
            },
        )

    @Test
    fun acknowledgementHappensAfterAllFilesAreWrittenAndSaveIsPersisted() = runTest {
        val events = mutableListOf<String>()
        var saved = row()
        val client = client { request ->
            if (request.method == HttpMethod.Post) {
                assertTrue("child" in saved.savedTransferIds())
                events += "ack"
                respond("", HttpStatusCode.NoContent)
            } else {
                events += "get"
                respond(encrypted())
            }
        }
        try {
            receiveAndSaveChild(
                client,
                "child",
                listOf(file("one", 3, blobId = "a"), file("two", 3, blobId = "b")),
                key,
                saveFile = { _, content ->
                    content {}
                    events += "write"
                },
                recordSaved = { child ->
                    events += "persist"
                    saved =
                        mergeReceivedHistory(
                            saved,
                            ReceivedSnapshot(mapOf("child" to child)),
                            saved = true,
                        )
                },
            )
            assertEquals(listOf("get", "write", "get", "write", "persist", "ack"), events)
            assertEquals(6L, saved.totalSize)
        } finally {
            client.close()
        }
    }

    @Test
    fun partialDownloadAuthenticationSizeAndWriteFailuresNeverAcknowledge() = runTest {
        for (failure in listOf("network", "authentication", "size", "write")) {
            var requests = 0
            var acknowledgements = 0
            var writes = 0
            var recorded = false
            val client = client { request ->
                if (request.method == HttpMethod.Post) {
                    acknowledgements++
                    respond("", HttpStatusCode.NoContent)
                } else {
                    requests++
                    if (requests == 2 && failure == "network") error("Download interrupted")
                    val body = encrypted()
                    if (requests == 2 && failure == "authentication")
                        body[body.lastIndex] = (body.last().toInt() xor 1).toByte()
                    respond(body)
                }
            }
            try {
                var failed = false
                try {
                    receiveAndSaveChild(
                        client,
                        "child",
                        listOf(
                            file("one", 3, blobId = "a"),
                            file("two", if (failure == "size") 4 else 3, blobId = "b"),
                        ),
                        key,
                        saveFile = { _, content ->
                            content {}
                            writes++
                            if (writes == 2 && failure == "write") error("Disk full")
                        },
                        recordSaved = { recorded = true },
                    )
                } catch (_: Exception) {
                    failed = true
                }
                assertTrue(failure, failed)
                assertEquals(failure, 0, acknowledgements)
                assertFalse(failure, recorded)
            } finally {
                client.close()
            }
        }
    }

    @Test
    fun partialSaveRetrySkipsPersistedFilesAndAcknowledgesOnlyAfterAllSaved() = runTest {
        val savedFiles = mutableSetOf<String>()
        val written = mutableListOf<String>()
        val downloaded = mutableListOf<String>()
        var failSecond = true
        var acknowledgements = 0
        var recorded = false
        val client = client { request ->
            if (request.method == HttpMethod.Post) {
                acknowledgements++
                respond("", HttpStatusCode.NoContent)
            } else {
                downloaded += request.url.encodedPath.substringAfterLast('/')
                respond(encrypted())
            }
        }
        suspend fun save() =
            receiveAndSaveChild(
                client,
                "same-child",
                listOf(file("first", 3, blobId = "a"), file("second", 3, blobId = "b")),
                key,
                saveFile = { file, content ->
                    content {}
                    if (file.blobId == "b" && failSecond) error("Disk full")
                    else written += file.blobId
                },
                recordSaved = { recorded = true },
                alreadySaved = savedFiles.toSet(),
                recordFileSaved = { savedFiles += it },
            )
        try {
            try {
                save()
            } catch (_: IllegalStateException) {}
            assertEquals(setOf("a"), savedFiles)
            assertFalse(recorded)
            assertEquals(0, acknowledgements)
            failSecond = false
            save()
            assertEquals(listOf("a", "b"), written)
            assertEquals(listOf("a", "b", "b"), downloaded)
            assertTrue(recorded)
            assertEquals(1, acknowledgements)
        } finally {
            client.close()
        }
    }

    @Test
    fun failedAcknowledgementRetriesDurableSavedIdsWithoutDownloadingAgain() = runTest {
        var saved = row()
        var gets = 0
        var posts = 0
        val client = client { request ->
            if (request.method == HttpMethod.Post) {
                posts++
                respond(
                    "",
                    if (posts == 1) HttpStatusCode.ServiceUnavailable else HttpStatusCode.NoContent,
                )
            } else {
                gets++
                respond(encrypted())
            }
        }
        try {
            receiveAndSaveChild(
                client,
                "child",
                listOf(file("one", 3, blobId = "a")),
                key,
                saveFile = { _, content -> content {} },
                recordSaved = { child ->
                    saved =
                        mergeReceivedHistory(
                            saved,
                            ReceivedSnapshot(mapOf("child" to child)),
                            saved = true,
                        )
                },
            )
            assertEquals("complete", saved.status)
            assertEquals(setOf("child"), saved.savedTransferIds())
            retrySavedDownloadAcknowledgements(saved.copy(), client)
            assertEquals(1, gets)
            assertEquals(2, posts)
        } finally {
            client.close()
        }
    }

    @Test
    fun metadataAndUnsavedChildrenDoNotCreateAcknowledgements() = runTest {
        var requests = 0
        val client = client {
            requests++
            error("No request should be sent")
        }
        try {
            val observed =
                mergeReceivedHistory(row(), ReceivedSnapshot(mapOf("child" to ReceivedChild(1, 3))))
            retrySavedDownloadAcknowledgements(observed, client)
            assertEquals(0, requests)
        } finally {
            client.close()
        }
    }

    @Test
    fun acknowledgementTimeoutIsBestEffortButCallerCancellationPropagates() = runTest {
        val slow = client {
            delay(10_000)
            error("Should have timed out")
        }
        try {
            assertFalse(acknowledgeSavedDownload(slow, "child"))
        } finally {
            slow.close()
        }
        val cancelled = client { throw CancellationException("Cancelled by caller") }
        try {
            var propagated = false
            try {
                acknowledgeSavedDownload(cancelled, "child")
            } catch (_: CancellationException) {
                propagated = true
            }
            assertTrue(propagated)
        } finally {
            cancelled.close()
        }
    }
}
