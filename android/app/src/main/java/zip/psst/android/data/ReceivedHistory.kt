package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.Transfer
import zip.psst.shared.model.TransferStatus
import java.time.Instant
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.Serializable

@Serializable data class ReceivedChild(val fileCount: Int, val plaintextSize: Long? = null)

/** Only completed, immutable children belong in a history snapshot. */
data class ReceivedSnapshot(
    val children: Map<String, ReceivedChild>,
    val expiresAt: Long? = null,
    val completedFiles: Long? = null,
    val partial: Boolean = false,
    val summaryObserved: Boolean = false,
    val maxFiles: Int? = null,
    val reservedFiles: Long? = null,
)

internal fun DropSlot.receivedSnapshot() =
    ReceivedSnapshot(
        completedTransfers.associate { it.transferId to ReceivedChild(it.fileCount) },
        parseHistoryExpiry(expiresAt),
        summary?.completedFiles,
        partial = paginated,
        summaryObserved = paginated,
        maxFiles = maxFiles,
        reservedFiles = reservedFiles,
    )

internal fun mergeSentHistory(
    current: TransferHistoryEntity,
    transfer: Transfer,
): TransferHistoryEntity =
    current.copy(
        fileCount = maxOf(current.fileCount, transfer.fileCount),
        summaryUpdating = false,
        expiresAt = parseHistoryExpiry(transfer.expiresAt) ?: current.expiresAt,
        status = sentHistoryStatus(transfer, current.status),
        maxDownloads = transfer.maxDownloads,
        sharedTitle = transfer.title,
    )

internal fun sentHistoryStatus(transfer: Transfer, previousStatus: String? = null): String =
    when {
        transfer.status == TransferStatus.EXHAUSTED -> "exhausted"
        previousStatus == "downloaded" || transfer.downloadedAt != null -> "downloaded"
        previousStatus == "download_started" || transfer.downloadCount > 0 -> "download_started"
        previousStatus == "failed" && transfer.status == TransferStatus.PENDING -> "failed"
        else -> transfer.status.name.lowercase()
    }

/** Refresh metadata and retry acknowledgements for files already saved on this device. */
internal suspend fun refreshHistoryEntry(
    dao: TransferHistoryDao,
    id: String,
    reportFailure: Boolean = false,
    contextCurrent: () -> Boolean = { true },
    onTransfer: (Transfer) -> Unit = {},
    createClient: (ServerConfig) -> ApiClient = { ApiClient(it) },
): TransferHistoryEntity? {
    val row = dao.getById(id) ?: return null
    try {
        withTimeout(5_000L) {
            val client = createClient(ServerConfig(row.serverUrl))
            try {
                if (row.type == "sent" || row.type == "send") {
                    val transfer = client.transfers.get(row.id)
                    if (!contextCurrent()) throw CancellationException("Account changed")
                    dao.mergeSent(row.id, transfer)
                    onTransfer(transfer)
                } else {
                    val slot = client.slots.get(row.id)
                    if (!contextCurrent()) throw CancellationException("Account changed")
                    val updated =
                        dao.mergeReceived(
                            row.id,
                            slot.receivedSnapshot(),
                            expectedScope = row.checkpointScope(),
                        ) ?: row
                    retrySavedDownloadAcknowledgements(
                        dao.savedChildren(updated, slot.completedTransfers.map { it.transferId }),
                        client,
                    )
                }
            } catch (e: CancellationException) {
                throw e
            } catch (error: Exception) {
                if (!contextCurrent()) throw CancellationException("Account changed")
                if (
                    error is zip.psst.shared.api.ResourceRevokedException ||
                        (error is io.ktor.client.plugins.ClientRequestException &&
                            error.response.status.value in listOf(404, 410))
                )
                    dao.updateStatus(id, "unavailable")
                else if (reportFailure) throw error
            } finally {
                client.close()
            }
        }
    } catch (error: kotlinx.coroutines.TimeoutCancellationException) {
        if (reportFailure) throw java.io.IOException("Status request timed out")
    }
    return dao.getById(id)
}

internal fun historyStatusLabel(type: String, status: String): UiText =
    when (status) {
        "complete" ->
            if (type == "received" || type == "receive") message(R.string.ui_received_status_saved)
            else message(R.string.ui_received_status_ready)
        "downloaded" -> message(R.string.l_downloaded_c61970)
        "download_started" -> message(R.string.ui_received_status_started)
        "has_uploads" -> message(R.string.ui_received_status_uploads)
        "waiting" -> message(R.string.waiting_files)
        "pending" -> message(R.string.ui_received_status_progress)
        "failed" -> message(R.string.ui_received_status_failed)
        "exhausted" -> message(R.string.l_download_limit_reached_8745b6)
        "expired" -> message(R.string.expired)
        "unavailable" -> message(R.string.ui_received_status_revoked)
        else -> message(R.string.ui_received_status_unknown)
    }

internal fun parseHistoryExpiry(value: String?): Long? =
    try {
        value?.let { Instant.parse(it).toEpochMilli() }
    } catch (_: Exception) {
        null
    }
