package zip.psst.android.data

/** Source cursors stay internal; the visible window never exceeds fifty records. */
data class MergedHistoryCursor(
    val account: LocalHistoryCursor? = null,
    val downloads: LocalHistoryCursor? = null,
)

data class MergedHistoryPage(val rows: List<HistoryRow>, val next: MergedHistoryCursor?)

fun mergeHistoryPage(
    account: List<TransferHistoryEntity>,
    downloads: List<GuestDownload>,
    after: MergedHistoryCursor = MergedHistoryCursor(),
    accountHasMore: Boolean = false,
    downloadsHaveMore: Boolean = false,
    downloadsAfterPage: LocalHistoryCursor? = null,
): MergedHistoryPage {
    require(account.size <= 51 && downloads.size <= 50)
    val merged = unifiedHistory(account, downloads, "all")
    val visible = merged.take(50)
    val lastAccount = visible.filterIsInstance<HistoryRow.Owned>().lastOrNull()?.value
    val lastDownload = visible.filterIsInstance<HistoryRow.Downloaded>().lastOrNull()?.value
    val cursor =
        MergedHistoryCursor(
            lastAccount?.let { LocalHistoryCursor(it.createdAt, it.id) } ?: after.account,
            if (
                visible.count { it is HistoryRow.Downloaded } == downloads.size &&
                    downloadsAfterPage != null
            )
                downloadsAfterPage
            else
                lastDownload?.let { LocalHistoryCursor(it.createdAt, it.identity) }
                    ?: after.downloads,
        )
    return MergedHistoryPage(
        visible,
        cursor.takeIf { merged.size > 50 || accountHasMore || downloadsHaveMore },
    )
}
