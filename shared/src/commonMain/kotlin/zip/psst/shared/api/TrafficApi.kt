package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.http.HttpHeaders
import kotlin.time.Instant
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class TrafficPolicy(
    @SerialName("enforcement_enabled") val enforcementEnabled: Boolean,
    @SerialName("server_budget_bytes") val serverBudgetBytes: Long,
    @SerialName("default_account_budget_bytes") val defaultAccountBudgetBytes: Long,
    val basis: String,
    @SerialName("cycle_start_day") val cycleStartDay: Int,
    @SerialName("upload_bytes_per_second") val uploadBytesPerSecond: Long,
    @SerialName("download_bytes_per_second") val downloadBytesPerSecond: Long,
    @SerialName("max_active_streams") val maxActiveStreams: Int,
    @SerialName("max_streams_per_account") val maxStreamsPerAccount: Int,
    @SerialName("max_streams_per_ip") val maxStreamsPerIp: Int,
    @SerialName("max_streams_per_transfer") val maxStreamsPerTransfer: Int,
    @SerialName("max_streams_per_slot") val maxStreamsPerSlot: Int,
) {
    fun validate() {
        require(
            serverBudgetBytes in 1..9007199254740991L &&
                defaultAccountBudgetBytes in 1..9007199254740991L &&
                basis in listOf("outbound", "combined") &&
                cycleStartDay in 1..31 &&
                uploadBytesPerSecond in 1..10737418240L &&
                downloadBytesPerSecond in 1..10737418240L &&
                listOf(
                        maxActiveStreams,
                        maxStreamsPerAccount,
                        maxStreamsPerIp,
                        maxStreamsPerTransfer,
                        maxStreamsPerSlot,
                    )
                    .all { it in 1..4096 }
        ) {
            "Invalid server traffic policy"
        }
    }
}

@Serializable
data class TrafficUsage(
    @SerialName("observed_uploaded_bytes") val observedUploadedBytes: Long,
    @SerialName("observed_downloaded_bytes") val observedDownloadedBytes: Long,
    @SerialName("reserved_uploaded_bytes") val reservedUploadedBytes: Long,
    @SerialName("reserved_downloaded_bytes") val reservedDownloadedBytes: Long,
    @SerialName("conservative_uploaded_bytes") val conservativeUploadedBytes: Long,
    @SerialName("conservative_downloaded_bytes") val conservativeDownloadedBytes: Long,
    @SerialName("charged_bytes") val chargedBytes: Long,
    @SerialName("budget_bytes") val budgetBytes: Long,
    @SerialName("remaining_bytes") val remainingBytes: Long,
)

@Serializable data class TrafficCycle(val start: String, val end: String)

@Serializable
data class TrafficSnapshot(
    val policy: TrafficPolicy,
    val usage: TrafficUsage,
    @SerialName("recording_started_at") val recordingStartedAt: String,
    val cycle: TrafficCycle,
    @SerialName("lease_bytes") val leaseBytes: Long,
    val state: String,
) {
    fun validate() {
        policy.validate()
        require(
            state in listOf("ready", "exhausted", "unavailable") &&
                leaseBytes in 1..1048576 &&
                usage.budgetBytes in 1..9007199254740991L &&
                usage.remainingBytes in 0..usage.budgetBytes &&
                listOf(
                        usage.observedUploadedBytes,
                        usage.observedDownloadedBytes,
                        usage.reservedUploadedBytes,
                        usage.reservedDownloadedBytes,
                        usage.conservativeUploadedBytes,
                        usage.conservativeDownloadedBytes,
                        usage.chargedBytes,
                    )
                    .all { it >= 0 }
        ) {
            "Invalid account traffic status"
        }
        val start = normalizedTrafficRetryAt(cycle.start)
        val end = normalizedTrafficRetryAt(cycle.end)
        require(
            start != null &&
                end != null &&
                normalizedTrafficRetryAt(recordingStartedAt) != null &&
                Instant.parse(start) < Instant.parse(end)
        ) {
            "Invalid traffic billing cycle"
        }
    }
}

class TrafficApi(
    private val client: HttpClient,
    private val config: ServerConfig,
    private val sessionToken: String?,
) {
    /** One bounded account-scoped snapshot. Never use cached usage as admission permission. */
    @Throws(Exception::class)
    suspend fun usage(): TrafficSnapshot {
        val response =
            client.get("${config.apiBaseUrl}/auth/traffic-usage") {
                expectSuccess = false
                sessionToken?.let { header(HttpHeaders.Authorization, "Bearer $it") }
            }
        if (response.status.value == 401) throw AuthenticationRequiredException()
        return response.readControlJson<TrafficSnapshot>(64 * 1024).also { it.validate() }
    }
}
