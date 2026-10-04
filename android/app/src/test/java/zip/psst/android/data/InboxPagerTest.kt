package zip.psst.android.data

import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.SlotTransfer
import zip.psst.shared.model.TransferStatus
import org.junit.Assert.*
import org.junit.Test

class InboxPagerTest {
    @Test
    fun rollingHistoryDoesNotCapForwardBrowsing() {
        var pager = InboxPager()
        repeat(2000) { pager = pager.next("cursor-$it") }
        assertEquals(2001L, pager.number)
        assertEquals(100, pager.previous.size)
        repeat(100) { pager = pager.back() }
        assertTrue(pager.previous.isEmpty())
        assertEquals(1901L, pager.number)
        assertNull(InboxPager().cursor)
    }

    @Test
    fun repeatedCursorFailsWithoutChangingVisiblePage() {
        val page = InboxPager().next("one").next("two")
        assertThrows(IllegalArgumentException::class.java) { page.next("one") }
        assertThrows(IllegalArgumentException::class.java) { page.next("two") }
        assertEquals("two", page.cursor)
    }

    @Test
    fun accountCredentialOriginInboxAndCursorChangesRejectStaleResults() {
        val access = HistoryAccess("https://host", "owner", credentialVersion = 1)
        val pager = InboxPager().next("one")
        val request = InboxReadIdentity(access, "inbox", pager)
        assertTrue(request.accepts(access, "inbox", pager))
        assertFalse(request.accepts(access.copy(accountId = "other"), "inbox", pager))
        assertFalse(request.accepts(access.copy(credentialVersion = 2), "inbox", pager))
        assertFalse(request.accepts(access.copy(serverUrl = "https://other"), "inbox", pager))
        assertFalse(request.accepts(access, "other", pager))
        assertFalse(request.accepts(access, "inbox", pager.next("two")))
    }

    @Test
    fun savingPartialPageNeverMarksWholeInboxSavedEvenWhenTotalsUnknown() {
        val row = TransferHistoryEntity("inbox", "received", 0, 0, "https://host", "key", "waiting")
        val partial =
            ReceivedSnapshot(
                mapOf("child" to ReceivedChild(2)),
                completedFiles = 10,
                partial = true,
            )
        val saved = mergeReceivedHistory(row, partial, saved = true)
        assertEquals("has_uploads", saved.status)
        assertEquals(10, saved.fileCount)
        assertEquals(setOf("child"), saved.savedTransferIds())
        val unknown = mergeReceivedHistory(row, partial.copy(completedFiles = null), saved = true)
        assertEquals("has_uploads", unknown.status)
        assertEquals(
            "has_uploads",
            mergeReceivedHistory(unknown, ReceivedSnapshot(emptyMap(), partial = true)).status,
        )
        assertEquals(
            Int.MAX_VALUE,
            mergeReceivedHistory(row, partial.copy(completedFiles = Long.MAX_VALUE)).fileCount,
        )
    }

    @Test
    fun consentSelectionNeverExpandsToNewArrivalsAndRejectsChangedChildren() {
        val child = SlotTransfer("shown", TransferStatus.COMPLETE, 2)
        val shown = DropSlot("inbox", transfers = listOf(child))
        val next =
            shown.copy(transfers = listOf(child, SlotTransfer("later", TransferStatus.COMPLETE, 3)))
        assertEquals(listOf(child), shownInboxTransfers(shown, next))
        assertThrows(IllegalArgumentException::class.java) {
            shownInboxTransfers(shown, shown.copy(transfers = emptyList()))
        }
        assertThrows(IllegalArgumentException::class.java) {
            shownInboxTransfers(shown, shown.copy(transfers = listOf(child.copy(fileCount = 1))))
        }
        assertThrows(IllegalArgumentException::class.java) {
            shownInboxTransfers(shown, shown.copy(recipientPublicKey = "changed"))
        }
    }

    @Test
    fun authoritativeCleanupCountsReplaceOlderTotalsWithoutRemovingSaveCheckpoints() {
        val row =
            TransferHistoryEntity("inbox", "received", 500, 0, "https://host", "key", "complete")
        val updated =
            mergeReceivedHistory(
                row,
                ReceivedSnapshot(
                    mapOf("saved" to ReceivedChild(2)),
                    completedFiles = 1,
                    partial = true,
                ),
                saved = true,
            )
        assertEquals(1, updated.fileCount)
        assertEquals(setOf("saved"), updated.savedTransferIds())
        assertEquals("has_uploads", updated.status)
        val unknown =
            mergeReceivedHistory(
                updated,
                ReceivedSnapshot(mapOf("later" to ReceivedChild(10)), partial = true),
            )
        assertEquals(1, unknown.fileCount)
        assertEquals(updated.savedTransferIds(), unknown.savedTransferIds())
    }
}
