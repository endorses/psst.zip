package zip.psst.android.viewmodel

import zip.psst.android.data.HistoryAccess
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.savedFileCount
import zip.psst.shared.model.UrlHelper
import kotlin.io.encoding.Base64

/** Existing links are restored entirely from the scoped local record; this never creates a slot. */
internal fun restoreReceiveEntry(
    row: TransferHistoryEntity,
    access: HistoryAccess,
): ReceiveUiState {
    require(access.permits(row)) { "Receive link belongs to another account" }
    require(row.type in listOf("receive", "received"))
    val key = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).decode(row.encryptionKey)
    require(key.size == 32) { "Missing encryption key" }
    return ReceiveUiState(
        slotId = row.id,
        encryptionKey = row.encryptionKey,
        uploadUrl = UrlHelper.buildUploadUrl(row.serverUrl, row.id, key),
        slotStatus = row.status,
        downloadComplete = row.status == "complete",
        savedFileCount = row.savedFileCount(),
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
