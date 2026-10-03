package zip.psst.android.viewmodel

import zip.psst.android.data.HistoryAccess
import org.junit.Assert.*
import org.junit.Test

class TaskContinuityTest {
    @Test
    fun reauthenticationOnlyRestoresTheOriginalAccountAndServer() {
        val origin = HistoryAccess("https://host", "alice")
        assertTrue(canResumeSelection(origin, HistoryAccess("https://host/", "alice")))
        assertFalse(canResumeSelection(origin, HistoryAccess("https://host", "bob")))
        assertFalse(canResumeSelection(origin, HistoryAccess("https://other", "alice")))
        assertTrue(canResumeSelection(HistoryAccess(), HistoryAccess("https://host", "alice")))
    }
}
