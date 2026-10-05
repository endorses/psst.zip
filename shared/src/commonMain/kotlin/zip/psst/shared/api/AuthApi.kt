package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.ServerOrigin
import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.bearerAuth
import io.ktor.client.request.get
import io.ktor.client.request.parameter
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.contentType
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

@Serializable
data class AuthUser(
    val id: String,
    val username: String,
    val role: String,
    val disabled: Boolean = false,
    @SerialName("must_change_password") val mustChangePassword: Boolean = false,
)

@Serializable
class AuthSession(
    val token: String,
    val user: AuthUser,
    @SerialName("session_id") val sessionId: String,
    @SerialName("expires_at") val expiresAt: String,
)

/** Single-use pairing secrets are never passwords or durable session tokens. */
@Serializable
class PairingCode(
    val type: String,
    val version: Int,
    @SerialName("server_url") val serverUrl: String,
    val code: String,
) {
    companion object {
        @Throws(Exception::class)
        fun parse(raw: String): PairingCode {
            require(raw.length <= 4096) { "This is not a server login QR code" }
            val pairing =
                try {
                    Json.decodeFromString<PairingCode>(raw)
                } catch (_: Exception) {
                    throw IllegalArgumentException("This is not a server login QR code")
                }
            require(
                pairing.type == "psst-pairing" &&
                    pairing.version == 1 &&
                    pairing.code.matches(Regex("[A-Za-z0-9_-]{32,128}"))
            ) {
                "This is not a supported server login QR code"
            }
            val normalizedOrigin = ServerOrigin.normalize(pairing.serverUrl)
            require(normalizedOrigin != null) {
                "The pairing code contains an invalid server address"
            }
            return PairingCode(pairing.type, pairing.version, normalizedOrigin, pairing.code)
        }
    }
}

@Serializable private data class CurrentUserResponse(val user: AuthUser)

class AuthApi(
    private val client: HttpClient,
    private val config: ServerConfig,
    private val token: String? = null,
) {
    @Throws(Exception::class)
    suspend fun me(): AuthUser =
        withTimeout(10_000L) {
            val response =
                client.get("${config.apiBaseUrl}/auth/me") {
                    expectSuccess = false
                    token?.let { bearerAuth(it) }
                }
            response.checkAuthenticatedWrite()
            response.body<CurrentUserResponse>().user
        }

    /** Password replacement revokes this session; the caller must sign in again. */
    @Throws(Exception::class)
    suspend fun changePassword(currentPassword: String, password: String): Unit =
        withTimeout(15_000L) {
            val response =
                client.post("${config.apiBaseUrl}/auth/password") {
                    expectSuccess = false
                    token?.let { bearerAuth(it) }
                    contentType(ContentType.Application.Json)
                    setBody(mapOf("current_password" to currentPassword, "password" to password))
                }
            if (response.status.value == 401) throw AuthenticationRequiredException()
            require(response.status.value == 204) {
                when (response.status.value) {
                    403 -> "The current password is incorrect."
                    400 -> "Use a different password containing 12–72 UTF-8 bytes."
                    else -> "Could not change your password. Try again later."
                }
            }
        }

    /** Bounded page API for native callers that expose explicit incremental history loading. */
    @Throws(Exception::class)
    suspend fun resourcesPage(after: String?, limit: Int): AuthResources =
        resourcesPage(after, limit, null)

    @Throws(Exception::class)
    suspend fun resourcesPage(after: String?, limit: Int, kind: String?): AuthResources =
        withTimeout(10_000L) {
            require(limit in 1..100)
            require(kind == null || kind in listOf("transfer", "slot"))
            require(
                after == null || (after.length in 1..512 && after.matches(Regex("[A-Za-z0-9_-]+")))
            )
            val response =
                client.get("${config.apiBaseUrl}/auth/resources") {
                    expectSuccess = false
                    token?.let { bearerAuth(it) }
                    parameter("limit", limit)
                    kind?.let { parameter("kind", it) }
                    after?.let { parameter("after", it) }
                }
            response.checkAuthenticatedWrite()
            decodeResourcePage(
                response.readControlJson<kotlinx.serialization.json.JsonObject>(1024 * 1024),
                after,
                limit,
            )
        }

    @Throws(Exception::class)
    suspend fun login(username: String, password: String, deviceName: String): AuthSession =
        exchange(
            "login",
            mapOf(
                "username" to username,
                "password" to password,
                "device_name" to deviceName,
                "session_type" to "device",
            ),
        )

    @Throws(Exception::class)
    suspend fun redeemPairing(code: String, deviceName: String): AuthSession =
        exchange("pairings/redeem", mapOf("code" to code, "device_name" to deviceName))

    private suspend fun exchange(path: String, values: Map<String, String>): AuthSession =
        withTimeout(15_000L) {
            val response =
                client.post("${config.apiBaseUrl}/auth/$path") {
                    expectSuccess = false
                    contentType(ContentType.Application.Json)
                    setBody(values)
                }
            response.checkAccountRestriction()
            check(response.status.value in 200..299) {
                when (response.status.value) {
                    400,
                    401,
                    403 ->
                        if (path == "login")
                            "Sign-in failed. Check your credentials and use HTTPS unless the server explicitly allows development HTTP."
                        else
                            "Pairing failed. The code may have expired or already been used. Generate a new one in the web app."
                    429 -> "Too many sign-in attempts. Please wait and try again."
                    404 -> "This server needs an update before it supports account sign-in."
                    else -> "The server could not sign you in. Try again later."
                }
            }
            response.body<AuthSession>().also {
                require(it.token.isNotBlank()) { "The server returned an invalid session" }
            }
        }

    @Throws(Exception::class)
    suspend fun logout() =
        withTimeout(10_000L) {
            val response =
                client.post("${config.apiBaseUrl}/auth/logout") {
                    expectSuccess = false
                    token?.let { bearerAuth(it) }
                }
            check(response.status.value in listOf(204, 401)) {
                "Could not sign out on the server. Try again when connected."
            }
        }
}
