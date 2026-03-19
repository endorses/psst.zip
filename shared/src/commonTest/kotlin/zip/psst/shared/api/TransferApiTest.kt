package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.TransferStatus
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals

class TransferApiTest {
    private val config = ServerConfig(baseUrl = "https://example.com")

    private fun createMockClient(handler: MockEngine.() -> Unit = {}): Pair<HttpClient, MockEngine> {
        val engine = MockEngine { request ->
            when {
                request.url.encodedPath == "/api/v1/transfers" &&
                    request.method == HttpMethod.Post -> {
                    respond(
                        content = """{"id":"test-xfer","file_count":0,"total_size":0,"status":"pending"}""",
                        status = HttpStatusCode.Created,
                        headers = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString()),
                    )
                }
                request.url.encodedPath == "/api/v1/transfers/test-xfer" &&
                    request.method == HttpMethod.Get -> {
                    respond(
                        content = """{"id":"test-xfer","file_count":2,"total_size":1024,"status":"complete"}""",
                        status = HttpStatusCode.OK,
                        headers = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString()),
                    )
                }
                request.url.encodedPath == "/api/v1/transfers/test-xfer/manifest" &&
                    request.method == HttpMethod.Get -> {
                    respond(
                        content = ByteArray(32) { it.toByte() }.decodeToString(),
                        status = HttpStatusCode.OK,
                        headers = headersOf(
                            HttpHeaders.ContentType,
                            ContentType.Application.OctetStream.toString(),
                        ),
                    )
                }
                request.url.encodedPath == "/api/v1/transfers/test-xfer/complete" &&
                    request.method == HttpMethod.Post -> {
                    respond(
                        content = "",
                        status = HttpStatusCode.NoContent,
                    )
                }
                else -> {
                    respond(
                        content = "Not found",
                        status = HttpStatusCode.NotFound,
                    )
                }
            }
        }

        val client = HttpClient(engine) {
            install(ContentNegotiation) {
                json(
                    Json {
                        ignoreUnknownKeys = true
                        isLenient = true
                        encodeDefaults = true
                    },
                )
            }
        }

        return Pair(client, engine)
    }

    @Test
    fun createTransfer() = runTest {
        val (client, _) = createMockClient()
        val api = TransferApi(client, config)

        val transfer = api.create()
        assertEquals("test-xfer", transfer.id)
        assertEquals(TransferStatus.PENDING, transfer.status)
    }

    @Test
    fun getTransfer() = runTest {
        val (client, _) = createMockClient()
        val api = TransferApi(client, config)

        val transfer = api.get("test-xfer")
        assertEquals("test-xfer", transfer.id)
        assertEquals(2, transfer.fileCount)
        assertEquals(1024L, transfer.totalSize)
        assertEquals(TransferStatus.COMPLETE, transfer.status)
    }

    @Test
    fun completeTransfer() = runTest {
        val (client, _) = createMockClient()
        val api = TransferApi(client, config)

        // Should not throw
        api.complete("test-xfer")
    }
}
