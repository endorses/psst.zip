package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Test

class GuestReceivePipelineTest {
    private val key = ByteArray(32) { it.toByte() }
    private val nonce = ByteArray(12) { it.toByte() }
    private val plain = byteArrayOf(1, 2, 3)
    private val encryptionId = "1".repeat(32)

    private fun metadata(blobId: String) =
        FileMetadata(
            "same.txt",
            3,
            blobId = blobId,
            encoding = "chunked-v1",
            chunkSize = 4194304,
            encryptionId = encryptionId,
        )

    private val files = listOf(metadata("a"), metadata("b"))

    private fun saved(id: String) =
        SavedGuestFile(id, "content://downloads/$id", "same-$id.txt", 3, "text/plain", "digest")

    private fun row() = GuestDownload("identity", "https://example.com", "transfer", files = files)

    @Test
    fun operatorPauseKeepsSavedCheckpointAndExplicitResumeRequestsOnlyMissingFile() = runBlocking {
        val requests = mutableListOf<String>()
        var paused = true
        val encrypted = ChunkedFileCrypto.encrypt(key, encryptionId, 3, 0, plain)
        val client =
            ApiClient(
                ServerConfig("https://external.test"),
                HttpClient(
                    MockEngine { request ->
                        val id = request.url.encodedPath.substringAfterLast('/')
                        requests += id
                        if (id == "b" && paused)
                            respond(
                                """{"code":"public_transfers_paused"}""",
                                HttpStatusCode.ServiceUnavailable,
                            )
                        else respond(encrypted)
                    }
                ),
            )
        val checkpoints = mutableListOf<SavedGuestFile>()
        suspend fun receive() =
            receiveGuestFiles(
                client,
                "transfer",
                files,
                key,
                checkpoints.map { it.blobId }.toSet(),
                { _, _, _ -> },
                { _, _ -> },
                { file, content ->
                    content { actual -> assertArrayEquals(plain, actual) }
                    saved(file.blobId)
                },
                { checkpoints += it },
            )
        try {
            try {
                receive()
                fail("Pause was ignored")
            } catch (error: zip.psst.shared.api.PublicTransfersPausedException) {
                assertTrue(error.message!!.contains("Retry after"))
            }
            assertEquals(listOf("a"), checkpoints.map { it.blobId })
            assertEquals(listOf("a", "b"), requests)
            paused = false
            receive() // Explicit caller retry after the operator resumes service.
            assertEquals(listOf("a", "b"), checkpoints.map { it.blobId })
            assertEquals(listOf("a", "b", "b"), requests)
        } finally {
            client.close()
        }
    }

    @Test
    fun trafficBudgetKeepsSavedCheckpointAndExplicitResumeRequestsOnlyMissingFile() = runBlocking {
        val requests = mutableListOf<String>()
        var paused = true
        val encrypted = ChunkedFileCrypto.encrypt(key, encryptionId, 3, 0, plain)
        val client =
            ApiClient(
                ServerConfig("https://external.test"),
                HttpClient(
                    MockEngine { request ->
                        val id = request.url.encodedPath.substringAfterLast('/')
                        requests += id
                        if (id == "b" && paused)
                            respond(
                                """{"code":"traffic_budget_exhausted"}""",
                                HttpStatusCode.TooManyRequests,
                            )
                        else respond(encrypted)
                    }
                ),
            )
        val checkpoints = mutableListOf<SavedGuestFile>()
        suspend fun receive() =
            receiveGuestFiles(
                client,
                "transfer",
                files,
                key,
                checkpoints.map { it.blobId }.toSet(),
                { _, _, _ -> },
                { _, _ -> },
                { file, content ->
                    content { actual -> assertArrayEquals(plain, actual) }
                    saved(file.blobId)
                },
                { checkpoints += it },
            )
        try {
            try {
                receive()
                fail("Traffic budget was ignored")
            } catch (error: zip.psst.shared.api.TrafficBudgetExhaustedException) {
                assertTrue(error.message!!.contains("Retry after"))
            }
            assertEquals(listOf("a"), checkpoints.map { it.blobId })
            assertEquals(listOf("a", "b"), requests)
            paused = false
            receive() // Explicit caller retry after the next billing cycle or an operator budget
            // change.
            assertEquals(listOf("a", "b"), checkpoints.map { it.blobId })
            assertEquals(listOf("a", "b", "b"), requests)
        } finally {
            client.close()
        }
    }

    @Test
    fun actualResponseMustMatchManifestBeforePublicationWithoutAutomaticRetries() = runTest {
        val encrypted = ChunkedFileCrypto.encrypt(key, encryptionId, 3, 0, plain)
        for (body in listOf(encrypted.copyOf(encrypted.size - 1), encrypted + byteArrayOf(1))) {
            var requests = 0
            var published = 0
            val client =
                ApiClient(
                    ServerConfig("https://external.test"),
                    HttpClient(
                        MockEngine {
                            requests++
                            assertNull(it.headers[HttpHeaders.Authorization])
                            respond(body)
                        }
                    ),
                )
            try {
                try {
                    receiveGuestFiles(
                        client,
                        "transfer",
                        listOf(files[0]),
                        key,
                        emptySet(),
                        { _, _, _ -> },
                        { _, _ -> },
                        { file, content ->
                            content {}
                            published++
                            saved(file.blobId)
                        },
                        { fail("Invalid response must never checkpoint") },
                    )
                    fail("Expected exact-length rejection")
                } catch (_: IllegalArgumentException) {}
                assertEquals(0, published)
                assertEquals(1, requests)
            } finally {
                client.close()
            }
        }
    }

    @Test
    fun failedSecondFileKeepsFirstCheckpointAndRetrySkipsItsDownload() = runTest {
        val requests = mutableListOf<String>()
        val checkpoints = mutableListOf<SavedGuestFile>()
        var fail = true
        val client =
            ApiClient(
                ServerConfig("https://example.com"),
                HttpClient(
                    MockEngine { request ->
                        val blob = request.url.encodedPath.substringAfterLast('/')
                        requests += blob
                        if (blob == "b" && fail) throw IOException("interrupted")
                        respond(ChunkedFileCrypto.encrypt(key, encryptionId, 3, 0, plain))
                    }
                ),
            )
        try {
            try {
                receiveGuestFiles(
                    client,
                    "transfer",
                    files,
                    key,
                    emptySet(),
                    { _, _, _ -> },
                    { _, _ -> },
                    { f, content ->
                        content { bytes -> assertArrayEquals(plain, bytes) }
                        saved(f.blobId)
                    },
                    checkpoints::add,
                )
                fail("expected failure")
            } catch (_: IOException) {}
            assertEquals(listOf("a"), checkpoints.map { it.blobId })
            fail = false
            receiveGuestFiles(
                client,
                "transfer",
                files,
                key,
                checkpoints.map { it.blobId }.toSet(),
                { _, _, _ -> },
                { _, _ -> },
                { f, content ->
                    content {}
                    saved(f.blobId)
                },
                checkpoints::add,
            )
            assertEquals(listOf("a", "b", "b"), requests)
            assertEquals(listOf("a", "b"), checkpoints.map { it.blobId })
        } finally {
            client.close()
        }
    }

    @Test
    fun authenticationDeclaredLengthAndStorageFailuresNeverCheckpoint() = runTest {
        for (failure in listOf("authentication", "size", "storage", "cancel")) {
            var writes = 0
            var checkpoints = 0
            val encrypted = ChunkedFileCrypto.encrypt(key, encryptionId, 3, 0, plain)
            if (failure == "authentication")
                encrypted[encrypted.lastIndex] = (encrypted.last().toInt() xor 1).toByte()
            val client =
                ApiClient(
                    ServerConfig("https://example.com"),
                    HttpClient(MockEngine { respond(encrypted) }),
                )
            try {
                try {
                    receiveGuestFiles(
                        client,
                        "transfer",
                        listOf(files[0].copy(size = if (failure == "size") 4 else 3)),
                        key,
                        emptySet(),
                        { _, _, _ -> },
                        { _, _ -> },
                        { _, content ->
                            content {}
                            writes++
                            if (failure == "cancel") throw CancellationException()
                            throw IOException("storage unavailable")
                        },
                        { checkpoints++ },
                    )
                    fail("expected failure")
                } catch (_: Exception) {}
                assertEquals(0, checkpoints)
                assertEquals(if (failure == "storage" || failure == "cancel") 1 else 0, writes)
            } finally {
                client.close()
            }
        }
    }

    @Test
    fun publishedOutputAfterProcessDeathBecomesCheckpointWithoutDuplicateWrite() {
        val record = row().copy(saved = listOf(saved("a")), pending = saved("b"))
        var durable: GuestDownload? = null
        val recovered =
            reconcileGuestOutput(
                record,
                { true },
                { fail("published output must not be deleted") },
                { durable = it },
            )
        assertEquals(2, recovered.saved.size)
        assertTrue(recovered.complete)
        assertTrue(recovered.receiptPending)
        assertNull(recovered.pending)
        assertEquals(recovered, durable)
    }

    @Test
    fun partialOutputIsDeletedButSuccessfulFilesRemain() {
        val record = row().copy(saved = listOf(saved("a")), pending = saved("b"))
        val deleted = mutableListOf<String>()
        val recovered = reconcileGuestOutput(record, { false }, { deleted += it.blobId }, {})
        assertEquals(listOf("b"), deleted)
        assertEquals(listOf("a"), recovered.saved.map { it.blobId })
        assertFalse(recovered.complete)
        assertFalse(recovered.receiptPending)
    }

    @Test
    fun failedCheckpointPersistenceIsReportedAndOriginalJournalCanRecover() {
        val original = row().copy(pending = saved("a"))
        try {
            reconcileGuestOutput(original, { true }, {}, { throw IOException("disk full") })
            fail("expected failure")
        } catch (_: IOException) {}
        assertEquals("a", original.pending?.blobId)
        assertTrue(original.saved.isEmpty())
        val recovered = reconcileGuestOutput(original, { true }, {}, {})
        assertEquals(listOf("a"), recovered.saved.map { it.blobId })
    }

    @Test
    fun cleanupForCompletedOrExpiredUploadsNeverDeletesPublishedFiles() = runBlocking {
        for (status in listOf(200, 404, 410)) {
            var deletes = 0
            val client =
                ApiClient(
                    ServerConfig("https://example.com"),
                    HttpClient(
                        MockEngine { request ->
                            if (request.method == HttpMethod.Delete) deletes++
                            respond(
                                if (status == 200) """{"id":"transfer","status":"complete"}"""
                                else "gone",
                                HttpStatusCode.fromValue(status),
                                headersOf(HttpHeaders.ContentType, "application/json"),
                            )
                        }
                    ) {
                        expectSuccess = true
                        install(ContentNegotiation) { json() }
                    },
                )
            try {
                assertEquals(status == 200, cleanupGuestUpload(client, "transfer", "scoped-token"))
                assertEquals(0, deletes)
            } finally {
                client.close()
            }
        }
    }

    @Test
    fun cleanupRetainsUnavailableEntryAndDeletesPendingWithOnlyScopedToken() = runBlocking {
        var deletes = 0
        val client =
            ApiClient(
                ServerConfig("https://example.com"),
                HttpClient(
                    MockEngine { request ->
                        if (request.method == HttpMethod.Delete) {
                            deletes++
                            assertEquals(
                                "Bearer scoped-token",
                                request.headers[HttpHeaders.Authorization],
                            )
                            respond("", HttpStatusCode.NoContent)
                        } else
                            respond(
                                """{"id":"transfer","status":"pending"}""",
                                HttpStatusCode.OK,
                                headersOf(HttpHeaders.ContentType, "application/json"),
                            )
                    }
                ) {
                    expectSuccess = true
                    install(ContentNegotiation) { json() }
                },
            )
        try {
            cleanupGuestUpload(client, "transfer", "scoped-token")
            assertEquals(1, deletes)
        } finally {
            client.close()
        }
        val offline =
            ApiClient(
                ServerConfig("https://example.com"),
                HttpClient(MockEngine { throw IOException("offline") }),
            )
        try {
            try {
                cleanupGuestUpload(offline, "transfer", "scoped-token")
                fail("entry must stay queued")
            } catch (_: IOException) {}
        } finally {
            offline.close()
        }
    }

    @Test
    fun lostCompleteResponseRecoversSuccessfulUploadWithoutDeletingOrCompletingAgain() =
        runBlocking {
            var serverCompleted = false
            var completeRequests = 0
            var deletes = 0
            var journalCleared = false
            val client =
                ApiClient(
                    ServerConfig("https://example.com"),
                    HttpClient(
                        MockEngine { request ->
                            when {
                                request.url.encodedPath.endsWith("/complete") -> {
                                    completeRequests++
                                    serverCompleted = true
                                    throw IOException("Response lost after commit")
                                }
                                request.method == HttpMethod.Delete -> {
                                    deletes++
                                    respond("", HttpStatusCode.NoContent)
                                }
                                else ->
                                    respond(
                                        """{"id":"transfer","status":"${if (serverCompleted) "complete" else "pending"}"}""",
                                        HttpStatusCode.OK,
                                        headersOf(HttpHeaders.ContentType, "application/json"),
                                    )
                            }
                        }
                    ) {
                        expectSuccess = true
                        install(ContentNegotiation) { json() }
                    },
                )
            try {
                var knownCompleted = false
                try {
                    client.transfers.complete("transfer")
                    knownCompleted = true
                } catch (_: IOException) {}
                val result =
                    resolveGuestUpload(client, "transfer", "scoped-token", knownCompleted) {
                        journalCleared = true
                    }
                assertTrue(result.completed)
                assertTrue(result.journalCleared)
                assertTrue(journalCleared)
                assertEquals(1, completeRequests)
                assertEquals(0, deletes)
            } finally {
                client.close()
            }
        }

    @Test
    fun completedUploadStaysSuccessfulWhenLocalJournalDeletionFails() = runBlocking {
        var requests = 0
        val client =
            ApiClient(
                ServerConfig("https://example.com"),
                HttpClient(
                    MockEngine {
                        requests++
                        throw IOException("Server unavailable after completion")
                    }
                ),
            )
        try {
            val result =
                resolveGuestUpload(client, "transfer", "scoped-token", knownCompleted = true) {
                    throw IOException("Cannot delete cleanup journal")
                }
            assertTrue(result.completed)
            assertFalse(result.journalCleared)
            assertEquals(0, requests)
        } finally {
            client.close()
        }
    }

    @Test
    fun correctedKeyCanOnlyReplaceUnauthenticatedEmptyEntry() {
        val empty = row().copy(files = emptyList())
        assertTrue(canReplaceGuestKey(empty))
        assertFalse(canReplaceGuestKey(row()))
        assertFalse(canReplaceGuestKey(empty.copy(saved = listOf(saved("a")))))
        assertFalse(canReplaceGuestKey(empty.copy(pending = saved("a"))))
    }

    @Test
    fun interruptedAtomicRenameIsStillDiscoveredForRecovery() {
        assertEquals(
            listOf("a.json", "b.json"),
            guestAtomicNames(
                listOf("a.json.bak", "b.json", "b.json.bak", "c.json.new", "a.key"),
                "json",
            ),
        )
    }

    @Test
    fun collisionPublicationPreservesOriginalAndRetriesConcurrentWriter() {
        val directory = java.nio.file.Files.createTempDirectory("guest-publication-test").toFile()
        try {
            java.io.File(directory, "file.txt").writeText("existing")
            val temporary = java.io.File(directory, ".partial").apply { writeText("received") }
            var raced = false
            val output =
                publishGuestFile(temporary, directory, "file.txt") { candidate ->
                    if (!raced) {
                        candidate.writeText("concurrent")
                        raced = true
                    }
                }
            assertEquals("file (3).txt", output.name)
            assertEquals("received", output.readText())
            assertEquals("existing", java.io.File(directory, "file.txt").readText())
            assertEquals("concurrent", java.io.File(directory, "file (2).txt").readText())
            assertFalse(temporary.exists())
        } finally {
            directory.deleteRecursively()
        }
    }

    @Test
    fun guestIdentitySeparatesOriginsAndDecoderPreservesOlderRecords() {
        assertNotEquals(
            GuestDownloadStore.identity("http://one.example", "id"),
            GuestDownloadStore.identity("http://two.example", "id"),
        )
        val old =
            Json.decodeFromString<GuestDownload>(
                """{"identity":"old","origin":"http://one.example","transferId":"id","createdAt":1}"""
            )
        assertEquals(emptyList<SavedGuestFile>(), old.saved)
        assertFalse(old.complete)
        assertFalse(old.receiptPending)
    }
}
