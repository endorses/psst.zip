package zip.psst.android.data

/** Saved empty is meaningful: the launch intent has already been delivered to Send. */
internal fun <T> restorePendingShares(saved: List<T>?, launch: List<T>): List<T> = saved ?: launch
