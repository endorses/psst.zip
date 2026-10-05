package zip.psst.android.data

import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow

internal data class HistoryMutation(val originScope: String, val accountId: String?) {
    fun matches(access: HistoryAccess): Boolean =
        originScope == localHistoryScope(access.serverUrl) &&
            (accountId == null || accountId == access.accountId)
}

/** Metadata wake-ups only; these never carry a credential or retry a transfer payload. */
internal object HistoryNotifications {
    // Navigation can destroy a ViewModel; server delays belong to the process/server-account scope.
    val retryGate = HistoryRetryGate { android.os.SystemClock.elapsedRealtime() }

    private val mutable =
        MutableSharedFlow<HistoryMutation>(
            extraBufferCapacity = 1,
            onBufferOverflow = BufferOverflow.DROP_OLDEST,
        )
    val changes = mutable.asSharedFlow()

    fun changed(serverUrl: String, accountId: String? = null) {
        mutable.tryEmit(HistoryMutation(localHistoryScope(serverUrl), accountId))
    }
}
