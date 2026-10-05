package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.FileMetadata
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/** Authentication, size checks, and every write must succeed before the durable save and ack. */
internal suspend fun receiveAndSaveChild(
    client: ApiClient,
    transferId: String,
    files: List<FileMetadata>,
    key: ByteArray,
    saveFile: suspend (FileMetadata, FileContent) -> Unit,
    recordSaved: suspend (ReceivedChild) -> Unit,
    onFileSaved: () -> Unit = {},
    alreadySaved: Set<String> = emptySet(),
    recordFileSaved: suspend (String) -> Unit = {},
) {
    require(files.isNotEmpty()) { "Transfer contains no files" }
    var savedBytes = 0L
    for (file in files) {
        if (file.blobId in alreadySaved) {
            savedBytes += file.size
            onFileSaved()
            continue
        }
        saveFile(file, downloadedContent(client, transferId, file, key))
        withContext(NonCancellable) { recordFileSaved(file.blobId) }
        savedBytes += file.size
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
            HistoryNotifications.changed(client.config.normalizedBaseUrl)
            true
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            false
        }
    } ?: false

/** Only indexed saved children from the visible page enter this bounded idempotent retry. */
internal suspend fun retrySavedDownloadAcknowledgements(savedIds: Set<String>, client: ApiClient) {
    require(savedIds.size <= 100)
    withTimeoutOrNull(2_500L) { for (id in savedIds) acknowledgeSavedDownload(client, id) }
}
