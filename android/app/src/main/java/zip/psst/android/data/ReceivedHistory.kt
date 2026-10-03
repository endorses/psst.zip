package zip.psst.android.data

import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.EncryptedManifest
import zip.psst.shared.model.Manifest
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.Transfer
import java.time.Instant
import kotlin.io.encoding.Base64
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull
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
        status =
            if (current.status == "download_started" || transfer.downloadCount > 0)
                "download_started"
            else transfer.status.name.lowercase(),
    )

/** Refresh metadata only, using the origin and key saved with this history entry. */
internal suspend fun refreshHistoryEntry(
    dao: TransferHistoryDao,
    id: String,
    createClient: (ServerConfig) -> ApiClient = { ApiClient(it) },
): TransferHistoryEntity? {
    val row = dao.getById(id) ?: return null
    withTimeoutOrNull(5_000L) {
        val client = createClient(ServerConfig(row.serverUrl))
        try {
            if (row.type == "sent" || row.type == "send") {
                dao.mergeSent(row.id, client.transfers.get(row.id))
            } else {
                val slot = client.slots.get(row.id)
                dao.mergeReceived(row.id, slot.receivedSnapshot())
                if (slot.completedTransfers.isNotEmpty()) {
                    val key =
                        Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)
                            .decode(row.encryptionKey)
                    for (transfer in slot.completedTransfers) {
                        val encrypted =
                            EncryptedManifest.fromBytes(
                                client.transfers.downloadManifest(transfer.transferId)
                            )
                        val manifest =
                            Json.decodeFromString<Manifest>(
                                CryptoProvider.decrypt(key, encrypted.nonce, encrypted.ciphertext)
                                    .decodeToString()
                            )
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
                        dao.mergeReceived(
                            row.id,
                            ReceivedSnapshot(
                                mapOf(
                                    transfer.transferId to ReceivedChild(transfer.fileCount, size)
                                )
                            ),
                        )
                    }
                }
            }
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // Missing, expired, offline, or unreadable entries retain known local facts.
        } finally {
            client.close()
        }
    }
    return dao.getById(id)
}

internal fun historyStatusLabel(type: String, status: String): String =
    when (status) {
        "complete" -> if (type == "received" || type == "receive") "Saved" else "Ready to download"
        "download_started" -> "Download started"
        "has_uploads" -> "Uploads received"
        "waiting" -> "Waiting for files"
        "pending" -> "In progress"
        "expired" -> "Expired"
        else -> "Unknown"
    }

internal fun parseHistoryExpiry(value: String?): Long? =
    try {
        value?.let { Instant.parse(it).toEpochMilli() }
    } catch (_: Exception) {
        null
    }
