package zip.psst.shared.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
class TransferTrafficStatus(
    val state: String,
    @SerialName("retry_at") val retryAt: String? = null,
) {
    @Throws(Exception::class)
    fun policyException(): TransferPolicyException? =
        when (state) {
            "ready" -> null
            "exhausted" -> TrafficBudgetExhaustedException(retryAt)
            "unavailable" -> TrafficAccountingUnavailableException()
            "paused" -> PublicTransfersPausedException()
            "revoked" -> ResourceRevokedException()
            else -> throw IllegalArgumentException("Unsupported transfer traffic status")
        }
}

/** Remote EOF before the manifest's authenticated wire size, not a decryption failure. */
class TransferDownloadInterruptedException :
    IllegalArgumentException("The file download was interrupted")

/** Only recognizable transport failures qualify; crypto, local IO and cancellation do not. */
object TrafficFailureClassifier {
    fun shouldProbe(error: Throwable): Boolean {
        if (error is kotlinx.coroutines.CancellationException || error is TransferPolicyException)
            return false
        return error is TransferDownloadInterruptedException ||
            error is io.ktor.client.plugins.ResponseException ||
            error is io.ktor.client.plugins.HttpRequestTimeoutException ||
            error is io.ktor.client.network.sockets.ConnectTimeoutException ||
            error is io.ktor.client.network.sockets.SocketTimeoutException ||
            error is io.ktor.utils.io.ClosedReadChannelException
    }
}
