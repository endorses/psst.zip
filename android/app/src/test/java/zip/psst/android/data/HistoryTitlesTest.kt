package zip.psst.android.data

import org.junit.Assert.*
import org.junit.Test

class HistoryTitlesTest {
    private val entry =
        TransferHistoryEntity(
            "same",
            "sent",
            4,
            400,
            "https://files.example",
            "key",
            "complete",
            accountId = "alice",
            automaticTitle = "holiday.jpg",
        )

    @Test
    fun automaticTitleIncludesAdditionalCountAndCustomTitleWins() {
        assertEquals("holiday.jpg + 3 files", historyTitle(entry))
        assertEquals("Wedding", historyTitle(entry.copy(title = "Wedding")))
        assertEquals("holiday.jpg + 3 files", historyTitle(entry.copy(title = "")))
    }

    @Test
    fun refreshPreservesCustomAndAutomaticTitles() {
        val access = HistoryAccess(entry.serverUrl, "alice")
        val merged =
            mergeAccountResource(
                entry.copy(title = "Wedding"),
                entry.copy(title = null, automaticTitle = null, status = "downloaded"),
                access,
            )!!
        assertEquals("Wedding", merged.title)
        assertEquals("holiday.jpg", merged.automaticTitle)
    }

    @Test
    fun longUnicodeTitleRetainsExtensionAndCountWithoutBreakingSurrogates() {
        val original = "🧑".repeat(90) + ".jpg + 3 files"
        val compact = compactHistoryTitle(original)
        assertTrue(compact.endsWith(".jpg + 3 files"))
        assertTrue(compact.codePointCount(0, compact.length) <= 64)
        assertFalse(compact.contains("\uFFFD"))
    }

    @Test
    fun labelsDoNotCrossAccountServerOrTypeCollision() {
        val access = HistoryAccess(entry.serverUrl, "alice")
        assertNull(mergeAccountResource(entry.copy(accountId = "bob"), entry, access))
        assertFalse(access.permits(entry.copy(serverUrl = "https://other.example")))
    }
}
