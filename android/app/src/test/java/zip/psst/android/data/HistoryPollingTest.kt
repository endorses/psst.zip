package zip.psst.android.data

import org.junit.Assert.*
import org.junit.Test

class HistoryPollingTest {
    @Test
    fun serverDeadlineSurvivesWakeupsAndPollerRestartsUntilElapsed() {
        var now = 1000L
        val gate = HistoryRetryGate { now }
        gate.defer("one|owner", 90_000)
        now += 10_000
        repeat(5) { assertEquals(80_000L, gate.remaining("one|owner")) }
        assertEquals(0L, gate.remaining("one|other"))
        gate.defer("one|owner", 10_000)
        assertEquals(80_000L, gate.remaining("one|owner"))
        now += 80_000
        assertEquals(0L, gate.remaining("one|owner"))
        gate.defer("one|owner", Long.MAX_VALUE)
        assertEquals(Long.MAX_VALUE - now, gate.remaining("one|owner"))
    }

    @Test
    fun mutationSignalsStayWithinTheirServerAndAccountScope() {
        val access = HistoryAccess("https://one.test", "owner")
        assertTrue(HistoryMutation(localHistoryScope("https://one.test/"), "owner").matches(access))
        assertFalse(HistoryMutation(localHistoryScope("https://two.test"), "owner").matches(access))
        assertFalse(HistoryMutation(localHistoryScope("https://one.test"), "other").matches(access))
        assertTrue(HistoryMutation(localHistoryScope("https://one.test"), null).matches(access))
    }

    @Test
    fun tenSecondCadenceAndFailureBackoffRespectLongerServerInstructions() {
        assertEquals(10_000L, historyPollingDelay(0))
        assertEquals(
            listOf(10_000L, 20_000L, 40_000L, 60_000L, 60_000L),
            (1..5).map { historyPollingDelay(it) },
        )
        assertEquals(10_321L, historyPollingDelay(1, jitterMillis = 321))
        assertEquals(60_000L, historyPollingDelay(4, jitterMillis = 999))
        assertEquals(90_000L, historyPollingDelay(2, retryAfterMillis = 90_000))
        assertEquals(10_000L, historyPollingDelay(0, jitterMillis = 321))
    }
}
