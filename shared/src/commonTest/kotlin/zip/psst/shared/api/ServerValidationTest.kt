package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.HttpRequestData
import io.ktor.client.request.HttpResponseData
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest

class ServerValidationTest {
    // Keep HTTP handlers on the same virtual clock as validation's timeout.
    private fun TestScope.mockClient(
        handler: suspend MockRequestHandleScope.(HttpRequestData) -> HttpResponseData
    ): HttpClient =
        HttpClient(MockEngine) {
            engine {
                dispatcher = StandardTestDispatcher(testScheduler)
                addHandler(handler)
            }
        }

    private val marker = "<html><head><meta content = '1'\n name = \"psst-web\" ></head></html>"

    @Test
    fun acceptsApiAndBothPublicPagesWithoutCreatingResources() = runTest {
        val paths = mutableListOf<String>()
        val http =
            mockClient({ request ->
                assertEquals(HttpMethod.Get, request.method)
                paths += request.url.encodedPath
                if (request.url.encodedPath == "/api/v1/health") {
                    respond(
                        """{"service":"psst.zip","api_version":1}""",
                        headers =
                            headersOf(HttpHeaders.ContentType, "application/json; charset=utf-8"),
                    )
                } else {
                    respond(
                        marker,
                        headers = headersOf(HttpHeaders.ContentType, "text/html; charset=UTF-8"),
                    )
                }
            })
        val client = ApiClient(ServerConfig("https://transfer.example.com/"), http)
        try {
            client.validateServer()
            assertEquals(
                listOf("/api/v1/health", "/d/_connection_check", "/u/_connection_check"),
                paths,
            )
        } finally {
            client.close()
        }
    }

    @Test
    fun rejectsUnsafeOrNonOriginAddressesBeforeAnyRequest() = runTest {
        val http = mockClient({ error("Invalid URLs must not reach the network") })
        try {
            for (url in
                listOf(
                    "",
                    "example.com",
                    "ftp://192.168.1.2:8080",
                    "http://user:pass@192.168.1.2:8080",
                    "http://192.168.1.2:8080/path",
                    "http://192.168.1.2:8080?x=1",
                    "http://192.168.1.2:8080#key",
                    "https://user:pass@example.com",
                    "https://example.com/path",
                    "https://example.com?x=1",
                    "https://example.com#secret",
                    "https://example.com//",
                    " https://example.com",
                    "https://example.com\\path",
                )) {
                assertFailsWith<IllegalArgumentException>(url) {
                    ApiClient(ServerConfig(url), http).validateServer()
                }
            }
        } finally {
            http.close()
        }
    }

    @Test
    fun acceptsHttpAndHttpsForUserConfiguredLanPublicAndLoopbackOrigins() = runTest {
        for (origin in
            listOf(
                "http://localhost:8080",
                "http://127.0.0.1:8080",
                "http://[::1]:8080",
                "http://192.168.1.2:8080",
                "http://10.4.8.12:9000",
                "http://[fd12:3456::42]:8080",
                "http://transfer.example.net",
                "https://transfer.example.net",
            )) {
            val http =
                mockClient({ request ->
                    assertEquals(origin.substringBefore("://"), request.url.protocol.name)
                    assertTrue(request.url.toString().startsWith("$origin/"))
                    if (request.url.encodedPath.endsWith("/health")) {
                        respond(
                            """{"service":"psst.zip","api_version":1}""",
                            headers = headersOf(HttpHeaders.ContentType, "application/json"),
                        )
                    } else {
                        respond(marker, headers = headersOf(HttpHeaders.ContentType, "text/html"))
                    }
                })
            try {
                ApiClient(ServerConfig(origin), http).validateServer()
            } finally {
                http.close()
            }
        }
    }

    @Test
    fun doesNotRetryHttpsFailureUsingHttp() = runTest {
        val requestedUrls = mutableListOf<String>()
        val http =
            mockClient({ request ->
                requestedUrls += request.url.toString()
                throw IllegalStateException("Certificate validation failed")
            })
        try {
            val failure =
                assertFailsWith<IllegalStateException> {
                    ApiClient(ServerConfig("https://transfer.example.net"), http).validateServer()
                }
            assertTrue(failure.message.orEmpty().contains("Certificate validation failed"))
            assertEquals(listOf("https://transfer.example.net/api/v1/health"), requestedUrls)
        } finally {
            http.close()
        }
    }

    @Test
    fun rejectsWrongApiVersionOrIdentity() = runTest {
        for (body in
            listOf(
                """{"service":"other","api_version":1}""",
                """{"service":"psst.zip","api_version":2}""",
                """{"service":"psst.zip","api_version":"1"}""",
                "{}",
                "not JSON",
            )) {
            val http =
                mockClient({
                    respond(body, headers = headersOf(HttpHeaders.ContentType, "application/json"))
                })
            try {
                assertFailsWith<IllegalStateException> {
                    ApiClient(ServerConfig("https://example.com"), http).validateServer()
                }
            } finally {
                http.close()
            }
        }
    }

    @Test
    fun rejectsMissingOrWrongWebPageOnEitherRoute() = runTest {
        for (missing in listOf("/d/", "/u/")) {
            val http =
                mockClient({ request ->
                    when {
                        request.url.encodedPath.endsWith("/health") ->
                            respond(
                                """{"service":"psst.zip","api_version":1}""",
                                headers = headersOf(HttpHeaders.ContentType, "application/json"),
                            )
                        request.url.encodedPath.startsWith(missing) ->
                            respond(
                                "<html>Other app</html>",
                                headers = headersOf(HttpHeaders.ContentType, "text/html"),
                            )
                        else ->
                            respond(
                                marker,
                                headers = headersOf(HttpHeaders.ContentType, "text/html"),
                            )
                    }
                })
            try {
                val failure =
                    assertFailsWith<IllegalStateException> {
                        ApiClient(ServerConfig("https://example.com"), http).validateServer()
                    }
                assertTrue(failure.message.orEmpty().contains("web app is missing"))
            } finally {
                http.close()
            }
        }
    }

    @Test
    fun rejectsHttpErrorAndOversizedResponses() = runTest {
        for (large in listOf(false, true)) {
            val http =
                mockClient({
                    respond(
                        if (large) "x".repeat(16 * 1024 + 1) else "Not found",
                        status = if (large) HttpStatusCode.OK else HttpStatusCode.NotFound,
                        headers = headersOf(HttpHeaders.ContentType, "application/json"),
                    )
                })
            try {
                assertFailsWith<IllegalStateException> {
                    ApiClient(ServerConfig("https://example.com"), http).validateServer()
                }
            } finally {
                http.close()
            }
        }
    }

    @Test
    fun rejectsWrongContentTypeAndNetworkFailures() = runTest {
        val wrongType =
            mockClient({
                respond(
                    """{"service":"psst.zip","api_version":1}""",
                    headers = headersOf(HttpHeaders.ContentType, "text/html"),
                )
            })
        try {
            assertFailsWith<IllegalStateException> {
                ApiClient(ServerConfig("https://example.com"), wrongType).validateServer()
            }
        } finally {
            wrongType.close()
        }
        val offline = mockClient({ throw IllegalStateException("Connection refused") })
        try {
            val failure =
                assertFailsWith<IllegalStateException> {
                    ApiClient(ServerConfig("https://example.com"), offline).validateServer()
                }
            assertTrue(failure.message.orEmpty().contains("Connection refused"))
        } finally {
            offline.close()
        }
    }

    @Test
    fun timeoutIsActionableAndExternalCancellationPropagates() = runTest {
        val slow =
            mockClient({
                delay(20_000)
                error("Should time out")
            })
        try {
            val failure =
                assertFailsWith<IllegalStateException> {
                    ApiClient(ServerConfig("https://example.com"), slow).validateServer()
                }
            assertTrue(failure.message.orEmpty().contains("timed out"))
        } finally {
            slow.close()
        }
        val cancelled = mockClient({ throw CancellationException("Cancelled by caller") })
        try {
            assertFailsWith<CancellationException> {
                ApiClient(ServerConfig("https://example.com"), cancelled).validateServer()
            }
        } finally {
            cancelled.close()
        }
    }
}
