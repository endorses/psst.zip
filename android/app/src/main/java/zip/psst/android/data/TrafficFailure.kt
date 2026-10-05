package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.api.TransferPolicyException
import zip.psst.shared.api.TransferTrafficStatus
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull

internal fun receiveCapacityError(error: Exception): UiText =
    when (error) {
        is TransferPolicyException ->
            failureText(error) ?: message(R.string.l_this_receive_link_is_unavailable_b567b3)
        is io.ktor.client.plugins.ResponseException ->
            message(
                R.string.l_receive_capacity_could_not_be_checked_http_1_s_refresh_and_try_ag_912538,
                (error.response.status.value),
            )
        is IllegalArgumentException ->
            failureText(error) ?: message(R.string.l_this_receive_link_could_not_be_checked_dd190b)
        else ->
            message(
                R.string.l_receive_capacity_could_not_be_checked_check_your_connection_and_r_298b9c
            )
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
