package zip.psst.android.data

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
        require(files.map { it.first }.toSet() == manifestFingerprints.keys)
        val remaining = files.filterNot { (child, file) -> "$child/${file.blobId}" in savedIds }
        var bytes = 0L
        for ((_, file) in remaining) {
            require(file.size >= 0 && file.size <= GuestDownloadPreflight.MAX_TOTAL_BYTES - bytes) {
                "This inbox exceeds the supported download total of 1 TiB"
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
