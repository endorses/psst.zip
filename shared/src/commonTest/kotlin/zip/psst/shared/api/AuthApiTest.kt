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
    fun administratorDeviceLoginWithoutSessionReturnsSafeRestrictionAndAllowsRegularLogin() =
        runTest {
            var requests = 0
            val client =
                HttpClient(
                    MockEngine { request ->
                        requests++
                        assertEquals("/api/v1/auth/login", request.url.encodedPath)
                        assertNull(request.headers[HttpHeaders.Authorization])
                        val body =
                            Json.decodeFromString<Map<String, String>>(
                                request.body.toByteArray().decodeToString()
                            )
                        assertEquals("device", body["session_type"])
                        if (requests == 1) {
                            assertEquals("admin", body["username"])
                            respond(
                                """{"code":"admin_transfer_forbidden","error":"private administrator diagnostic"}""",
                                HttpStatusCode.Forbidden,
                                headersOf(
                                    HttpHeaders.ContentType,
                                    ContentType.Application.Json.toString(),
                                ),
                            )
                        } else {
                            assertEquals("alice", body["username"])
                            respond(
                                """{"token":"restricted-device-token","user":{"id":"u1","username":"alice","role":"user","must_change_password":true},"session_id":"s1","expires_at":"2026-11-01T00:00:00Z"}""",
                                HttpStatusCode.OK,
                                headersOf(
                                    HttpHeaders.ContentType,
                                    ContentType.Application.Json.toString(),
                                ),
                            )
                        }
                    }
                ) {
                    install(ContentNegotiation) { json() }
                }
            try {
                // Login must remain anonymous even when this client holds an existing account
                // session.
                val api =
                    AuthApi(client, ServerConfig("https://files.example.com"), "existing-session")
                val error =
                    assertFailsWith<AdminTransferForbiddenException> {
                        withContext(Dispatchers.Default) { api.login("admin", "secret", "Android") }
                    }
                assertEquals(1, requests)
                assertTrue(error.message.orEmpty().contains("regular account"))
                assertFalse(error.message.orEmpty().contains("private administrator diagnostic"))

                val session =
                    withContext(Dispatchers.Default) {
                        api.login("alice", "temporary secret", "Android")
                    }
                assertEquals(2, requests)
                assertEquals("restricted-device-token", session.token)
                assertEquals("user", session.user.role)
                assertTrue(session.user.mustChangePassword)
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
    fun ownerClientReadsAndWritesUseItsScopedSession() = runTest {
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
                            assertEquals(
                                "Bearer session-secret",
                                request.headers[HttpHeaders.Authorization],
                            )
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

    @Test
    fun currentUserCarriesRequiredPasswordStateAndOldResponsesRemainReadable() = runTest {
        withContext(Dispatchers.Default) {
            for (flag in listOf("", ",\"must_change_password\":true")) {
                val client =
                    HttpClient(
                        MockEngine { request ->
                            assertEquals("/api/v1/auth/me", request.url.encodedPath)
                            assertEquals("Bearer token", request.headers[HttpHeaders.Authorization])
                            respond(
                                """{"user":{"id":"u","username":"alice","role":"user"$flag}}""",
                                HttpStatusCode.OK,
                                headersOf(HttpHeaders.ContentType, "application/json"),
                            )
                        }
                    ) {
                        install(ContentNegotiation) { json() }
                    }
                try {
                    val user =
                        AuthApi(client, ServerConfig("https://files.example.com"), "token").me()
                    assertEquals(flag.isNotEmpty(), user.mustChangePassword)
                } finally {
                    client.close()
                }
            }
        }
    }

    @Test
    fun passwordReplacementUsesCurrentSessionAndKeepsSecretsOutOfErrors() = runTest {
        withContext(Dispatchers.Default) {
            for (status in
                listOf(
                    HttpStatusCode.NoContent,
                    HttpStatusCode.Forbidden,
                    HttpStatusCode.Unauthorized,
                )) {
                val client =
                    HttpClient(
                        MockEngine { request ->
                            assertEquals("/api/v1/auth/password", request.url.encodedPath)
                            assertEquals("Bearer token", request.headers[HttpHeaders.Authorization])
                            val body = request.body.toByteArray().decodeToString()
                            assertTrue(body.contains("\"current_password\":\"temporary secret\""))
                            assertTrue(body.contains("\"password\":\"replacement secret\""))
                            respond("private server diagnostic", status)
                        }
                    ) {
                        install(ContentNegotiation) { json() }
                    }
                try {
                    val api = AuthApi(client, ServerConfig("https://files.example.com"), "token")
                    if (status == HttpStatusCode.NoContent)
                        api.changePassword("temporary secret", "replacement secret")
                    else {
                        val failure =
                            assertFailsWith<IllegalArgumentException> {
                                api.changePassword("temporary secret", "replacement secret")
                            }
                        assertFalse(failure.message.orEmpty().contains("private"))
                        if (status == HttpStatusCode.Unauthorized)
                            assertTrue(failure is AuthenticationRequiredException)
                    }
                } finally {
                    client.close()
                }
            }
        }
    }

    @Test
    fun restrictionCodesStayDistinctAcrossOwnerWritesAndPairing() = runTest {
        withContext(Dispatchers.Default) {
            for (code in listOf("password_change_required", "admin_transfer_forbidden")) {
                val client =
                    HttpClient(
                        MockEngine {
                            respond(
                                """{"code":"$code","error":"private diagnostic"}""",
                                HttpStatusCode.Forbidden,
                            )
                        }
                    ) {
                        install(ContentNegotiation) { json() }
                    }
                try {
                    suspend fun verify(action: suspend () -> Unit) {
                        val failure = assertFailsWith<IllegalArgumentException> { action() }
                        if (code == "password_change_required")
                            assertTrue(failure is PasswordChangeRequiredException)
                        else assertTrue(failure is AdminTransferForbiddenException)
                        assertFalse(failure.message.orEmpty().contains("private"))
                    }
                    verify {
                        TransferApi(client, ServerConfig("https://files.example.com"), "token")
                            .create()
                    }
                    verify {
                        AuthApi(client, ServerConfig("https://files.example.com"), "token")
                            .resources()
                    }
                    verify {
                        AuthApi(client, ServerConfig("https://files.example.com"))
                            .redeemPairing("code", "phone")
                    }
                } finally {
                    client.close()
                }
            }
        }
    }

    @Test
    fun unknownMalformedAndOversizedRestrictionsUseSafeFallback() = runTest {
        withContext(Dispatchers.Default) {
            for (body in
                listOf("not json", """{"code":"unknown","error":"secret"}""", "x".repeat(5000))) {
                val client =
                    HttpClient(MockEngine { respond(body, HttpStatusCode.Forbidden) }) {
                        install(ContentNegotiation) { json() }
                    }
                try {
                    assertFailsWith<AuthenticationRequiredException> {
                        TransferApi(client, ServerConfig("https://files.example.com"), "token")
                            .create()
                    }
                } finally {
                    client.close()
                }
            }
        }
    }
}
