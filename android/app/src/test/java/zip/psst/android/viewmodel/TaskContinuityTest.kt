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
        assertFalse(
            canResumeSelection(
                HistoryAccess("https://host"),
                HistoryAccess("https://other", "alice"),
            )
        )
    }

    @Test
    fun sameAccountRecoveryRetainsProtectionButDropsOldCompletion() {
        var send =
            SendUiState(
                maxDownloadsInput = "8",
                downloadLimitEnabled = true,
                linkPolicyLocked = true,
                requiresLogin = true,
                transferId = "old-resource",
                encryptionKey = "old-key",
                downloadUrl = "old-link",
                isUploading = true,
            )
        var receive =
            ReceiveUiState(
                localName = "Documents from Alice",
                maxFilesInput = "5",
                fileLimitEnabled = true,
                linkPolicyLocked = true,
                requiresLogin = true,
                isCreatingSlot = true,
            )
        for (recovery in
            listOf(
                SelectionRecovery.RESTRICTED,
                SelectionRecovery.RESTRICTED,
                SelectionRecovery.READY,
            )) {
            send = restoreSendDraft(send, recovery)
            receive = restoreReceiveDraft(receive, recovery)
            assertEquals("8", send.maxDownloadsInput)
            assertTrue(send.downloadLimitEnabled)
            assertTrue(send.linkPolicyLocked)
            assertNull(send.pendingCompletion())
            assertNull(send.downloadUrl)
            assertFalse(send.isUploading)
            assertEquals("5", receive.maxFilesInput)
            assertEquals("Documents from Alice", receive.localName)
            assertTrue(receive.fileLimitEnabled)
            assertTrue(receive.linkPolicyLocked)
            assertFalse(receive.isCreatingSlot)
            assertEquals(recovery == SelectionRecovery.RESTRICTED, send.requiresLogin)
            assertEquals(send.requiresLogin, receive.requiresLogin)
        }
        assertEquals(SendUiState(), restoreSendDraft(send, SelectionRecovery.DISCARD))
        assertEquals(ReceiveUiState(), restoreReceiveDraft(receive, SelectionRecovery.DISCARD))
    }
}
