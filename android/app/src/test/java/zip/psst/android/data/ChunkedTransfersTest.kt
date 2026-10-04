package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.toByteArray
import io.ktor.http.*
import io.ktor.utils.io.ByteChannel
import io.ktor.utils.io.writeFully
import java.io.File
import java.security.MessageDigest
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

class ChunkedTransfersTest {
    private val key = ByteArray(32) { it.toByte() }
    private val id = "a".repeat(32)

    @Test
    fun downloadAbove100MiBStreamsFramesWithoutJoining() = runTest {
        val size = 101L * 1024 * 1024 + 7
        val expectedDigest = MessageDigest.getInstance("SHA-256")
        val receivedDigest = MessageDigest.getInstance("SHA-256")
        var maximumWrite = 0
        var count = 0L
        val body = ByteChannel()
        val producer = launch {
            for (index in 0 until ChunkedFileCrypto.chunkCount(size)) {
                val plain =
                    ByteArray(ChunkedFileCrypto.plaintextSize(size, index)) {
                        (index + it).toByte()
                    }
                expectedDigest.update(plain)
                body.writeFully(ChunkedFileCrypto.encrypt(key, id, size, index, plain))
            }
            body.close()
        }
        val client =
            ApiClient(
                ServerConfig("https://example.test"),
                HttpClient(MockEngine { respond(body) }),
            )
        try {
            val file =
                FileMetadata(
                    "large.bin",
                    size,
                    blobId = "blob",
                    encoding = "chunked-v1",
                    chunkSize = ChunkedFileCrypto.CHUNK_SIZE,
                    encryptionId = id,
                )
            val content =
                downloadedContent(client, "transfer", file, key) { bytes, total ->
                    assertEquals(ChunkedFileCrypto.wireSize(size), total)
                    assertTrue(bytes <= total!!)
                }
            content { chunk ->
                maximumWrite = maxOf(maximumWrite, chunk.size)
                receivedDigest.update(chunk)
                count += chunk.size
            }
            producer.join()
            assertEquals(size, count)
            assertEquals(ChunkedFileCrypto.CHUNK_SIZE, maximumWrite)
            assertArrayEquals(expectedDigest.digest(), receivedDigest.digest())
        } finally {
            client.close()
            producer.cancel()
        }
    }

    @Test
    fun uploadUsesBoundedPatchesAndExactWireLength() = runTest {
        val file = File.createTempFile("psst-test-upload-", ".bin")
        val expected = ByteArray(ChunkedFileCrypto.CHUNK_SIZE + 9) { it.toByte() }
        file.writeBytes(expected)
        val frames = mutableListOf<ByteArray>()
        var offset = 0L
        val client =
            ApiClient(
                ServerConfig("https://example.test"),
                HttpClient(
                    MockEngine { request ->
                        if (request.method == HttpMethod.Post) {
                            assertEquals(
                                ChunkedFileCrypto.wireSize(expected.size.toLong()).toString(),
                                request.headers["Upload-Length"],
                            )
                            respond(
                                "",
                                HttpStatusCode.Created,
                                headersOf("Location", "/api/v1/transfers/transfer/files/blob"),
                            )
                        } else {
                            assertEquals(offset.toString(), request.headers["Upload-Offset"])
                            val bytes = request.body.toByteArray()
                            assertTrue(
                                bytes.size <=
                                    ChunkedFileCrypto.CHUNK_SIZE + ChunkedFileCrypto.FRAME_OVERHEAD
                            )
                            frames += bytes
                            offset += bytes.size
                            respond(
                                "",
                                HttpStatusCode.NoContent,
                                headersOf("Upload-Offset", offset.toString()),
                            )
                        }
                    }
                ),
            )
        try {
            val metadata =
                uploadChunkedFile(
                    client,
                    "transfer",
                    file,
                    "large.bin",
                    "application/octet-stream",
                    key,
                )
            assertEquals("chunked-v1", metadata.encoding)
            assertEquals(2, frames.size)
            assertEquals(expected.size.toLong(), metadata.size)
            frames.forEachIndexed { index, frame ->
                val actual =
                    ChunkedFileCrypto.decrypt(
                        key,
                        metadata.encryptionId,
                        metadata.size,
                        index.toLong(),
                        frame,
                    )
                assertArrayEquals(
                    expected.copyOfRange(
                        index * ChunkedFileCrypto.CHUNK_SIZE,
                        minOf(expected.size, (index + 1) * ChunkedFileCrypto.CHUNK_SIZE),
                    ),
                    actual,
                )
            }
        } finally {
            client.close()
            file.delete()
        }
    }
}
