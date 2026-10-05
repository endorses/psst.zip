package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.model.ManifestValidator

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
        .sortedWith(
            compareByDescending<HistoryRow> { it.createdAt }
                .thenByDescending {
                    when (it) {
                        is HistoryRow.Owned -> "owned:${it.value.id}"
                        is HistoryRow.Downloaded -> "downloaded:${it.value.identity}"
                    }
                }
                .thenBy { it.key }
        )

/** Display only: persisted manifest names remain untouched for authentication and resume checks. */
internal fun receivedFilenameLabel(name: String): String = displayFilename(name).text()

internal fun displayFilename(name: String): UiText =
    runCatching { userText(ManifestValidator.safeFilename(name)) }
        .getOrDefault(message(R.string.ui_unknown_file))

internal fun automaticHistoryTitle(firstName: String?, count: Int): UiText? =
    firstName?.let {
        val name = displayFilename(it)
        if (count > 1)
            pluralMessage(R.plurals.automatic_history_title, (count - 1).toLong(), name, count - 1)
        else name
    }

internal fun historyTitle(entry: TransferHistoryEntity): UiText =
    entry.sharedTitle?.takeIf { it.isNotBlank() }?.let(::userText)
        ?: entry.title?.takeIf { it.isNotBlank() }?.let(::userText)
        ?: automaticHistoryTitle(entry.automaticTitle, entry.fileCount)
        ?: message(
            if (entry.type == "sent") R.string.ui_sent_fallback else R.string.ui_receive_fallback
        )

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
