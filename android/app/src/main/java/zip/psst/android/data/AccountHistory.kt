package zip.psst.android.data

import zip.psst.shared.api.AuthResourceSlot
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
                fileCount = (transfer.fileCount ?: 0).coerceAtMost(Int.MAX_VALUE.toLong()).toInt(),
                summaryUpdating = transfer.summary?.ready != true,
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
                maxDownloads = transfer.maxDownloads,
            ),
            access,
        )
    }
    for (slot in resources.slots) {
        if (currentAccess() != access) throw CancellationException("Account changed")
        dao.mergeAccountResource(slotHistoryResource(slot, access), access)
    }
}

/** Compact resource summaries contain authoritative counts, never an authoritative child list. */
internal fun slotHistoryResource(
    slot: AuthResourceSlot,
    access: HistoryAccess,
): TransferHistoryEntity {
    require(
        slot.fileCount == null ||
            (slot.completedFiles != null &&
                slot.completedFiles in 0..requireNotNull(slot.fileCount))
    ) {
        "Invalid inbox summary counts"
    }
    return TransferHistoryEntity(
        id = slot.id,
        type = "received",
        fileCount = (slot.completedFiles ?: 0).coerceAtMost(Int.MAX_VALUE.toLong()).toInt(),
        summaryUpdating = slot.summary?.ready != true,
        totalSize = 0, // Encrypted server bytes cannot be presented as plaintext file sizes.
        serverUrl = access.serverUrl,
        encryptionKey = "",
        status = if (slot.status in listOf("revoked", "expired")) "unavailable" else slot.status,
        accountId = access.accountId,
        createdAt = parseHistoryExpiry(slot.createdAt) ?: System.currentTimeMillis(),
        expiresAt = parseHistoryExpiry(slot.expiresAt),
        maxFiles = slot.maxFiles,
        reservedFiles = slot.reservedFiles,
    )
}

internal fun mergeAccountResource(
    current: TransferHistoryEntity?,
    incoming: TransferHistoryEntity,
    access: HistoryAccess,
): TransferHistoryEntity? {
    require(incoming.accountId == access.accountId && access.permits(incoming))
    // An ID collision must never adopt a legacy row or another account's key/capability.
    if (
        current != null &&
            (current.type != incoming.type ||
                current.accountId != access.accountId ||
                !access.permits(current))
    )
        return null
    val updated =
        current?.copy(
            fileCount = if (incoming.summaryUpdating) current.fileCount else incoming.fileCount,
            summaryUpdating = incoming.summaryUpdating,
            expiresAt = incoming.expiresAt ?: current.expiresAt,
            maxDownloads = incoming.maxDownloads ?: current.maxDownloads,
            maxFiles = incoming.maxFiles ?: current.maxFiles,
            reservedFiles =
                incoming.reservedFiles?.let { maxOf(current.reservedFiles ?: 0, it) }
                    ?: current.reservedFiles,
            status =
                if (current.status == "failed" && incoming.status == "pending") "failed"
                else incoming.status,
        ) ?: incoming
    return updated
}
