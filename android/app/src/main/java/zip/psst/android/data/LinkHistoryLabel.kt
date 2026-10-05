package zip.psst.android.data

/** Completed files and cumulative reservations are intentionally different facts. */
internal fun historyLinkPolicyLabel(row: TransferHistoryEntity): String? =
    if (row.type == "sent" || row.type == "send") {
        row.maxDownloads?.let { limit ->
            if (limit == 0) "No optional download limit"
            else "Limit: $limit download attempts per file"
        }
    } else
        row.maxFiles?.let { limit ->
            val used = row.reservedFiles
            if (limit == 0)
                "No optional file-count limit" + (used?.let { " · $it file allowances used" } ?: "")
            else
                (used?.let { "$it of $limit file allowances used" }
                    ?: "Limit: $limit files accepted") + " · unfinished uploads also count"
        }
