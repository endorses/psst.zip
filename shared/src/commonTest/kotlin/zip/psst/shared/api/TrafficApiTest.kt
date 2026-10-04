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
import kotlinx.serialization.json.*

class TrafficApiTest {
    private val policy =
        """{"enforcement_enabled":true,"server_budget_bytes":1000,"default_account_budget_bytes":500,"basis":"outbound","cycle_start_day":1,"upload_bytes_per_second":104857600,"download_bytes_per_second":104857600,"max_active_streams":64,"max_streams_per_account":4,"max_streams_per_ip":4,"max_streams_per_transfer":4,"max_streams_per_slot":4}"""
    private val snapshot =
        """{"policy":$policy,"usage":{"observed_uploaded_bytes":0,"observed_downloaded_bytes":10,"reserved_uploaded_bytes":0,"reserved_downloaded_bytes":20,"conservative_uploaded_bytes":0,"conservative_downloaded_bytes":0,"charged_bytes":30,"budget_bytes":500,"remaining_bytes":470},"recording_started_at":"2026-10-01T00:00:00Z","cycle":{"start":"2026-10-01T00:00:00Z","end":"2026-11-01T00:00:00Z"},"lease_bytes":65536,"state":"ready"}"""

    private fun transportTest(block: suspend () -> Unit) = runTest {
        withContext(Dispatchers.Default) { block() }
    }

    @Test
    fun publicPolicyRemainsAnonymousAndAccountUsageIsScopedAndOneShot() = transportTest {
        var requests = 0
        val http =
            HttpClient(
                MockEngine { r ->
                    requests++
                    assertEquals("owner.test", r.url.host)
                    if (r.url.encodedPath.endsWith("/config")) {
                        assertNull(r.headers[HttpHeaders.Authorization])
                        respond(
                            """{"max_file_size":26214400,"max_file_size_ceiling":1099511627776,"traffic_policy":$policy}"""
                        )
                    } else {
                        assertEquals("/api/v1/auth/traffic-usage", r.url.encodedPath)
                        assertEquals("Bearer owner-token", r.headers[HttpHeaders.Authorization])
                        respond(snapshot)
                    }
                }
            ) {
                applyClientPolicy()
            }
        val api = ApiClient(ServerConfig("https://owner.test"), http, "owner-token")
        try {
            val limits = api.limits.get()
            assertEquals(26214400, limits.maxFileSize)
            assertTrue(limits.trafficPolicy!!.enforcementEnabled)
            val usage = api.traffic.usage()
            assertEquals(470, usage.usage.remainingBytes)
            assertEquals(2, requests)
        } finally {
            api.close()
        }
    }

    @Test
    fun invalidPolicyUsageCycleAndOversizedConfigFailClosed() = transportTest {
        for (body in
            listOf(
                snapshot.replace("\"remaining_bytes\":470", "\"remaining_bytes\":-1"),
                snapshot.replace("\"basis\":\"outbound\"", "\"basis\":\"unknown\""),
                snapshot.replace("2026-11-01T00:00:00Z", "2026-09-01T00:00:00Z"),
                snapshot.replace("\"lease_bytes\":65536", "\"lease_bytes\":0"),
            )) {
            val api =
                ApiClient(
                    ServerConfig("https://owner.test"),
                    HttpClient(MockEngine { respond(body) }) { applyClientPolicy() },
                    "token",
                )
            try {
                assertFailsWith<IllegalArgumentException> { api.traffic.usage() }
            } finally {
                api.close()
            }
        }
        val http =
            HttpClient(
                MockEngine {
                    respond(" ".repeat(65537) + """{"max_file_size":1,"max_file_size_ceiling":1}""")
                }
            ) {
                applyClientPolicy()
            }
        val api = ApiClient(ServerConfig("https://owner.test"), http)
        try {
            assertFailsWith<IllegalArgumentException> { api.limits.get() }
        } finally {
            api.close()
        }
    }
}
