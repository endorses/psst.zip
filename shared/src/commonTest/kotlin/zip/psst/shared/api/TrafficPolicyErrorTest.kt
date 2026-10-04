package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.*
import kotlin.test.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext

class TrafficPolicyErrorTest {
    private fun transportTest(block: suspend () -> Unit) = runTest {
        withContext(Dispatchers.Default) { block() }
    }

    @Test
    fun policyChangeStopsWithoutRetryingOrExpiringLogin() = transportTest {
        var requests = 0
        val http =
            HttpClient(
                MockEngine {
                    requests++
                    respond(
                        "",
                        HttpStatusCode.Conflict,
                        headersOf("X-Psst-Error-Code", "traffic_policy_changed"),
                    )
                }
            ) {
                applyClientPolicy()
            }
        val api = ApiClient(ServerConfig("https://server.test"), http, "session")
        try {
            val error =
                assertFailsWith<TrafficPolicyChangedException> {
                    api.tus.uploadChunk(
                        "https://server.test/api/v1/transfers/id/files/file",
                        ByteArray(63),
                        0,
                    )
                }
            assertTrue(error.message!!.contains("Retry manually"))
            assertEquals(1, requests)
        } finally {
            api.close()
        }
    }

    @Test
    fun budgetResponsesExposeOnlyValidatedCycleTimesAndNeverRetry() = transportTest {
        for (header in listOf(false, true)) {
            var requests = 0
            val http =
                HttpClient(
                    MockEngine {
                        requests++
                        respond(
                            """{"code":"traffic_budget_exhausted","retry_at":"2026-11-01T00:00:00.000Z","error":"untrusted-secret"}""",
                            HttpStatusCode.TooManyRequests,
                            if (header)
                                headersOf(
                                    "X-Psst-Error-Code" to listOf("traffic_budget_exhausted"),
                                    "X-Psst-Retry-At" to listOf("2026-11-01T00:00:00Z"),
                                )
                            else headersOf(),
                        )
                    }
                ) {
                    applyClientPolicy()
                }
            val api = ApiClient(ServerConfig("https://server.test"), http, "session")
            try {
                val error =
                    assertFailsWith<TrafficBudgetExhaustedException> {
                        api.transfers.downloadManifest("id")
                    }
                assertEquals("2026-11-01T00:00:00Z", error.retryAt)
                assertEquals("Traffic budget reached", error.title)
                assertTrue(error.message!!.contains("2026-11-01"))
                assertFalse(error.message!!.contains("secret"))
                assertEquals(1, requests)
            } finally {
                api.close()
            }
        }
    }

    @Test
    fun budgetTimestampIsOptionalAndHostileValuesNeverAppearInUi() = transportTest {
        for (timestamp in
            listOf(
                "not-a-date",
                "2026-99-01T00:00:00Z",
                "2026-11-01T00:00:00+00:00",
                "x".repeat(100),
            )) {
            val error = TrafficBudgetExhaustedException(timestamp)
            assertNull(error.retryAt)
            assertFalse(error.message!!.contains(timestamp))
        }
        val http =
            HttpClient(
                MockEngine {
                    respond(
                        """{"code":"traffic_budget_exhausted","retry_at":{},"error":"secret"}""",
                        HttpStatusCode.TooManyRequests,
                    )
                }
            ) {
                applyClientPolicy()
            }
        val api = ApiClient(ServerConfig("https://server.test"), http)
        try {
            assertNull(
                assertFailsWith<TrafficBudgetExhaustedException> { api.transfers.create() }.retryAt
            )
        } finally {
            api.close()
        }
    }

    @Test
    fun accountingFailureIsDistinctFromBudgetExhaustionAndRateLimiting() = transportTest {
        for ((status, code) in
            listOf(
                503 to "traffic_accounting_unavailable",
                429 to "traffic_accounting_unavailable",
                503 to "traffic_budget_exhausted",
                429 to "rate_limited",
            )) {
            var requests = 0
            val http =
                HttpClient(
                    MockEngine {
                        requests++
                        respond("""{"code":"$code"}""", HttpStatusCode.fromValue(status))
                    }
                ) {
                    applyClientPolicy()
                }
            val api = ApiClient(ServerConfig("https://server.test"), http, "session")
            try {
                val error = assertFails {
                    api.tus.uploadChunk(
                        "https://server.test/api/v1/transfers/id/files/file",
                        ByteArray(63),
                        0,
                    )
                }
                assertEquals(
                    status == 503 && code == "traffic_accounting_unavailable",
                    error is TrafficAccountingUnavailableException,
                )
                assertFalse(error is AuthenticationRequiredException)
                assertEquals(1, requests)
            } finally {
                api.close()
            }
        }
    }
}
