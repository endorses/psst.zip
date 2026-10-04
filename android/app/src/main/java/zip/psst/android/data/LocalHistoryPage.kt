package zip.psst.android.data

/** Stable keyset anchor; deleting the boundary record does not invalidate it. */
data class LocalHistoryCursor(val createdAt: Long, val id: String)

data class LocalHistoryPager(
    val cursor: LocalHistoryCursor? = null,
    val previous: List<LocalHistoryCursor?> = emptyList(),
    val number: Long = 1,
) {
    fun next(cursor: LocalHistoryCursor) =
        LocalHistoryPager(cursor, (previous + listOf(this.cursor)).takeLast(100), number + 1)

    fun back() = LocalHistoryPager(previous.last(), previous.dropLast(1), number - 1)
}

internal fun localHistoryScope(origin: String) = origin.trim().trimEnd('/').lowercase()
