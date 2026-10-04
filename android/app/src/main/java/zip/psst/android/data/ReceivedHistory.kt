package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.Transfer
import zip.psst.shared.model.TransferStatus
import java.time.Instant
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

@Serializable data class ReceivedChild(val fileCount: Int, val plaintextSize: Long? = null)

/** Only completed, immutable children belong in a history snapshot. */
data class ReceivedSnapshot(val children: Map<String, ReceivedChild>, val expiresAt: Long? = null)

internal fun DropSlot.receivedSnapshot() =
    ReceivedSnapshot(
        completedTransfers.associate { it.transferId to ReceivedChild(it.fileCount) },
        parseHistoryExpiry(expiresAt),
    )

internal fun TransferHistoryEntity.savedTransferIds(): Set<String> =
    Json.decodeFromString(savedTransferIdsJson)

internal fun TransferHistoryEntity.savedFileCount(): Int {
    val perFile = Json.decodeFromString<Set<String>>(savedFileIdsJson).size
    val children = Json.decodeFromString<Map<String, ReceivedChild>>(receivedTransfersJson)
    return maxOf(perFile, savedTransferIds().sumOf { children[it]?.fileCount ?: 0 })
}

/** Merge under a Room transaction so delayed polls cannot undo a successful save. */
internal fun mergeReceivedHistory(
    current: TransferHistoryEntity,
    snapshot: ReceivedSnapshot,
    saved: Boolean = false,
): TransferHistoryEntity {
    val known = Json.decodeFromString<Map<String, ReceivedChild>>(current.receivedTransfersJson)
    val children = known.toMutableMap()
    for ((id, child) in snapshot.children) {
        val previous = children[id]
        children[id] =
            ReceivedChild(
                previous?.fileCount ?: child.fileCount,
                child.plaintextSize ?: previous?.plaintextSize,
            )
    }
    val savedIds = current.savedTransferIds() + if (saved) snapshot.children.keys else emptySet()
    val knownCount = children.values.sumOf { it.fileCount }
    val count = maxOf(current.fileCount, knownCount)
    val status =
        when {
            children.isEmpty() -> current.status
            knownCount == count && savedIds.containsAll(children.keys) -> "complete"
            children.isNotEmpty() || count > 0 -> "has_uploads"
            else -> current.status
        }
    return current.copy(
        fileCount = count,
        totalSize = maxOf(current.totalSize, children.values.sumOf { it.plaintextSize ?: 0 }),
        status = status,
        receivedTransfersJson = Json.encodeToString(children.toMap()),
        savedTransferIdsJson = Json.encodeToString(savedIds),
        expiresAt = snapshot.expiresAt ?: current.expiresAt,
    )
}

internal fun mergeSentHistory(
    current: TransferHistoryEntity,
    transfer: Transfer,
): TransferHistoryEntity =
    current.copy(
        fileCount = maxOf(current.fileCount, transfer.fileCount),
        expiresAt = parseHistoryExpiry(transfer.expiresAt) ?: current.expiresAt,
        status = sentHistoryStatus(transfer, current.status),
    )

internal fun sentHistoryStatus(transfer: Transfer, previousStatus: String? = null): String =
    when {
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
    privateReceiveKey: (TransferHistoryEntity) -> ByteArray? = { null },
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
                    dao.mergeSent(row.id, transfer)
                    onTransfer(transfer)
                } else {
                    retrySavedDownloadAcknowledgements(row, client)
                    val slot = client.slots.get(row.id)
                    dao.mergeReceived(row.id, slot.receivedSnapshot())
                    if (slot.completedTransfers.isNotEmpty() && row.encryptionKey.isNotBlank()) {
                        val privateKey = privateReceiveKey(row)
                        if (row.encryptionKey.startsWith("v2.") && privateKey == null)
                            return@withTimeout
                        for (transfer in slot.completedTransfers) {
                            val manifest =
                                decryptInboxManifest(
                                        row,
                                        transfer.transferId,
                                        client.transfers.downloadManifest(transfer.transferId),
                                        privateKey,
                                    )
                                    .manifest
                            require(manifest.files.size == transfer.fileCount) {
                                "Manifest file count mismatch"
                            }
                            var size = 0L
                            for (file in manifest.files) {
                                require(file.size >= 0 && file.size <= Long.MAX_VALUE - size) {
                                    "Invalid file size"
                                }
                                size += file.size
                            }
                            manifest.files.firstOrNull()?.name?.let {
                                dao.setTitleIfEmpty(row.id, it)
                            }
                            dao.mergeReceived(
                                row.id,
                                ReceivedSnapshot(
                                    mapOf(
                                        transfer.transferId to
                                            ReceivedChild(transfer.fileCount, size)
                                    )
                                ),
                            )
                        }
                    }
                }
            } catch (e: CancellationException) {
                throw e
            } catch (error: Exception) {
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

internal fun historyStatusLabel(type: String, status: String): String =
    when (status) {
        "complete" -> if (type == "received" || type == "receive") "Saved" else "Ready to download"
        "downloaded" -> "Downloaded"
        "download_started" -> "Download started"
        "has_uploads" -> "Uploads received"
        "waiting" -> "Waiting for files"
        "pending" -> "In progress"
        "failed" -> "Upload failed"
        "expired" -> "Expired"
        "unavailable" -> "Expired or revoked"
        else -> "Unknown"
    }

internal fun parseHistoryExpiry(value: String?): Long? =
    try {
        value?.let { Instant.parse(it).toEpochMilli() }
    } catch (_: Exception) {
        null
    }
