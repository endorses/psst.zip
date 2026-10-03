package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.UrlHelper
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.toByteArray
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext

class GuestApiTest {
    private fun transportTest(block: suspend () -> Unit) = runTest {
        withContext(Dispatchers.Default) { block() }
    }

    private val id = "01234567-89ab-cdef-0123-456789abcdef"
    private val blob = "11234567-89ab-cdef-0123-456789abcdef"
    private val capability = "c".repeat(43)
    private val origin = "http://192.168.178.29:8080"
    private val jsonHeaders =
        headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())

    @Test
    fun guestMetadataManifestBlobAndReceiptUseOnlyParsedOriginWithoutSecrets() = transportTest {
        val key = ByteArray(32) { it.toByte() }
        val parsed = UrlHelper.parse(UrlHelper.buildDownloadUrl(origin, id, key))!!
        val paths = mutableListOf<String>()
        val http =
            HttpClient(
                MockEngine { request ->
                    assertEquals("192.168.178.29", request.url.host)
                    assertEquals(8080, request.url.port)
                    assertEquals("", request.url.fragment)
                    assertNull(request.headers[HttpHeaders.Authorization])
                    assertNull(request.headers[HttpHeaders.Cookie])
                    paths += request.url.encodedPath
                    when {
                        request.url.encodedPath.endsWith("downloaded") -> {
                            assertEquals(0, request.body.toByteArray().size)
                            respond("", HttpStatusCode.NoContent)
                        }
                        request.url.encodedPath.endsWith(id) ->
                            respond("""{"id":"$id","status":"complete"}""", headers = jsonHeaders)
                        else -> respond(ByteArray(28))
                    }
                }
            ) {
                applyClientPolicy()
            }
        val api = ApiClient(ServerConfig(parsed.origin), http)
        try {
            api.transfers.get(parsed.id)
            api.transfers.downloadManifest(parsed.id)
            api.transfers.downloadFile(parsed.id, blob)
            api.transfers.acknowledgeDownload(parsed.id)
            assertEquals(4, paths.size)
            assertTrue(paths.all { it.startsWith("/api/v1/transfers/$id") })
        } finally {
            api.close()
        }
    }

    @Test
    fun redirectsAreRejectedBeforeAnotherOriginIsRequested() = transportTest {
        for (suffix in listOf("", "/manifest", "/files/$blob", "/downloaded")) {
            var calls = 0
            val http =
                HttpClient(
                    MockEngine { request ->
                        calls++
                        assertEquals("example.com", request.url.host)
                        respond(
                            "",
                            HttpStatusCode.Found,
                            headersOf(HttpHeaders.Location, "http://evil.example/target"),
                        )
                    }
                ) {
                    applyClientPolicy()
                }
            val api = ApiClient(ServerConfig("https://example.com"), http)
            try {
                assertFailsWith<Exception> {
                    when (suffix) {
                        "" -> api.transfers.get(id)
                        "/manifest" -> api.transfers.downloadManifest(id)
                        "/downloaded" -> api.transfers.acknowledgeDownload(id)
                        else -> api.transfers.downloadFile(id, blob)
                    }
                }
                assertEquals(1, calls)
            } finally {
                api.close()
            }
        }
    }

    @Test
    fun slotCreationIsPublicButWritesUseOnlyChildCapability() = transportTest {
        var publicCalls = 0
        val http =
            HttpClient(
                MockEngine { request ->
                    when {
                        request.url.encodedPath == "/api/v1/slots/$id/transfers" -> {
                            publicCalls++
                            assertNull(request.headers[HttpHeaders.Authorization])
                            respond(
                                """{"id":"$blob","delete_token":"$capability"}""",
                                HttpStatusCode.Created,
                                jsonHeaders,
                            )
                        }
                        else -> {
                            assertEquals(
                                "Bearer $capability",
                                request.headers[HttpHeaders.Authorization],
                            )
                            assertFalse(
                                request.headers[HttpHeaders.Authorization]!!.contains(
                                    "account-token"
                                )
                            )
                            if (request.url.encodedPath.endsWith("files"))
                                respond(
                                    "",
                                    HttpStatusCode.Created,
                                    headersOf(
                                        HttpHeaders.Location,
                                        "$origin/api/v1/transfers/$blob/files/$id",
                                    ),
                                )
                            else respond("", HttpStatusCode.NoContent)
                        }
                    }
                }
            ) {
                applyClientPolicy()
            }
        try {
            val created = SlotApi(http, ServerConfig(origin), "account-token").createTransfer(id)
            assertEquals(capability, created.deleteToken)
            assertEquals(1, publicCalls)
            val api = ApiClient(ServerConfig(origin), http, created.deleteToken)
            api.transfers.uploadManifest(created.id, byteArrayOf(1))
            api.transfers.complete(created.id)
            api.transfers.delete(created.id, created.deleteToken)
            api.tus.create("$origin/api/v1/transfers/$blob/files", 2)
        } finally {
            http.close()
        }
    }

    @Test
    fun childUploadRejectsOffOriginTusLocationBeforeSendingCapabilityThere() = transportTest {
        var calls = 0
        val http =
            HttpClient(
                MockEngine {
                    calls++
                    respond(
                        "",
                        HttpStatusCode.Created,
                        headersOf(HttpHeaders.Location, "https://evil.example/resource"),
                    )
                }
            ) {
                applyClientPolicy()
            }
        try {
            assertFailsWith<IllegalArgumentException> {
                TusClient(http, origin, capability).create("$origin/api/v1/transfers/$id/files", 1)
            }
            assertEquals(1, calls)
        } finally {
            http.close()
        }
    }

    @Test
    fun unexpectedMetadataIdentityCannotRedirectTheCoordinator() = transportTest {
        val http =
            HttpClient(
                MockEngine {
                    respond("""{"id":"$blob","status":"complete"}""", headers = jsonHeaders)
                }
            ) {
                applyClientPolicy()
            }
        try {
            assertFailsWith<IllegalArgumentException> {
                TransferApi(http, ServerConfig(origin)).get(id)
            }
        } finally {
            http.close()
        }
    }

    @Test
    fun excessiveOrTruncatedDeclaredLengthsFailBeforePublishingBytes() = transportTest {
        for (length in listOf("-1", "26214429", "50")) {
            val http =
                HttpClient(
                    MockEngine {
                        respond(
                            ByteArray(28),
                            headers = headersOf(HttpHeaders.ContentLength, length),
                        )
                    }
                ) {
                    applyClientPolicy()
                }
            try {
                assertFailsWith<IllegalArgumentException> {
                    TransferApi(http, ServerConfig(origin)).downloadFile(id, blob)
                }
            } finally {
                http.close()
            }
        }
    }

    @Test
    fun unknownContentLengthReportsBytesWithoutInventingATotal() = transportTest {
        val http = HttpClient(MockEngine { respond(ByteArray(50)) }) { applyClientPolicy() }
        val updates = mutableListOf<Long>()
        try {
            TransferApi(http, ServerConfig(origin)).downloadFileWithProgress(id, blob) {
                count,
                total ->
                assertNull(total)
                updates += count
            }
            assertEquals(listOf(0L, 50L), updates)
        } finally {
            http.close()
        }
    }

    @Test
    fun progressIsActualMonotonicBoundedAndContainsFinalBytes() = transportTest {
        val bytes = ByteArray(150_000) { (it % 251).toByte() }
        val updates = mutableListOf<Long>()
        val http =
            HttpClient(
                MockEngine {
                    respond(
                        bytes,
                        headers = headersOf(HttpHeaders.ContentLength, bytes.size.toString()),
                    )
                }
            ) {
                applyClientPolicy()
            }
        try {
            val result =
                TransferApi(http, ServerConfig(origin)).downloadFileWithProgress(id, blob) {
                    count,
                    total ->
                    assertEquals(bytes.size.toLong(), total)
                    updates += count
                }
            assertTrue(bytes.contentEquals(result))
            assertEquals(0L, updates.first())
            assertEquals(bytes.size.toLong(), updates.last())
            assertTrue(updates.zipWithNext().all { (a, b) -> a < b })
            assertTrue(updates.size <= 4)
        } finally {
            http.close()
        }
    }
}
