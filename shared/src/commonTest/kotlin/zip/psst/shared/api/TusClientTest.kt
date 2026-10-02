package zip.psst.shared.api

import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.toByteArray
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlinx.coroutines.test.runTest

class TusClientTest {

    @Test
    fun createReturnsLocationHeader() = runTest {
        val engine = MockEngine { request ->
            assertEquals(HttpMethod.Post, request.method)
            assertEquals(TusClient.TUS_VERSION, request.headers["Tus-Resumable"])
            assertEquals("100", request.headers["Upload-Length"])

            respond(
                content = "",
                status = HttpStatusCode.Created,
                headers = headersOf(HttpHeaders.Location, "/uploads/abc123"),
            )
        }

        val client = HttpClient(engine)
        val tus = TusClient(client)
        val location = tus.create("https://example.com/uploads", 100L)
        assertEquals("https://example.com/uploads/abc123", location)
    }

    @Test
    fun createFailsWithoutLocationHeader() = runTest {
        val engine = MockEngine { respond(content = "", status = HttpStatusCode.Created) }

        val client = HttpClient(engine)
        val tus = TusClient(client)

        assertFailsWith<IllegalStateException> { tus.create("https://example.com/uploads", 100L) }
    }

    @Test
    fun getOffsetReturnsUploadOffset() = runTest {
        val engine = MockEngine { request ->
            assertEquals(HttpMethod.Head, request.method)

            respond(
                content = "",
                status = HttpStatusCode.OK,
                headers = headersOf("Upload-Offset", "512"),
            )
        }

        val client = HttpClient(engine)
        val tus = TusClient(client)
        val offset = tus.getOffset("https://example.com/uploads/abc123")
        assertEquals(512L, offset)
    }

    @Test
    fun uploadSendsChunksWithCorrectOffsets() = runTest {
        var patchCount = 0
        val engine = MockEngine { request ->
            when (request.method) {
                HttpMethod.Patch -> {
                    val offset = request.headers["Upload-Offset"]?.toLong() ?: 0
                    val bodyBytes = request.body.toByteArray()
                    val newOffset = offset + bodyBytes.size
                    patchCount++

                    respond(
                        content = "",
                        status = HttpStatusCode.NoContent,
                        headers = headersOf("Upload-Offset", newOffset.toString()),
                    )
                }
                else -> respond(content = "", status = HttpStatusCode.NotFound)
            }
        }

        val client = HttpClient(engine)
        val tus = TusClient(client)

        val data = ByteArray(250) { it.toByte() }
        tus.upload("https://example.com/uploads/abc", data, chunkSize = 100)

        // 250 bytes / 100 byte chunks = 3 chunks (100 + 100 + 50)
        assertEquals(3, patchCount)
    }

    @Test
    fun uploadReportsProgress() = runTest {
        val engine = MockEngine { request ->
            val offset = request.headers["Upload-Offset"]?.toLong() ?: 0
            val bodyBytes = request.body.toByteArray()
            val newOffset = offset + bodyBytes.size

            respond(
                content = "",
                status = HttpStatusCode.NoContent,
                headers = headersOf("Upload-Offset", newOffset.toString()),
            )
        }

        val client = HttpClient(engine)
        val tus = TusClient(client)

        val progressValues = mutableListOf<Long>()
        val data = ByteArray(200) { it.toByte() }
        tus.upload("https://example.com/uploads/abc", data, chunkSize = 100) { uploaded ->
            progressValues.add(uploaded)
        }

        assertEquals(listOf(100L, 200L), progressValues)
    }

    @Test
    fun createWithMetadataSendsHeader() = runTest {
        var metadataHeader: String? = null
        val engine = MockEngine { request ->
            metadataHeader = request.headers["Upload-Metadata"]
            respond(
                content = "",
                status = HttpStatusCode.Created,
                headers = headersOf(HttpHeaders.Location, "/uploads/xyz"),
            )
        }

        val client = HttpClient(engine)
        val tus = TusClient(client)
        tus.create("https://example.com/uploads", 100L, metadata = mapOf("filename" to "test.txt"))

        // Metadata header should be present and contain the key
        assertEquals(true, metadataHeader?.contains("filename"))
    }
}
