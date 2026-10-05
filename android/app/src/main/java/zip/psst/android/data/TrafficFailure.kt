package zip.psst.android.data

import zip.psst.shared.api.TransferPolicyException
import zip.psst.shared.api.TransferTrafficStatus
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull

internal fun receiveCapacityError(error: Exception): String =
    when (error) {
        is TransferPolicyException -> error.message ?: "This receive link is unavailable"
        is io.ktor.client.plugins.ResponseException ->
            "Receive capacity could not be checked (HTTP ${error.response.status.value}). Refresh and try again; your files are still selected."
        is IllegalArgumentException -> error.message ?: "This receive link could not be checked"
        else ->
            "Receive capacity could not be checked. Check your connection and refresh; your files are still selected."
    }

/** One metadata request after remote IO interruption, never a payload retry. */
internal suspend fun classifyTrafficFailure(
    error: Exception,
    probe: suspend () -> TransferTrafficStatus,
): TransferPolicyException? {
    if (error is TransferPolicyException || error is CancellationException) return null
    val remote =
        zip.psst.shared.api.TrafficFailureClassifier.shouldProbe(error) ||
            error is java.net.SocketException ||
            error is java.net.SocketTimeoutException ||
            error is java.net.UnknownHostException
    if (!remote) return null
    return try {
        withTimeoutOrNull(3000) { probe().policyException() }
    } catch (e: CancellationException) {
        throw e
    } catch (e: TransferPolicyException) {
        e
    } catch (_: Exception) {
        null
    }
}
