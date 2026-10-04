package zip.psst.shared.api

import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.get
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
class ServerLimits(
    @SerialName("max_file_size") val maxFileSize: Long,
    @SerialName("max_file_size_ceiling") val maxFileSizeCeiling: Long,
    @SerialName("traffic_policy") val trafficPolicy: TrafficPolicy? = null,
    @SerialName("abuse_contact_email") val abuseContactEmail: String = "",
)

class LimitsApi(private val client: HttpClient, private val config: ServerConfig) {
    /** Public policy for this exact origin. Failure never silently enables larger uploads. */
    @Throws(Exception::class)
    suspend fun get(): ServerLimits {
        val response = client.get("${config.apiBaseUrl}/config") { expectSuccess = false }
        require(response.status.value == 200) { "Could not read the server's file limit" }
        val limits = response.readControlJson<ServerLimits>(maxBytes = 64 * 1024)
        require(
            limits.maxFileSize in 1..limits.maxFileSizeCeiling &&
                limits.maxFileSizeCeiling <= ChunkedFileCrypto.MAX_FILE_SIZE
        ) {
            "Invalid server file limit"
        }
        limits.trafficPolicy?.validate()
        return limits
    }
}
