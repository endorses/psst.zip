package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.toByteArray
import io.ktor.client.request.get
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest

class LinkDeletionTest {
    private val config = ServerConfig("https://original.example")

    @Test
    fun bearerIsSentOnlyForAuthenticatedDeleteAndLegacySendsNone() = runTest {
        val requests = mutableListOf<String>()
        val http =
            HttpClient(MockEngine) {
                expectSuccess = true
                engine {
                    dispatcher = StandardTestDispatcher(testScheduler)
                    addHandler { request ->
                        requests += request.url.encodedPath
                        assertEquals(0, request.body.toByteArray().size)
                        assertEquals("", request.url.fragment)
                        assertEquals(0, request.url.parameters.names().size)
                        if (
                            request.method == HttpMethod.Delete &&
                                request.url.encodedPath.endsWith("owned")
                        ) {
                            assertEquals(
                                "Bearer owner-secret",
                                request.headers[HttpHeaders.Authorization],
                            )
                        } else assertEquals(null, request.headers[HttpHeaders.Authorization])
                        respond("", HttpStatusCode.NoContent)
                    }
                }
            }
        try {
            TransferApi(http, config).delete("owned", "owner-secret")
            SlotApi(http, config).delete("owned", "owner-secret")
            TransferApi(http, config).delete("legacy")
            SlotApi(http, config).delete("legacy")
            http.get("${config.apiBaseUrl}/transfers/owned")
            assertEquals(
                listOf(
                    "/api/v1/transfers/owned",
                    "/api/v1/slots/owned",
                    "/api/v1/transfers/legacy",
                    "/api/v1/slots/legacy",
                    "/api/v1/transfers/owned",
                ),
                requests,
            )
        } finally {
            http.close()
        }
    }

    @Test
    fun onlyNoContentAndMissingCountAsSuccessEvenWithExpectSuccess() = runTest {
        for (code in listOf(204, 404, 200, 401, 403, 405, 500)) {
            val http =
                HttpClient(MockEngine) {
                    expectSuccess = true
                    engine {
                        dispatcher = StandardTestDispatcher(testScheduler)
                        addHandler {
                            respond("untrusted server body", HttpStatusCode.fromValue(code))
                        }
                    }
                }
            try {
                for (slot in listOf(false, true)) {
                    suspend fun revoke() {
                        if (slot) SlotApi(http, config).delete("id", "secret")
                        else TransferApi(http, config).delete("id", "secret")
                    }
                    if (code in listOf(204, 404)) revoke()
                    else {
                        val error = assertFailsWith<LinkDeletionException> { revoke() }
                        assertEquals(code, error.statusCode)
                        assertTrue(!error.message.orEmpty().contains("untrusted"))
                        if (code == 405)
                            assertTrue(error.message.orEmpty().contains("Update the server"))
                    }
                }
            } finally {
                http.close()
            }
        }
    }

    @Test
    fun requestsAreBoundedAndCallerCancellationPropagates() = runTest {
        val http =
            HttpClient(MockEngine) {
                engine {
                    dispatcher = StandardTestDispatcher(testScheduler)
                    addHandler {
                        delay(20_000)
                        respond("", HttpStatusCode.NoContent)
                    }
                }
            }
        try {
            assertFailsWith<LinkDeletionException> { TransferApi(http, config).delete("slow") }
            assertEquals(10_000L, testScheduler.currentTime)
        } finally {
            http.close()
        }
        val cancelled =
            HttpClient(MockEngine) {
                engine {
                    dispatcher = StandardTestDispatcher(testScheduler)
                    addHandler { throw CancellationException("Cancelled") }
                }
            }
        try {
            assertFailsWith<CancellationException> { SlotApi(cancelled, config).delete("slot") }
        } finally {
            cancelled.close()
        }
    }
}
