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

internal fun automaticHistoryTitle(firstName: String?, count: Int): String? =
    firstName?.let {
        if (count > 1) "$it + ${count - 1} ${if (count == 2) "file" else "files"}" else it
    }

internal fun historyTitle(entry: TransferHistoryEntity): String =
    entry.title?.takeIf { it.isNotBlank() }
        ?: automaticHistoryTitle(entry.automaticTitle, entry.fileCount)
        ?: "${entry.fileCount} ${if (entry.fileCount == 1) "file" else "files"} · ${java.text.DateFormat.getDateTimeInstance(java.text.DateFormat.MEDIUM, java.text.DateFormat.SHORT).format(java.util.Date(entry.createdAt))}"

/** Keep the filename extension/count visible, while accessibility exposes the full original. */
internal fun compactHistoryTitle(title: String, limit: Int = 64): String {
    if (title.codePointCount(0, title.length) <= limit) return title
    val suffixStart =
        title.lastIndexOf('.').takeIf { it > 0 && title.length - it <= 24 }
            ?: title.offsetByCodePoints(0, title.codePointCount(0, title.length) - 16)
    val suffix = title.substring(suffixStart)
    val prefixLength = (limit - suffix.codePointCount(0, suffix.length) - 1).coerceAtLeast(8)
    return title.substring(0, title.offsetByCodePoints(0, prefixLength)) + "…" + suffix
}
