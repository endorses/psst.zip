package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.model.FileMetadata
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull

/** Authentication, size checks, and every write must succeed before the durable save and ack. */
internal suspend fun receiveAndSaveChild(
    client: ApiClient,
    transferId: String,
    files: List<FileMetadata>,
    key: ByteArray,
    saveFile: suspend (FileMetadata, ByteArray) -> Unit,
    recordSaved: suspend (ReceivedChild) -> Unit,
    onFileSaved: () -> Unit = {},
) {
    require(files.isNotEmpty()) { "Transfer contains no files" }
    var savedBytes = 0L
    for (file in files) {
        val encrypted = client.transfers.downloadFile(transferId, file.blobId)
        require(encrypted.size >= 28) { "Encrypted file is incomplete" }
        val plaintext =
            CryptoProvider.decrypt(
                key,
                encrypted.copyOfRange(0, 12),
                encrypted.copyOfRange(12, encrypted.size),
            )
        require(plaintext.size.toLong() == file.size) { "Manifest file size mismatch" }
        saveFile(file, plaintext)
        savedBytes += plaintext.size
        onFileSaved()
    }
    // Persist first: acknowledgement failure/cancellation must never require another file download.
    recordSaved(ReceivedChild(files.size, savedBytes))
    acknowledgeSavedDownload(client, transferId)
}

internal suspend fun acknowledgeSavedDownload(client: ApiClient, transferId: String): Boolean =
    withTimeoutOrNull(1_500L) {
        try {
            client.transfers.acknowledgeDownload(transferId)
            true
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            false
        }
    } ?: false

/**
 * Saved IDs are the durable retry queue; the endpoint is idempotent, so no new schema is needed.
 */
internal suspend fun retrySavedDownloadAcknowledgements(
    row: TransferHistoryEntity,
    client: ApiClient,
) {
    withTimeoutOrNull(2_500L) {
        for (id in row.savedTransferIds()) acknowledgeSavedDownload(client, id)
    }
}
