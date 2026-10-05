package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.Transfer

internal class UnsupportedLinkPolicyException :
    UiFailureException(
        message(R.string.l_this_server_does_not_support_the_requested_link_protection_ask_it_45bd6a)
    )

internal fun verifyReceivePolicy(slot: DropSlot, publicKey: String, maxFiles: Int) {
    if (
        slot.receiveProtocol != 2 ||
            slot.recipientPublicKey != publicKey ||
            slot.maxFiles != maxFiles
    )
        throw UnsupportedLinkPolicyException()
}

internal fun verifyDownloadPolicy(transfer: Transfer, maxDownloads: Int) {
    if (transfer.maxDownloads != maxDownloads) throw UnsupportedLinkPolicyException()
}

/** Disabled means unlimited; an enabled limit must be an explicit positive integer. */
internal fun selectedLinkLimit(enabled: Boolean, value: String): Int {
    if (!enabled) return 0
    uiRequire(value.isNotBlank() && value.length <= 10) {
        message(R.string.l_enter_a_limit_between_1_and_2147483647_179608)
    }
    return optionalLinkLimit(value)
}

internal fun linkLimitError(enabled: Boolean, value: String): UiText? =
    try {
        selectedLinkLimit(enabled, value)
        null
    } catch (error: IllegalArgumentException) {
        failureText(error)
    }
