package zip.psst.shared.api

import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.*
import kotlin.test.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext

class IncidentControlTest {
    private val id = "11111111-1111-4111-8111-111111111111"

    private fun transportTest(block: suspend () -> Unit) = runTest {
        withContext(Dispatchers.Default) { block() }
    }

    @Test
    fun pausedResponsesAcrossCreateUploadAndDownloadsAreSafeAndNeverAutomaticallyRetried() =
        transportTest {
            val operations: List<suspend (ApiClient) -> Unit> =
                listOf(
                    { it.transfers.create() },
                    { it.slots.create("A".repeat(43), 0) },
                    { it.slots.createTransfer(id) },
                    { it.transfers.uploadManifest(id, ByteArray(116)) },
                    { it.transfers.complete(id) },
                    { it.createFileUpload(id, 63) },
                    {
                        it.tus.uploadChunk(
                            "https://server.test/api/v1/transfers/$id/files/$id",
                            ByteArray(63),
                            0,
                        )
                    },
                    { it.transfers.downloadManifest(id) },
                    {
                        it.transfers.downloadFileChunks(
                            id,
                            id,
                            63,
                            ChunkedFileCrypto.CHUNK_SIZE + ChunkedFileCrypto.FRAME_OVERHEAD,
                        ) {
                            fail("Paused body was treated as file data")
                        }
                    },
                )
            for (operation in operations) {
                var requests = 0
                val http =
                    HttpClient(
                        MockEngine {
                            requests++
                            respond(
                                """{"code":"public_transfers_paused","error":"SECRET_UNTRUSTED_ERROR"}""",
                                HttpStatusCode.ServiceUnavailable,
                            )
                        }
                    ) {
                        applyClientPolicy()
                    }
                val client = ApiClient(ServerConfig("https://server.test"), http, "session-token")
                try {
                    val error =
                        assertFailsWith<PublicTransfersPausedException> { operation(client) }
                    assertTrue(error.message!!.contains("Retry after"))
                    assertFalse(error.message!!.contains("SECRET"))
                    assertFalse((error as Exception) is AuthenticationRequiredException)
                    assertEquals(1, requests)
                } finally {
                    client.close()
                }
            }
        }

    @Test
    fun revokedResourcesRemainDistinctFromRevokedAccountSessionsAndHeadHeaderWorks() =
        transportTest {
            val http =
                HttpClient(
                    MockEngine { request ->
                        when {
                            request.method == HttpMethod.Head ->
                                respond(
                                    "",
                                    HttpStatusCode.ServiceUnavailable,
                                    headersOf("X-Psst-Error-Code", "public_transfers_paused"),
                                )
                            request.url.encodedPath.endsWith("/slots/$id") ->
                                respond("", HttpStatusCode.Unauthorized)
                            else ->
                                respond(
                                    """{"code":"resource_revoked","error":"untrusted"}""",
                                    HttpStatusCode.Gone,
                                )
                        }
                    }
                ) {
                    applyClientPolicy()
                }
            val client = ApiClient(ServerConfig("https://server.test"), http, "token")
            try {
                assertFailsWith<ResourceRevokedException> { client.transfers.get(id) }
                assertFailsWith<AuthenticationRequiredException> { client.slots.get(id) }
                assertFailsWith<PublicTransfersPausedException> {
                    client.tus.getOffset("https://server.test/api/v1/transfers/$id/files/$id")
                }
            } finally {
                client.close()
            }
        }

    @Test
    fun oversizedOrMismatchedErrorBodiesCannotInventOperatorActions() = transportTest {
        for ((status, body) in
            listOf(
                HttpStatusCode.ServiceUnavailable to
                    (" ".repeat(4097) + """{"code":"public_transfers_paused"}"""),
                HttpStatusCode.Gone to """{"code":"public_transfers_paused"}""",
            )) {
            val http = HttpClient(MockEngine { respond(body, status) }) { applyClientPolicy() }
            val client = ApiClient(ServerConfig("https://server.test"), http)
            try {
                val error = assertFails { client.transfers.get(id) }
                assertFalse(error is TransferPolicyException)
            } finally {
                client.close()
            }
        }
    }
}
