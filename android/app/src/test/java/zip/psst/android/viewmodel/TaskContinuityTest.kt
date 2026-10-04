package zip.psst.android.viewmodel

import zip.psst.android.data.HistoryAccess
import org.junit.Assert.*
import org.junit.Test

class TaskContinuityTest {
    @Test
    fun pendingSelectionSurvivesRestrictedPasswordReplacementUntilSameAccountIsReady() {
        val origin = HistoryAccess("https://host", "alice")
        var pending = true
        for (access in
            listOf(
                HistoryAccess(),
                origin.copy(mustChangePassword = true),
                HistoryAccess(),
                origin,
            )) {
            val recovery = selectionRecovery(pending, origin, access)
            assertNotEquals(SelectionRecovery.DISCARD, recovery)
            pending = recovery == SelectionRecovery.RESTRICTED
        }
        assertFalse(pending)
        assertEquals(
            SelectionRecovery.DISCARD,
            selectionRecovery(true, origin, origin.copy(accountId = "bob")),
        )
        assertEquals(
            SelectionRecovery.DISCARD,
            selectionRecovery(true, origin, origin.copy(isAdmin = true)),
        )
        assertEquals(SelectionRecovery.DISCARD, selectionRecovery(false, origin, HistoryAccess()))
    }

    @Test
    fun reauthenticationOnlyRestoresTheOriginalAccountAndServer() {
        val origin = HistoryAccess("https://host", "alice")
        assertTrue(canResumeSelection(origin, HistoryAccess("https://host/", "alice")))
        assertFalse(canResumeSelection(origin, HistoryAccess("https://host", "bob")))
        assertFalse(canResumeSelection(origin, HistoryAccess("https://other", "alice")))
        assertTrue(canResumeSelection(HistoryAccess(), HistoryAccess("https://host", "alice")))
    }
}
