package zip.psst.android.data

import zip.psst.android.i18n.ScanStage
import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.FileMetadata
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext

/** Decrypted data is authenticated and length checked before the first filesystem write. */
internal suspend fun receiveGuestFiles(
    client: ApiClient,
    transferId: String,
    files: List<FileMetadata>,
    key: ByteArray,
    alreadySaved: Set<String>,
    onStage: (ScanStage, Int, FileMetadata) -> Unit,
    onProgress: (Long, Long?) -> Unit,
    saveFile: suspend (FileMetadata, FileContent) -> SavedGuestFile,
    checkpoint: (SavedGuestFile) -> Unit,
) {
    for ((index, file) in files.withIndex()) {
        if (file.blobId in alreadySaved) continue
        onStage(ScanStage.DOWNLOADING, index + 1, file)
        val output = saveFile(file, downloadedContent(client, transferId, file, key, onProgress))
        withContext(NonCancellable) { checkpoint(output) }
    }
}

/**
 * Recover a published journal entry without another blob request; discard only incomplete writes.
 */
internal fun reconcileGuestOutput(
    record: GuestDownload,
    published: (SavedGuestFile) -> Boolean,
    discard: (SavedGuestFile) -> Unit,
    persist: (GuestDownload) -> Unit,
): GuestDownload {
    val pending = record.pending ?: return record
    val recovered =
        if (published(pending))
            record.copy(
                saved = record.saved.filterNot { it.blobId == pending.blobId } + pending,
                pending = null,
            )
        else {
            discard(pending)
            record.copy(pending = null)
        }
    val complete =
        recovered.files.isNotEmpty() &&
            recovered.files.all { file -> recovered.saved.any { it.blobId == file.blobId } }
    return recovered
        .copy(complete = complete, receiptPending = recovered.receiptPending || complete)
        .also(persist)
}

/** A completed upload is never cleanup material, including a crash just after finalization. */
internal suspend fun cleanupGuestUpload(
    client: ApiClient,
    transferId: String,
    token: String,
): Boolean {
    try {
        val transfer = client.transfers.uploadStatus(transferId)
        require(transfer.id == transferId)
        if (transfer.status == zip.psst.shared.model.TransferStatus.COMPLETE) return true
        client.transfers.delete(transferId, token)
    } catch (_: zip.psst.shared.api.ResourceRevokedException) {
        // Revoked upload capabilities cannot accept further content.
    } catch (e: io.ktor.client.plugins.ClientRequestException) {
        if (e.response.status.value !in listOf(404, 410)) throw e
    }
    return false
}

internal data class GuestUploadResolution(val completed: Boolean, val journalCleared: Boolean)

/** The server's completed outcome is independent of local cleanup journal housekeeping. */
internal suspend fun resolveGuestUpload(
    client: ApiClient,
    transferId: String,
    token: String,
    knownCompleted: Boolean,
    finishJournal: () -> Unit,
): GuestUploadResolution {
    val completed = knownCompleted || cleanupGuestUpload(client, transferId, token)
    val cleared =
        try {
            finishJournal()
            true
        } catch (_: Exception) {
            false
        }
    return GuestUploadResolution(completed, cleared)
}

/** No-replace publication keeps existing files intact, including a concurrent name collision. */
internal fun publishGuestFile(
    temporary: java.io.File,
    directory: java.io.File,
    name: String,
    journal: (java.io.File) -> Unit,
): java.io.File {
    val dot = name.lastIndexOf('.').takeIf { it > 0 } ?: name.length
    var counter = 1
    while (true) {
        val candidate =
            java.io.File(
                directory,
                if (counter == 1) name else name.take(dot) + " ($counter)" + name.substring(dot),
            )
        if (!candidate.exists()) {
            journal(candidate)
            try {
                java.nio.file.Files.move(temporary.toPath(), candidate.toPath())
                return candidate
            } catch (_: java.nio.file.FileAlreadyExistsException) {
                /* Another writer won; choose the next name. */
            }
        }
        counter++
        check(counter <= 10000) { "Too many files with this name" }
    }
}
