package zip.psst.android.data

import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.SlotTransfer

/** New arrivals never expand an already shown/approved selection. */
internal fun shownInboxTransfers(shown: DropSlot, refreshed: DropSlot): List<SlotTransfer> {
    require(
        shown.id == refreshed.id &&
            shown.receiveProtocol == refreshed.receiveProtocol &&
            shown.recipientPublicKey == refreshed.recipientPublicKey
    ) {
        "The inbox receive key changed. Reopen the inbox and retry."
    }
    val selected = shown.completedTransfers
    val ids = selected.map { it.transferId }.toSet()
    require(refreshed.completedTransfers.filter { it.transferId in ids } == selected) {
        "The shown uploads changed. Refresh this page and retry."
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
        require(cursor != this.cursor && cursor !in previous) {
            "The server repeated an inbox page. Return to the first page and retry."
        }
        return InboxPager(cursor, (previous + listOf(this.cursor)).takeLast(100), number + 1)
    }

    fun back(): InboxPager {
        require(previous.isNotEmpty())
        return InboxPager(previous.last(), previous.dropLast(1), number - 1)
    }
}

data class InboxReadIdentity(val access: HistoryAccess, val slotId: String, val pager: InboxPager) {
    fun accepts(access: HistoryAccess, slotId: String?, pager: InboxPager): Boolean =
        this.access == access && this.slotId == slotId && this.pager == pager
}
