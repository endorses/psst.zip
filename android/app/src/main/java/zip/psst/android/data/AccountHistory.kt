package zip.psst.android.data

import zip.psst.shared.api.AuthResources
import kotlinx.coroutines.CancellationException

/**
 * Merge server facts under Room's transaction without replacing local keys, titles or save
 * checkpoints.
 */
internal suspend fun syncAccountHistory(
    dao: TransferHistoryDao,
    resources: AuthResources,
    access: HistoryAccess,
    currentAccess: () -> HistoryAccess,
) {
    require(!access.accountId.isNullOrBlank())
    for (transfer in resources.transfers) {
        if (currentAccess() != access) throw CancellationException("Account changed")
        dao.mergeAccountResource(
            TransferHistoryEntity(
                id = transfer.id,
                type = "sent",
                fileCount = transfer.fileCount,
                totalSize =
                    0, // Server sizes are encrypted bytes, never present them as plaintext sizes.
                serverUrl = access.serverUrl,
                encryptionKey = "",
                status =
                    when {
                        transfer.status in listOf("expired", "revoked") -> "unavailable"
                        transfer.downloadedAt != null -> "downloaded"
                        transfer.downloadCount > 0 -> "download_started"
                        else -> transfer.status
                    },
                accountId = access.accountId,
                createdAt = parseHistoryExpiry(transfer.createdAt) ?: System.currentTimeMillis(),
                expiresAt = parseHistoryExpiry(transfer.expiresAt),
            ),
            access,
        )
    }
    for (slot in resources.slots) {
        if (currentAccess() != access) throw CancellationException("Account changed")
        val children = slot.transfers.filter { it.status == "complete" }
        val snapshot =
            ReceivedSnapshot(
                children.associate { it.transferId to ReceivedChild(it.fileCount) },
                parseHistoryExpiry(slot.expiresAt),
            )
        dao.mergeAccountResource(
            TransferHistoryEntity(
                id = slot.id,
                type = "received",
                fileCount = children.sumOf { it.fileCount },
                totalSize = 0,
                serverUrl = access.serverUrl,
                encryptionKey = "",
                status =
                    if (slot.status in listOf("revoked", "expired")) "unavailable" else slot.status,
                accountId = access.accountId,
                createdAt = parseHistoryExpiry(slot.createdAt) ?: System.currentTimeMillis(),
                expiresAt = parseHistoryExpiry(slot.expiresAt),
            ),
            access,
            snapshot,
        )
    }
}

internal fun mergeAccountResource(
    current: TransferHistoryEntity?,
    incoming: TransferHistoryEntity,
    access: HistoryAccess,
    snapshot: ReceivedSnapshot? = null,
): TransferHistoryEntity? {
    require(incoming.accountId == access.accountId && access.permits(incoming))
    // An ID collision must never adopt a legacy row or another account's key/capability.
    if (current != null && (current.accountId != access.accountId || !access.permits(current)))
        return null
    var updated =
        current?.copy(
            fileCount = maxOf(current.fileCount, incoming.fileCount),
            expiresAt = incoming.expiresAt ?: current.expiresAt,
            status =
                if (current.status == "failed" && incoming.status == "pending") "failed"
                else incoming.status,
        ) ?: incoming
    if (snapshot != null && incoming.status != "unavailable")
        updated = mergeReceivedHistory(updated, snapshot)
    return updated
}

/**
 * One shared budget covers manifest enrichment and verification of IDs absent from a successful
 * listing.
 */
internal fun historyRefreshBatch(
    rows: List<TransferHistoryEntity>,
    offset: Int,
    remoteIds: Set<String>,
    limit: Int = 20,
): List<TransferHistoryEntity> {
    val eligible =
        rows.filter {
            it.status != "unavailable" &&
                (it.id !in remoteIds ||
                    (it.type in listOf("receive", "received") && it.encryptionKey.isNotBlank()))
        }
    if (eligible.isEmpty()) return emptyList()
    return List(minOf(limit, eligible.size)) { eligible[(offset + it) % eligible.size] }
}

internal fun historyManifestBatch(
    rows: List<TransferHistoryEntity>,
    offset: Int,
    limit: Int = 20,
): List<TransferHistoryEntity> =
    historyRefreshBatch(rows, offset, rows.map { it.id }.toSet(), limit)
