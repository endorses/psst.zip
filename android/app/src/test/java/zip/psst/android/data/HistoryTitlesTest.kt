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
    fun untrustedManifestNamesAreSafeInHistoryAndDetailsWithoutChangingIdentity() {
        val raw = "photo\u061C\u200E\u200F\u202Ejpg.exe"
        val expected = "photo____jpg.exe"
        assertEquals(expected, receivedFilenameLabel(raw))
        assertEquals("$expected + 1 file", automaticHistoryTitle(raw, 2))
        val record = entry.copy(automaticTitle = raw)
        assertEquals("$expected + 3 files", historyTitle(record))
        assertEquals(raw, record.automaticTitle)
        assertEquals("File", receivedFilenameLabel("../unsafe.exe"))
        assertEquals("File", receivedFilenameLabel(""))
        assertEquals("صورة.jpg", receivedFilenameLabel("صورة.jpg"))
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
