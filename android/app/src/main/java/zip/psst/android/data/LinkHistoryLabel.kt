package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*

/** Completed files and cumulative reservations are intentionally different facts. */
internal fun historyLinkPolicyLabel(row: TransferHistoryEntity): UiText? =
    if (row.type == "sent" || row.type == "send")
        row.maxDownloads?.let { limit ->
            if (limit == 0) message(R.string.policy_download_unlimited)
            else pluralMessage(R.plurals.policy_download_limit, limit.toLong(), limit)
        }
    else
        row.maxFiles?.let { limit ->
            val used = row.reservedFiles
            when {
                limit == 0 && used != null ->
                    pluralMessage(R.plurals.policy_receive_unlimited_used, used, used)
                limit == 0 -> message(R.string.policy_receive_unlimited)
                used != null ->
                    pluralMessage(R.plurals.policy_receive_usage, limit.toLong(), used, limit)
                else -> pluralMessage(R.plurals.policy_receive_limit, limit.toLong(), limit)
            }
        }
