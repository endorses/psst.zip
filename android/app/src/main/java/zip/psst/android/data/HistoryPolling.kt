package zip.psst.android.data

/** Cadence applies only while History's lifecycle owns the poller. */
internal fun historyPollingDelay(
    failures: Int,
    retryAfterMillis: Long = 0,
    jitterMillis: Long = 0,
): Long {
    require(failures >= 0 && retryAfterMillis >= 0 && jitterMillis in 0..999)
    val ordinary =
        if (failures == 0) 10_000L
        else ((10_000L shl (failures - 1).coerceAtMost(3)) + jitterMillis).coerceAtMost(60_000L)
    return maxOf(ordinary, retryAfterMillis)
}

/** A lifecycle restart or explicit wake-up cannot shorten the server's retry deadline. */
internal class HistoryRetryGate(private val nowMillis: () -> Long) {
    private val deadlines = LinkedHashMap<String, Long>()

    @Synchronized
    fun defer(scope: String, delayMillis: Long) {
        require(delayMillis >= 0)
        val now = nowMillis()
        deadlines.entries.removeAll { it.value <= now }
        if (delayMillis == 0L) return
        val until = if (delayMillis > Long.MAX_VALUE - now) Long.MAX_VALUE else now + delayMillis
        deadlines[scope] = maxOf(deadlines[scope] ?: 0, until)
        while (deadlines.size > 200) deadlines.remove(deadlines.minBy { it.value }.key)
    }

    @Synchronized
    fun remaining(scope: String): Long {
        val now = nowMillis()
        val until = deadlines[scope] ?: return 0
        if (until <= now) {
            deadlines.remove(scope)
            return 0
        }
        return until - now
    }
}
