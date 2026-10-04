package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.toByteArray
import io.ktor.http.*
import kotlin.test.*
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.*

class PrivateInboxApiTest {
    @Test
    fun hostileMetadataIsBoundedBeforeManifestOrFileRequests() = transportTest {
        for (status in listOf(HttpStatusCode.OK, HttpStatusCode.BadRequest)) {
            var requests = 0
            val http =
                HttpClient(
                    MockEngine {
                        requests++
                        respond(" ".repeat(128 * 1024 + 1), status)
                    }
                ) {
                    applyClientPolicy()
                }
            val client = ApiClient(ServerConfig("https://hostile.test"), http)
            try {
                assertFails { client.transfers.get(child) }
                assertEquals(1, requests)
            } finally {
                client.close()
            }
        }
    }

    private fun transportTest(block: suspend () -> Unit) = runTest {
        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Default) { block() }
    }

    private val slot = "11111111-1111-1111-1111-111111111111"
    private val child = "22222222-2222-2222-2222-222222222222"
    private val publicKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

    @Test
    fun ownerReadsAreAuthenticatedAndPublicAvailabilityContainsNoCredentials() = transportTest {
        val calls = mutableListOf<String>()
        val http =
            HttpClient(
                MockEngine { request ->
                    val path = request.url.encodedPath
                    calls += path
                    assertEquals("owner.test", request.url.host)
                    assertEquals("", request.url.fragment)
                    assertNull(request.headers[HttpHeaders.Cookie])
                    if (path.endsWith("availability"))
                        assertNull(request.headers[HttpHeaders.Authorization])
                    else
                        assertEquals(
                            "Bearer owner-token",
                            request.headers[HttpHeaders.Authorization],
                        )
                    when {
                        path.endsWith("availability") ->
                            respond(
                                """{"id":"$slot","available":true,"receive_protocol":2,"recipient_public_key":"$publicKey","remaining_transfers":3,"remaining_bytes":1000}""",
                                headers = headersOf(HttpHeaders.ContentType, "application/json"),
                            )
                        path.endsWith("/slots/$slot") ->
                            respond(
                                """{"id":"$slot","receive_protocol":2}""",
                                headers = headersOf(HttpHeaders.ContentType, "application/json"),
                            )
                        path.endsWith("/transfers/$child") ->
                            respond(
                                """{"id":"$child","status":"complete"}""",
                                headers = headersOf(HttpHeaders.ContentType, "application/json"),
                            )
                        path.endsWith("downloaded") -> respond("", HttpStatusCode.NoContent)
                        else -> respond(ByteArray(28))
                    }
                }
            ) {
                applyClientPolicy()
            }
        val client = ApiClient(ServerConfig("https://owner.test"), http, "owner-token")
        try {
            client.slots.get(slot)
            client.slots.availability(slot)
            client.transfers.get(child)
            client.transfers.downloadManifest(child)
            client.transfers.downloadFile(child, slot)
            client.transfers.acknowledgeDownload(child)
            assertEquals(6, calls.size)
        } finally {
            client.close()
        }
    }

    @Test
    fun creationSerializesVersionPublicKeyAndOptionalLimits() = transportTest {
        val http =
            HttpClient(
                MockEngine { request ->
                    assertEquals("Bearer owner-token", request.headers[HttpHeaders.Authorization])
                    val body =
                        Json.parseToJsonElement(request.body.toByteArray().decodeToString())
                            .jsonObject
                    if (request.url.encodedPath.endsWith("slots")) {
                        assertEquals(2, body.getValue("receive_protocol").jsonPrimitive.int)
                        assertEquals(
                            publicKey,
                            body.getValue("recipient_public_key").jsonPrimitive.content,
                        )
                        assertEquals(3, body.getValue("max_files").jsonPrimitive.int)
                    } else assertEquals(2, body.getValue("max_downloads").jsonPrimitive.int)
                    respond(
                        """{"id":"$slot"}""",
                        HttpStatusCode.Created,
                        headersOf(HttpHeaders.ContentType, "application/json"),
                    )
                }
            ) {
                applyClientPolicy()
            }
        val client = ApiClient(ServerConfig("https://owner.test"), http, "owner-token")
        try {
            client.slots.create(publicKey, 3)
            client.transfers.create(2)
            assertFailsWith<IllegalArgumentException> { client.slots.create() }
            assertFailsWith<IllegalArgumentException> { client.transfers.create(-1) }
        } finally {
            client.close()
        }
    }

    @Test
    fun invitedUploaderRecoveryUsesOnlyScopedStatusProbe() = transportTest {
        var calls = 0
        val http =
            HttpClient(
                MockEngine { request ->
                    calls++
                    assertEquals("/api/v1/transfers/$child/upload-status", request.url.encodedPath)
                    assertEquals(
                        "Bearer child-only-token",
                        request.headers[HttpHeaders.Authorization],
                    )
                    respond(
                        """{"id":"$child","status":"complete"}""",
                        headers = headersOf(HttpHeaders.ContentType, "application/json"),
                    )
                }
            ) {
                applyClientPolicy()
            }
        val client = ApiClient(ServerConfig("https://owner.test"), http, "child-only-token")
        try {
            assertEquals(child, client.transfers.uploadStatus(child).id)
            assertEquals(1, calls)
        } finally {
            client.close()
        }
    }
}
