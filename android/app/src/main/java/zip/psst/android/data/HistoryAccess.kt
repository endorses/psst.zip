package zip.psst.android.data

/** Local keys and owner capabilities belong to the account that created them. */
data class HistoryAccess(
    val serverUrl: String = "",
    val accountId: String? = null,
    val isAdmin: Boolean = false,
) {
    fun permits(row: TransferHistoryEntity): Boolean {
        if (accountId.isNullOrBlank()) return false
        if (
            row.serverUrl.trim().trimEnd('/').lowercase() !=
                serverUrl.trim().trimEnd('/').lowercase()
        )
            return false
        return row.accountId == accountId || (row.accountId == null && isAdmin)
    }
}
