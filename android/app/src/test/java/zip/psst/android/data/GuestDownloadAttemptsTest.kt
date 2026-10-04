package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test

class GuestDownloadAttemptsTest {
    private val blob = "11111111-1111-4111-8111-111111111111"
    private val row =
        GuestDownload(
            "local",
            "https://external.test",
            "22222222-2222-4222-8222-222222222222",
            files =
                listOf(
                    FileMetadata(
                        "saved.txt",
                        3,
                        blobId = blob,
                        encoding = "chunked-v1",
                        chunkSize = 4194304,
                        encryptionId = "1".repeat(32),
                    )
                ),
        )

    @Test
    fun metadataRefreshNeverConsumesAnotherDownloadAndFailedRefreshClearsStaleAllowance() =
        runBlocking {
            var requests = 0
            var status = HttpStatusCode.OK
            val factory: (String) -> ApiClient = { origin ->
                ApiClient(
                    ServerConfig(origin),
                    HttpClient(
                        MockEngine {
                            requests++
                            assertEquals("/api/v1/transfers/${row.transferId}", it.url.encodedPath)
                            assertNull(it.headers[HttpHeaders.Authorization])
                            respond(
                                """{"id":"${row.transferId}","status":"complete","file_count":1,"total_size":63,"max_downloads":2,"files":[{"id":"$blob","size":63,"download_count":2,"remaining_downloads":0}]}""",
                                status,
                            )
                        }
                    ),
                )
            }
            assertEquals(mapOf(blob to 0L), refreshGuestDownloadAttempts(row, factory))
            status = HttpStatusCode.Gone
            assertEquals(mapOf(blob to null), refreshGuestDownloadAttempts(row, factory))
            assertEquals(2, requests)
            assertEquals("saved.txt", row.files.single().name)
        }
}
