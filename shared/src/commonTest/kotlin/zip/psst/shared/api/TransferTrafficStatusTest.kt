package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.*
import kotlin.test.*
import kotlinx.coroutines.test.runTest

class TransferTrafficStatusTest {
    @Test
    fun probesStayOnConfiguredOriginAndUseOnlyTheirClientsAuthorization() = runTest {
        val id = "11111111-1111-4111-8111-111111111111"
        for (token in listOf(null, "owner-or-upload-capability")) {
            val requests = mutableListOf<String>()
            val http =
                HttpClient(
                    MockEngine { request ->
                        assertEquals("external.test", request.url.host)
                        assertEquals(
                            if (request.url.encodedPath.contains("/slots/")) "download"
                            else "upload",
                            request.url.parameters["direction"],
                        )
                        assertEquals(
                            token?.let { "Bearer $it" },
                            request.headers[HttpHeaders.Authorization],
                        )
                        requests += request.url.encodedPath
                        respond("""{"state":"exhausted","retry_at":"2026-11-01T00:00:00Z"}""")
                    }
                ) {
                    applyClientPolicy()
                }
            val api = ApiClient(ServerConfig("https://external.test"), http, token)
            try {
                assertTrue(
                    api.transfers.trafficStatus(id, "upload").policyException()
                        is TrafficBudgetExhaustedException
                )
                assertTrue(
                    api.slots.trafficStatus(id, "download").policyException()
                        is TrafficBudgetExhaustedException
                )
                assertEquals(
                    listOf(
                        "/api/v1/transfers/$id/traffic-status",
                        "/api/v1/slots/$id/traffic-status",
                    ),
                    requests,
                )
            } finally {
                api.close()
            }
        }
    }

    @Test
    fun classifierExcludesLocalValidationAndCancellation() {
        assertTrue(TrafficFailureClassifier.shouldProbe(TransferDownloadInterruptedException()))
        assertFalse(
            TrafficFailureClassifier.shouldProbe(IllegalArgumentException("decrypt failed"))
        )
        assertFalse(
            TrafficFailureClassifier.shouldProbe(kotlinx.coroutines.CancellationException())
        )
        assertFalse(TrafficFailureClassifier.shouldProbe(PublicTransfersPausedException()))
    }

    @Test
    fun unknownStatusDoesNotInventRevocation() {
        assertFailsWith<IllegalArgumentException> {
            TransferTrafficStatus("suspended").policyException()
        }
        assertNull(TransferTrafficStatus("ready").policyException())
        assertTrue(TransferTrafficStatus("revoked").policyException() is ResourceRevokedException)
    }
}
