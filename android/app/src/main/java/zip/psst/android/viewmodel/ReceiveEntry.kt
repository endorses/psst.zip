package zip.psst.android.viewmodel

import zip.psst.android.data.HistoryAccess
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.decodeInboxKeyMarker
import zip.psst.shared.model.UrlHelper

/** Existing links are restored entirely from the scoped local record; this never creates a slot. */
internal fun restoreReceiveEntry(
    row: TransferHistoryEntity,
    access: HistoryAccess,
    privateKeyAvailable: Boolean = true,
): ReceiveUiState {
    require(access.permits(row)) { "Receive link belongs to another account" }
    require(row.type in listOf("receive", "received"))
    val key = runCatching { decodeInboxKeyMarker(row.encryptionKey) }.getOrNull()
    val canDecrypt = key?.size == 32 && privateKeyAvailable
    return ReceiveUiState(
        slotId = row.id,
        localName = row.sharedTitle ?: row.title.orEmpty(),
        encryptionKey = row.encryptionKey,
        keyUnavailable = !canDecrypt,
        uploadUrl =
            if (canDecrypt && row.encryptionKey.startsWith("v2."))
                UrlHelper.buildReceiveUrl(row.serverUrl, row.id, requireNotNull(key))
            else null,
        legacyReadOnly = !row.encryptionKey.startsWith("v2."),
        slotStatus = row.status,
        downloadComplete = row.status == "complete",
        savedFileCount = row.checkpointSavedFiles.coerceAtMost(Int.MAX_VALUE.toLong()).toInt(),
        checkpointState = row.checkpointState,
    )
}

internal enum class ReceiveRetry {
    SIGN_IN,
    SAVE,
    REOPEN,
    CREATE,
}

internal fun ReceiveUiState.retryAction(existingId: String?): ReceiveRetry =
    when {
        requiresLogin -> ReceiveRetry.SIGN_IN
        slotId != null -> ReceiveRetry.SAVE
        existingId != null -> ReceiveRetry.REOPEN
        else -> ReceiveRetry.CREATE
    }
