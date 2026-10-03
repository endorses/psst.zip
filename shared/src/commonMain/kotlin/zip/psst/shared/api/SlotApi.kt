package zip.psst.shared.api

import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.Transfer
import zip.psst.shared.model.UrlHelper
import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.bearerAuth
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.prepareGet
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsChannel
import io.ktor.http.ContentType
import io.ktor.http.contentType
import io.ktor.utils.io.readUTF8Line
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow

/** Represents a Server-Sent Event from the slot events endpoint. */
data class SlotEvent(val event: String, val data: String)

/** API operations for drop slots (receive flow). */
class SlotApi(
    private val httpClient: HttpClient,
    private val config: ServerConfig,
    private val sessionToken: String? = null,
) {
    /** Create a new drop slot. Returns the slot with its server-assigned ID. */
    @Throws(Exception::class)
    suspend fun create(): DropSlot {
        val response =
            httpClient.post("${config.apiBaseUrl}/slots") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
                contentType(ContentType.Application.Json)
            }
        response.checkAuthenticatedWrite()
        return response.body()
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
        require(response.status.value == 201) {
            "The receive link is unavailable or cannot accept more files"
        }
        return response.body<Transfer>().also {
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

    /** Get drop slot status including list of uploaded files. */
    @Throws(Exception::class)
    suspend fun get(slotId: String): DropSlot {
        val response = httpClient.get("${config.apiBaseUrl}/slots/$slotId")
        return response.body()
    }

    /**
     * Subscribe to real-time upload notifications via Server-Sent Events. Returns a Flow that emits
     * SlotEvents as they arrive.
     *
     * @param slotId the drop slot ID
     * @return a Flow of SSE events
     */
    fun events(slotId: String): Flow<SlotEvent> = flow {
        httpClient.prepareGet("${config.apiBaseUrl}/slots/$slotId/events").execute { response ->
            val channel = response.bodyAsChannel()
            var currentEvent = ""
            var currentData = StringBuilder()

            while (!channel.isClosedForRead) {
                val line = channel.readUTF8Line() ?: break

                when {
                    line.startsWith("event:") -> {
                        currentEvent = line.removePrefix("event:").trim()
                    }
                    line.startsWith("data:") -> {
                        if (currentData.isNotEmpty()) currentData.append('\n')
                        currentData.append(line.removePrefix("data:").trim())
                    }
                    line.isBlank() -> {
                        // Empty line signals end of an event
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
                    }
                }
            }
        }
    }
}
