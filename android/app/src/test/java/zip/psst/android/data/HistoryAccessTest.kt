package zip.psst.android.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class HistoryAccessTest {
    private val own =
        TransferHistoryEntity(
            "a",
            "sent",
            1,
            10,
            "https://files.example",
            "secret",
            "complete",
            accountId = "alice",
        )

    @Test
    fun historyIsScopedToServerAndAccount() {
        val access = HistoryAccess("https://files.example/", "alice")
        assertTrue(access.permits(own))
        assertFalse(access.permits(own.copy(accountId = "bob")))
        assertFalse(access.permits(own.copy(serverUrl = "https://other.example")))
        assertFalse(HistoryAccess().permits(own))
        assertFalse(access.copy(mustChangePassword = true).permits(own))
        assertFalse(HistoryAccess("https://files.example", null, true).permits(own))
    }

    @Test
    fun adminCannotSeePersonalOrUnassignedLegacyHistory() {
        val legacy = own.copy(accountId = null)
        assertFalse(HistoryAccess(own.serverUrl, "alice").permits(legacy))
        assertFalse(HistoryAccess(own.serverUrl, "admin", true).permits(legacy))
        assertFalse(HistoryAccess("https://other.example", "admin", true).permits(legacy))
        assertFalse(HistoryAccess(own.serverUrl, "admin", true).permits(own))
    }

    @Test
    fun historyMergePreservesAccountOwnership() {
        val merged = mergeReceivedHistory(own, ReceivedSnapshot(emptyMap()))
        assertEquals("alice", merged.accountId)
    }
}
