package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.Manifest
import zip.psst.shared.model.ManifestValidator

data class GuestDownloadConsent(
    val origin: String,
    val transferId: String,
    val files: List<FileMetadata>,
    val totalBytes: Long,
    val remainingBytes: Long,
    val redownloadMissing: Boolean,
    val skippedBlobIds: Set<String> = emptySet(),
)

internal class InsufficientDownloadSpaceException :
    UiFailureException(
        message(R.string.l_not_enough_storage_free_space_for_the_remaining_files_plus_256_mi_d719e6)
    )

internal object GuestDownloadPreflight {
    const val AUTO_DOWNLOAD_BYTES = 100L * 1024 * 1024
    const val FREE_SPACE_RESERVE = 256L * 1024 * 1024
    const val MAX_TOTAL_BYTES = 1024L * 1024 * 1024 * 1024

    fun inspect(
        origin: String,
        transferId: String,
        files: List<FileMetadata>,
        savedIds: Set<String>,
        redownloadMissing: Boolean,
        exhaustedBlobIds: Set<String> = emptySet(),
    ): GuestDownloadConsent {
        val total = ManifestValidator.validate(Manifest(files = files))
        uiRequire(total <= MAX_TOTAL_BYTES) {
            message(R.string.l_this_transfer_exceeds_the_supported_total_size_of_1_tib_1eea29)
        }
        val skipped =
            files
                .filter { it.blobId.lowercase() in exhaustedBlobIds && it.blobId !in savedIds }
                .map { it.blobId }
                .toSet()
        val remaining =
            files.filterNot { it.blobId in savedIds || it.blobId in skipped }.sumOf { it.size }
        return GuestDownloadConsent(
            origin,
            transferId,
            files.toList(),
            total,
            remaining,
            redownloadMissing,
            skipped,
        )
    }

    fun needsConsent(current: GuestDownloadConsent, approved: GuestDownloadConsent?): Boolean =
        (current.totalBytes > AUTO_DOWNLOAD_BYTES || current.skippedBlobIds.isNotEmpty()) &&
            current != approved

    /**
     * Existing saved files/unfinished external writes already consume the measured free bytes.
     * Native downloads stream into their destination and publish in place: no second disk copy,
     * ciphertext spool or ZIP. Retries discard their partial output before this check.
     */
    fun requireSpace(availableBytes: Long, remainingBytes: Long) {
        uiRequire(remainingBytes >= 0)
        if (
            availableBytes < FREE_SPACE_RESERVE ||
                remainingBytes > availableBytes - FREE_SPACE_RESERVE
        ) {
            throw InsufficientDownloadSpaceException()
        }
    }
}
