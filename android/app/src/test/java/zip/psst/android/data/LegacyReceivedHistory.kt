package zip.psst.android.data

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

/** Old-schema fixture construction; production never decodes or rewrites whole checkpoint JSON. */
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
    val knownCount =
        children.values.sumOf { it.fileCount.toLong() }.coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
    val count =
        if (snapshot.partial) {
            snapshot.completedFiles?.coerceAtMost(Int.MAX_VALUE.toLong())?.toInt()
                ?: current.fileCount
        } else maxOf(current.fileCount, knownCount)
    val status =
        when {
            snapshot.partial && current.status == "complete" ->
                if (count > 0) "has_uploads" else "waiting"
            children.isEmpty() -> current.status
            !snapshot.partial && knownCount == count && savedIds.containsAll(children.keys) ->
                "complete"
            children.isNotEmpty() || count > 0 -> "has_uploads"
            else -> current.status
        }
    return current.copy(
        fileCount = count,
        summaryUpdating =
            if (snapshot.summaryObserved) snapshot.completedFiles == null
            else current.summaryUpdating,
        totalSize = maxOf(current.totalSize, children.values.sumOf { it.plaintextSize ?: 0 }),
        status = status,
        receivedTransfersJson = Json.encodeToString(children.toMap()),
        savedTransferIdsJson = Json.encodeToString(savedIds),
        expiresAt = snapshot.expiresAt ?: current.expiresAt,
    )
}
