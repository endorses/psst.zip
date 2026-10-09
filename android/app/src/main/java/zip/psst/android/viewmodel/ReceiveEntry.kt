package zip.psst.android.viewmodel

import zip.psst.android.R
import zip.psst.android.data.HistoryAccess
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.decodeInboxKeyMarker
import zip.psst.android.i18n.UiText
import zip.psst.android.i18n.message
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

internal fun ReceiveUiState.storagePermissionDenied(): ReceiveUiState =
    copy(
        error =
            message(
                R.string.l_storage_access_is_required_to_save_in_downloads_psst_zip_on_this__32debb,
            ),
    )

/** A fresh inbox response cannot resolve a denied device storage permission. */
internal fun ReceiveUiState.storagePermissionErrorAfterRefresh(): UiText? = error?.takeIf {
    it.resource ==
        R.string.l_storage_access_is_required_to_save_in_downloads_psst_zip_on_this__32debb
}
