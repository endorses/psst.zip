package zip.psst.android.data

/** A presentation projection only. Account and local stores remain independent. */
sealed interface HistoryRow {
    val key: String
    val createdAt: Long

    data class Owned(val value: TransferHistoryEntity) : HistoryRow {
        override val key = "owned:${value.serverUrl}:${value.accountId}:${value.type}:${value.id}"
        override val createdAt = value.createdAt
    }

    data class Downloaded(val value: GuestDownload) : HistoryRow {
        override val key = "downloaded:${value.origin}:${value.transferId}:${value.identity}"
        override val createdAt = value.createdAt
    }
}

fun unifiedHistory(
    owned: List<TransferHistoryEntity>,
    downloaded: List<GuestDownload>,
    filter: String,
): List<HistoryRow> =
    (owned.map { HistoryRow.Owned(it) } + downloaded.map { HistoryRow.Downloaded(it) })
        .filter { row ->
            when (filter) {
                "sent" -> row is HistoryRow.Owned && row.value.type == "sent"
                "received" -> row is HistoryRow.Owned && row.value.type != "sent"
                "downloaded" -> row is HistoryRow.Downloaded
                else -> true
            }
        }
        .sortedWith(compareByDescending<HistoryRow> { it.createdAt }.thenBy { it.key })
