package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.toByteArray
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json

class AuthApiTest {
    @Test
    fun resourcesAreAuthenticatedAccountScopedAndContainNoLocalKeys() = runTest {
        val client =
            HttpClient(
                MockEngine { request ->
                    assertEquals("/api/v1/auth/resources", request.url.encodedPath)
                    assertEquals("Bearer device-token", request.headers[HttpHeaders.Authorization])
                    assertNull(request.url.parameters["all"])
                    respond(
                        """{"transfers":[{"id":"remote","status":"revoked","file_count":2}],"slots":[{"id":"receive","status":"has_uploads","transfers":[{"transfer_id":"child","status":"complete","file_count":3}]}]}""",
                        HttpStatusCode.OK,
                        headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString()),
                    )
                }
            ) {
                install(ContentNegotiation) { json() }
            }
        try {
            val resources =
                withContext(Dispatchers.Default) {
                    AuthApi(client, ServerConfig("https://files.example.com"), "device-token")
                        .resources()
                }
            assertEquals("revoked", resources.transfers.single().status)
            assertEquals(3, resources.slots.single().transfers.single().fileCount)
        } finally {
            client.close()
        }
    }

    @Test
    fun pairingAcceptsOnlyVersionedOriginAndSingleUseSecret() {
        val code = "a".repeat(43)
        val valid =
            """{"type":"psst-pairing","version":1,"server_url":"https://files.example.com","code":"$code"}"""
        assertEquals("https://files.example.com", PairingCode.parse(valid).serverUrl)
        for (bad in
            listOf(
                valid.replace("psst-pairing", "other"),
                valid.replace(":1", ":2"),
                valid.replace(
                    "https://files.example.com",
                    "https://user:password@files.example.com",
                ),
                valid.replace("https://files.example.com", "https://files.example.com/path"),
                valid.replace(code, "short"),
                "https://files.example.com/u/id#key",
            )) {
            assertFailsWith<IllegalArgumentException> { PairingCode.parse(bad) }
        }
    }

    @Test
    fun loginRequestsDeviceSessionAndRedactsServerError() = runTest {
        val client =
            HttpClient(
                MockEngine { request ->
                    assertEquals("/api/v1/auth/login", request.url.encodedPath)
                    val body = request.body.toByteArray().decodeToString()
                    assertTrue(body.contains("\"session_type\":\"device\""))
                    assertNull(request.headers[HttpHeaders.Authorization])
                    respond("password or token must not be displayed", HttpStatusCode.Unauthorized)
                }
            ) {
                install(ContentNegotiation) { json() }
            }
        try {
            val error =
                assertFailsWith<IllegalStateException> {
                    withContext(Dispatchers.Default) {
                        AuthApi(client, ServerConfig("https://files.example.com"))
                            .login("alice", "secret", "Android")
                    }
                }
            assertFalse(error.message.orEmpty().contains("must not be displayed"))
        } finally {
            client.close()
        }
    }

    @Test
    fun pairingExchangesForIndependentDeviceSession() = runTest {
        val client =
            HttpClient(
                MockEngine { request ->
                    assertEquals("/api/v1/auth/pairings/redeem", request.url.encodedPath)
                    assertNull(request.headers[HttpHeaders.Authorization])
                    respond(
                        """{"token":"device-secret","user":{"id":"u1","username":"alice","role":"user","disabled":false},"session_id":"s1","expires_at":"2026-11-01T00:00:00Z"}""",
                        HttpStatusCode.OK,
                        headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString()),
                    )
                }
            ) {
                install(ContentNegotiation) { json(Json { ignoreUnknownKeys = true }) }
            }
        try {
            val session =
                withContext(Dispatchers.Default) {
                    AuthApi(client, ServerConfig("https://files.example.com"))
                        .redeemPairing("a".repeat(43), "Android")
                }
            assertEquals("device-secret", session.token)
            assertEquals("alice", session.user.username)
            assertFalse(session.toString().contains("device-secret"))
        } finally {
            client.close()
        }
    }

    @Test
    fun ownerWritesSendSessionButPublicReadsDoNot() = runTest {
        val client =
            HttpClient(
                MockEngine { request ->
                    when (request.url.encodedPath) {
                        "/api/v1/transfers" -> {
                            assertEquals(
                                "Bearer session-secret",
                                request.headers[HttpHeaders.Authorization],
                            )
                            respond(
                                """{"id":"id","status":"pending"}""",
                                HttpStatusCode.Created,
                                headersOf(
                                    HttpHeaders.ContentType,
                                    ContentType.Application.Json.toString(),
                                ),
                            )
                        }
                        "/api/v1/transfers/id" -> {
                            assertNull(request.headers[HttpHeaders.Authorization])
                            respond(
                                """{"id":"id","status":"pending"}""",
                                HttpStatusCode.OK,
                                headersOf(
                                    HttpHeaders.ContentType,
                                    ContentType.Application.Json.toString(),
                                ),
                            )
                        }
                        else -> {
                            assertEquals(
                                "Bearer session-secret",
                                request.headers[HttpHeaders.Authorization],
                            )
                            respond("", HttpStatusCode.NoContent)
                        }
                    }
                }
            ) {
                install(ContentNegotiation) { json(Json { ignoreUnknownKeys = true }) }
            }
        try {
            val api =
                TransferApi(client, ServerConfig("https://files.example.com"), "session-secret")
            api.create()
            api.get("id")
            api.uploadManifest("id", byteArrayOf(1))
            api.complete("id")
        } finally {
            client.close()
        }
    }

    @Test
    fun revokedSessionProducesSignInErrorWithoutServerBody() = runTest {
        val client =
            HttpClient(MockEngine { respond("secret server body", HttpStatusCode.Unauthorized) }) {
                install(ContentNegotiation) { json() }
            }
        try {
            val error =
                assertFailsWith<AuthenticationRequiredException> {
                    TransferApi(client, ServerConfig("https://files.example.com"), "revoked-token")
                        .create()
                }
            assertTrue(error.message.orEmpty().contains("Sign in"))
            assertFalse(error.message.orEmpty().contains("secret"))
        } finally {
            client.close()
        }
    }

    @Test
    fun authenticatedTusRejectsCrossOriginLocationBeforeUpload() = runTest {
        var requests = 0
        val client =
            HttpClient(
                MockEngine { request ->
                    requests++
                    assertEquals("files.example.com", request.url.host)
                    assertEquals(
                        "Bearer session-secret",
                        request.headers[HttpHeaders.Authorization],
                    )
                    respond(
                        "",
                        HttpStatusCode.Created,
                        headersOf(HttpHeaders.Location, "https://attacker.example/upload"),
                    )
                }
            )
        try {
            val tus = TusClient(client, "https://files.example.com", "session-secret")
            assertFailsWith<IllegalArgumentException> {
                tus.create("https://files.example.com/api/v1/transfers/id/files", 1)
            }
            assertFailsWith<IllegalArgumentException> {
                tus.upload("https://attacker.example/upload", byteArrayOf(1))
            }
            assertEquals(1, requests)
        } finally {
            client.close()
        }
    }
}
