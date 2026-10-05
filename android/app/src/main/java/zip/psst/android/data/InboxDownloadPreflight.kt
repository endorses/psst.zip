package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.model.FileMetadata

/** Consent binds the owner, inbox, and every authenticated encrypted child manifest. */
data class InboxDownloadConsent(
    val origin: String,
    val accountId: String,
    val slotId: String,
    val remainingBytes: Long,
    val fileCount: Int,
    val manifestFingerprints: Map<String, String>,
)

internal object InboxDownloadPreflight {
    fun inspect(
        origin: String,
        accountId: String,
        slotId: String,
        files: List<Pair<String, FileMetadata>>,
        savedIds: Set<String>,
        manifestFingerprints: Map<String, String>,
    ): InboxDownloadConsent {
        uiRequire(files.map { it.first }.toSet() == manifestFingerprints.keys)
        val remaining = files.filterNot { (child, file) -> "$child/${file.blobId}" in savedIds }
        var bytes = 0L
        for ((_, file) in remaining) {
            uiRequire(
                file.size >= 0 && file.size <= GuestDownloadPreflight.MAX_TOTAL_BYTES - bytes
            ) {
                message(R.string.l_this_inbox_exceeds_the_supported_download_total_of_1_tib_57ed06)
            }
            bytes += file.size
        }
        return InboxDownloadConsent(
            origin,
            accountId,
            slotId,
            bytes,
            remaining.size,
            manifestFingerprints.toMap(),
        )
    }

    fun needsConsent(current: InboxDownloadConsent, approved: InboxDownloadConsent?): Boolean =
        current.remainingBytes > GuestDownloadPreflight.AUTO_DOWNLOAD_BYTES && current != approved
}
