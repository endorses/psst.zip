package zip.psst.shared.api

import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext

class ChunkedTransferApiTest {
    private fun testTransport(block: suspend () -> Unit) = runTest {
        withContext(Dispatchers.Default) { block() }
    }

    @Test
    fun configReadsTheTargetOriginWithoutAccountSecrets() = testTransport {
        val http =
            HttpClient(
                MockEngine { request ->
                    assertEquals("other.example", request.url.host)
                    assertEquals("/api/v1/config", request.url.encodedPath)
                    assertNull(request.headers[HttpHeaders.Authorization])
                    respond(
                        """{"max_file_size":536870912,"max_file_size_ceiling":5368709120}""",
                        headers = headersOf(HttpHeaders.ContentType, "application/json"),
                    )
                }
            ) {
                applyClientPolicy()
            }
        val api = ApiClient(ServerConfig("https://other.example"), http)
        try {
            assertEquals(536870912L, api.limits.get().maxFileSize)
        } finally {
            api.close()
        }
    }

    @Test
    fun exactFramesAreDeliveredSeriallyAndTrailingOrShortDataFails() = testTransport {
        val size = ChunkedFileCrypto.CHUNK_SIZE + ChunkedFileCrypto.FRAME_OVERHEAD
        for (delta in listOf(-1, 0, 1)) {
            val http =
                HttpClient(MockEngine { respond(ByteArray(size + 60 + delta)) }) {
                    applyClientPolicy()
                }
            val api = ApiClient(ServerConfig("https://example.com"), http)
            val lengths = mutableListOf<Int>()
            suspend fun download() {
                api.transfers.downloadFileChunks("id", "file", size + 60L, size) {
                    lengths += it.size
                    true
                }
            }
            try {
                if (delta == 0) {
                    download()
                    assertEquals(listOf(size, 60), lengths)
                } else assertFailsWith<IllegalArgumentException> { download() }
            } finally {
                api.close()
            }
        }
    }

    @Test
    fun refusingAFrameStopsBeforeTheNextCallback() = testTransport {
        val size = ChunkedFileCrypto.CHUNK_SIZE + ChunkedFileCrypto.FRAME_OVERHEAD
        val http = HttpClient(MockEngine { respond(ByteArray(size + 60)) }) { applyClientPolicy() }
        val api = ApiClient(ServerConfig("https://example.com"), http)
        var callbacks = 0
        try {
            assertFailsWith<IllegalArgumentException> {
                api.transfers.downloadFileChunks("id", "file", size + 60L, size) {
                    callbacks++
                    false
                }
            }
            assertEquals(1, callbacks)
        } finally {
            api.close()
        }
    }

    @Test
    fun redirectsNeverDeliverAFrame() = testTransport {
        val http =
            HttpClient(
                MockEngine {
                    respond(
                        "",
                        HttpStatusCode.Found,
                        headersOf(HttpHeaders.Location, "https://other.example/file"),
                    )
                }
            ) {
                applyClientPolicy()
            }
        val api = ApiClient(ServerConfig("https://example.com"), http)
        try {
            assertFailsWith<IllegalArgumentException> {
                api.transfers.downloadFileChunks(
                    "id",
                    "file",
                    60,
                    ChunkedFileCrypto.CHUNK_SIZE + 60,
                ) {
                    error("must not deliver")
                }
            }
        } finally {
            api.close()
        }
    }
}
