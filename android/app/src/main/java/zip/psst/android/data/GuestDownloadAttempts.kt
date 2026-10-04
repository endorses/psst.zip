package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.Manifest
import zip.psst.shared.model.ManifestValidator
import kotlinx.coroutines.withTimeout

/** Refresh only advisory metadata after attempts; never consume another file GET. */
internal suspend fun refreshGuestDownloadAttempts(
    record: GuestDownload,
    createClient: (String) -> ApiClient = ApiClient::anonymous,
): Map<String, Long?> {
    val client = createClient(record.origin)
    return try {
        withTimeout(5000) {
            val transfer = client.transfers.get(record.transferId)
            ManifestValidator.validateForTransfer(Manifest(record.files), transfer)
            if (transfer.maxDownloads == 0) emptyMap()
            else transfer.files.associate { it.id.lowercase() to it.remainingDownloads }
        }
    } catch (_: Exception) {
        // Offline/revoked metadata must not leave stale allowances displayed.
        record.files.associate { it.blobId.lowercase() to null }
    } finally {
        client.close()
    }
}
