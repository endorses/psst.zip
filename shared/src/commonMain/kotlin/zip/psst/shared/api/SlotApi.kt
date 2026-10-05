package zip.psst.shared.api

import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.LinkTitle
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.SlotAvailability
import zip.psst.shared.model.Transfer
import zip.psst.shared.model.UrlHelper
import io.ktor.client.HttpClient
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.bearerAuth
import io.ktor.client.request.get
import io.ktor.client.request.parameter
import io.ktor.client.request.patch
import io.ktor.client.request.post
import io.ktor.client.request.prepareGet
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsChannel
import io.ktor.http.ContentType
import io.ktor.http.contentType
import io.ktor.utils.io.cancel
import io.ktor.utils.io.readUTF8Line
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject

@Serializable
private data class CreateReceiveRequest(
    val receive_protocol: Int = 2,
    val recipient_public_key: String,
    val max_files: Int,
    val title: String?,
)

/** Represents a Server-Sent Event from the slot events endpoint. */
data class SlotEvent(val event: String, val data: String)

// Notifications carry small identities, not manifests or file contents. Bound decoded characters
// both per line and across an event, including ignored fields, before retaining any server input.
private const val MAX_EVENT_LINE_CHARS = 4096
private const val MAX_EVENT_CHARS = 16 * 1024

/** API operations for drop slots (receive flow). */
class SlotApi(
    private val httpClient: HttpClient,
    private val config: ServerConfig,
    private val sessionToken: String? = null,
) {
    /** Create a new drop slot. Returns the slot with its server-assigned ID. */
    /** Metadata-only classification after interrupted IO; never downloads file data. */
    @Throws(Exception::class)
    suspend fun trafficStatus(id: String): TransferTrafficStatus = trafficStatus(id, "upload")

    @Throws(Exception::class)
    suspend fun trafficStatus(id: String, direction: String): TransferTrafficStatus {
        require(direction in listOf("upload", "download")) { "Invalid traffic direction" }
        require(id.matches(Regex("[0-9a-fA-F-]{36}"))) { "Invalid resource ID" }
        val response =
            httpClient.get("${config.apiBaseUrl}/slots/$id/traffic-status?direction=$direction") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
            }
        if (response.status.value == 401 && sessionToken != null)
            throw AuthenticationRequiredException()
        return response.readControlJson<TransferTrafficStatus>(4096).also { it.policyException() }
    }

    @Throws(Exception::class)
    suspend fun create(): DropSlot =
        throw IllegalArgumentException("A receive public key is required")

    @Throws(Exception::class)
    suspend fun create(recipientPublicKey: String, maxFiles: Int): DropSlot =
        create(recipientPublicKey, maxFiles, null)

    @Throws(Exception::class)
    suspend fun create(recipientPublicKey: String, maxFiles: Int, title: String?): DropSlot {
        require(maxFiles >= 0 && recipientPublicKey.matches(Regex("[A-Za-z0-9_-]{43}"))) {
            "Invalid receive link settings"
        }
        val response =
            httpClient.post("${config.apiBaseUrl}/slots") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
                contentType(ContentType.Application.Json)
                setBody(
                    CreateReceiveRequest(
                        recipient_public_key = recipientPublicKey,
                        max_files = maxFiles,
                        title = LinkTitle.normalize(title),
                    )
                )
            }
        response.checkAuthenticatedWrite()
        return response.readControlJson<DropSlot>(4096).also {
            require(LinkTitle.normalize(it.title) == it.title)
        }
    }

    @Throws(Exception::class)
    suspend fun renameTitle(id: String, title: String?): LinkTitle {
        require(UrlHelper.isResourceId(id))
        val response =
            httpClient.patch("${config.apiBaseUrl}/slots/$id/title") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
                contentType(ContentType.Application.Json)
                setBody(LinkTitle(LinkTitle.normalize(title)))
            }
        response.checkAuthenticatedWrite()
        return response.readControlJson<LinkTitle>(4096).also {
            require(LinkTitle.normalize(it.title) == it.title)
        }
    }

    /** Public submission policy excludes child identities and activity. Never sends a token. */
    @Throws(Exception::class)
    suspend fun availability(slotId: String): SlotAvailability {
        require(UrlHelper.isResourceId(slotId))
        return httpClient
            .get("${config.apiBaseUrl}/slots/$slotId/availability") { expectSuccess = false }
            .readControlJson<SlotAvailability>(4096)
            .also {
                require(LinkTitle.normalize(it.title) == it.title)
                require(it.id == slotId) { "Receive link identity mismatch" }
                it.uploadCapacity?.validate()
            }
    }

    /**
     * Public guest creation. Never attach an account token; the result carries a child-only
     * capability.
     */
    @Throws(Exception::class)
    suspend fun createTransfer(slotId: String): Transfer {
        require(UrlHelper.isResourceId(slotId)) { "Invalid receive link identifier" }
        val response =
            httpClient.post("${config.apiBaseUrl}/slots/$slotId/transfers") {
                expectSuccess = false
                contentType(ContentType.Application.Json)
                setBody(mapOf<String, String>())
            }
        response.checkAccountRestriction()
        require(response.status.value == 201) {
            "The receive link is unavailable or cannot accept more files"
        }
        if (response.status.value == 401 && sessionToken != null)
            throw AuthenticationRequiredException()
        return response.readControlJson<Transfer>(4096).also {
            require(
                UrlHelper.isResourceId(it.id) &&
                    it.deleteToken?.matches(Regex("[A-Za-z0-9_-]{32,128}")) == true
            ) {
                "The server returned an invalid upload capability"
            }
        }
    }

    /** Revoke the link and stored uploads. Missing resources are already revoked. */
    @Throws(Exception::class)
    suspend fun delete(slotId: String, deleteToken: String? = null) {
        deleteLink(httpClient, "${config.apiBaseUrl}/slots/$slotId", deleteToken ?: sessionToken)
    }

    /** Fetch only the first bounded inbox page. No implicit traversal or legacy fallback. */
    @Throws(Exception::class) suspend fun get(slotId: String): DropSlot = getPage(slotId, null, 50)

    @Throws(Exception::class)
    suspend fun getPage(slotId: String, after: String?, limit: Int): DropSlot =
        withTimeout(10_000L) {
            require(UrlHelper.isResourceId(slotId) && limit in 1..100 && validInboxCursor(after))
            val response =
                httpClient.get("${config.apiBaseUrl}/slots/$slotId/inbox") {
                    expectSuccess = false
                    parameter("limit", limit)
                    after?.let { parameter("after", it) }
                    sessionToken?.let { bearerAuth(it) }
                }
            if (response.status.value == 401 && sessionToken != null)
                throw AuthenticationRequiredException()
            decodeInboxPage(response.readControlJson<JsonObject>(), slotId, after, limit).also {
                require(LinkTitle.normalize(it.title) == it.title)
            }
        }

    /**
     * Subscribe to real-time upload notifications via Server-Sent Events. Returns a Flow that emits
     * SlotEvents as they arrive.
     *
     * @param slotId the drop slot ID
     * @return a Flow of SSE events
     */
    fun events(slotId: String): Flow<SlotEvent> = flow {
        httpClient
            .prepareGet("${config.apiBaseUrl}/slots/$slotId/events") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
            }
            .execute { response ->
                response.checkAccountRestriction()
                if (response.status.value == 401 && sessionToken != null)
                    throw AuthenticationRequiredException()
                require(response.status.value == 200) { "Inbox notifications are unavailable" }
                val channel = response.bodyAsChannel()
                var currentEvent = ""
                var currentData = StringBuilder()
                var eventChars = 0

                try {
                    while (!channel.isClosedForRead) {
                        val line = channel.readUTF8Line(MAX_EVENT_LINE_CHARS) ?: break
                        require(line.length < MAX_EVENT_CHARS - eventChars) {
                            "Inbox notification is too large"
                        }
                        eventChars += line.length + 1

                        when {
                            line.startsWith("event:") -> {
                                currentEvent = line.removePrefix("event:").trim()
                            }
                            line.startsWith("data:") -> {
                                if (currentData.isNotEmpty()) currentData.append('\n')
                                currentData.append(line.removePrefix("data:").trim())
                            }
                            line.isBlank() -> {
                                // Empty line signals end of an event and resets its whole budget.
                                if (currentEvent.isNotEmpty() || currentData.isNotEmpty()) {
                                    emit(
                                        SlotEvent(
                                            event = currentEvent.ifEmpty { "message" },
                                            data = currentData.toString(),
                                        )
                                    )
                                    currentEvent = ""
                                    currentData = StringBuilder()
                                }
                                eventChars = 0
                            }
                        }
                    }
                } finally {
                    channel.cancel()
                }
            }
    }
}
