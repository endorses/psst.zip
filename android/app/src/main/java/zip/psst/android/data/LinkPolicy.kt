package zip.psst.android.data

import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.Transfer

internal class UnsupportedLinkPolicyException :
    IllegalArgumentException(
        "This server does not support the requested link protection. Ask its administrator to update the server. No link was shared."
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
