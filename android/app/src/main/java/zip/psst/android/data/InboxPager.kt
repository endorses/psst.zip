package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.SlotTransfer

/** New arrivals never expand an already shown/approved selection. */
internal fun shownInboxTransfers(shown: DropSlot, refreshed: DropSlot): List<SlotTransfer> {
    uiRequire(
        shown.id == refreshed.id &&
            shown.receiveProtocol == refreshed.receiveProtocol &&
            shown.recipientPublicKey == refreshed.recipientPublicKey
    ) {
        message(R.string.l_the_inbox_receive_key_changed_reopen_the_inbox_and_retry_bece6f)
    }
    val selected = shown.completedTransfers
    val ids = selected.map { it.transferId }.toSet()
    uiRequire(refreshed.completedTransfers.filter { it.transferId in ids } == selected) {
        message(R.string.l_the_shown_uploads_changed_refresh_this_page_and_retry_376c2e)
    }
    return selected
}

/** Keep a rolling back-stack while forward browsing remains unlimited. */
data class InboxPager(
    val cursor: String? = null,
    val previous: List<String?> = emptyList(),
    val number: Long = 1,
) {
    fun next(cursor: String): InboxPager {
        uiRequire(cursor != this.cursor && cursor !in previous) {
            message(
                R.string.l_the_server_repeated_an_inbox_page_return_to_the_first_page_and_re_12c277
            )
        }
        return InboxPager(cursor, (previous + listOf(this.cursor)).takeLast(100), number + 1)
    }

    fun back(): InboxPager {
        uiRequire(previous.isNotEmpty())
        return InboxPager(previous.last(), previous.dropLast(1), number - 1)
    }
}

data class InboxReadIdentity(val access: HistoryAccess, val slotId: String, val pager: InboxPager) {
    fun accepts(access: HistoryAccess, slotId: String?, pager: InboxPager): Boolean =
        this.access == access && this.slotId == slotId && this.pager == pager
}
