package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.TransferStatus
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.toByteArray
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.*
import io.ktor.serialization.kotlinx.json.json
import kotlin.test.*
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.*

class LinkTitleApiTest {
    private fun transportTest(block: suspend () -> Unit) = runTest {
        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Default) { block() }
    }

    private val id = "00000000-0000-0000-0000-000000000001"

    @Test
    fun creationAndRenameSendExplicitSharedTitleAndOwnerToken() = transportTest {
        val calls = mutableListOf<String>()
        val engine = MockEngine { request ->
            assertEquals("Bearer owner-token", request.headers[HttpHeaders.Authorization])
            val body =
                Json.parseToJsonElement(request.body.toByteArray().decodeToString()).jsonObject
            calls += request.url.encodedPath
            if (request.method == HttpMethod.Patch) {
                assertEquals(JsonNull, body["title"])
                respond(
                    """{"title":null}""",
                    headers = headersOf(HttpHeaders.ContentType, "application/json"),
                )
            } else {
                assertEquals("Shared title", body["title"]?.jsonPrimitive?.content)
                respond(
                    """{"id":"$id","title":"Shared title"}""",
                    HttpStatusCode.Created,
                    headersOf(HttpHeaders.ContentType, "application/json"),
                )
            }
        }
        val client = HttpClient(engine) { install(ContentNegotiation) { json() } }
        try {
            val config = ServerConfig("https://example.com")
            val transfer = TransferApi(client, config, "owner-token")
            assertEquals("Shared title", transfer.create(0, " Shared title ").title)
            assertNull(transfer.renameTitle(id, " ").title)
            val slot = SlotApi(client, config, "owner-token")
            assertEquals(
                "Shared title",
                slot.create("CQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 0, "Shared title").title,
            )
            assertNull(slot.renameTitle(id, null).title)
            assertEquals(4, calls.size)
        } finally {
            client.close()
        }
    }

    @Test
    fun historyFilterIsSentToUnderlyingLoader() = transportTest {
        val engine = MockEngine { request ->
            assertEquals("slot", request.url.parameters["kind"])
            assertEquals("2", request.url.parameters["limit"])
            respond(
                """{"paginated":true,"next_cursor":null,"transfers":[],"slots":[]}""",
                headers = headersOf(HttpHeaders.ContentType, "application/json"),
            )
        }
        val client = HttpClient(engine) { install(ContentNegotiation) { json() } }
        try {
            assertTrue(
                AuthApi(client, ServerConfig("https://example.com"), "owner")
                    .resourcesPage(null, 2, "slot")
                    .slots
                    .isEmpty()
            )
        } finally {
            client.close()
        }
    }

    @Test
    fun exhaustedMetadataRemainsSeparateFromReceipt() = transportTest {
        val engine = MockEngine { request ->
            if (request.method == HttpMethod.Post) respond("", HttpStatusCode.NoContent)
            else
                respond(
                    """{"id":"$id","status":"exhausted","title":"Expired allowance","inactive_reason":"download_limit","downloaded_at":null}""",
                    headers = headersOf(HttpHeaders.ContentType, "application/json"),
                )
        }
        val client =
            HttpClient(engine) {
                install(ContentNegotiation) { json(Json { ignoreUnknownKeys = true }) }
            }
        try {
            val api = TransferApi(client, ServerConfig("https://example.com"))
            val transfer = api.get(id)
            assertEquals(TransferStatus.EXHAUSTED, transfer.status)
            assertEquals("download_limit", transfer.inactiveReason)
            assertNull(transfer.downloadedAt)
            api.acknowledgeDownload(id)
        } finally {
            client.close()
        }
    }
}
