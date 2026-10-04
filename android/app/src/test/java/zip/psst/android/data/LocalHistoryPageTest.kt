package zip.psst.android.data

import java.io.ByteArrayInputStream
import java.io.InputStream
import org.junit.Assert.*
import org.junit.Test

class LocalHistoryPageTest {
    @Test
    fun previousWindowRollsWithoutLimitingReachablePages() {
        var pager = LocalHistoryPager()
        repeat(2200) { pager = pager.next(LocalHistoryCursor(2200L - it, "id-$it")) }
        assertEquals(2201L, pager.number)
        assertEquals(100, pager.previous.size)
        repeat(100) { pager = pager.back() }
        assertEquals(2101L, pager.number)
        assertTrue(pager.previous.isEmpty())
        assertNull(LocalHistoryPager().cursor)
    }

    @Test
    fun originScopeDoesNotChangeSavedServerIdentity() {
        val url = " HTTPS://Example.COM/ "
        assertEquals("https://example.com", localHistoryScope(url))
        val row = TransferHistoryEntity("id", "sent", 1, 7, url, "secret", "complete")
        assertEquals(url, row.serverUrl)
        assertEquals("secret", row.encryptionKey)
        assertEquals("https://example.com", row.originScope)
    }

    @Test
    fun importWorkIncludesUnrelatedEntriesAndResumesWithoutDroppingRows() {
        val indexed = mutableSetOf<Int>()
        val entries = (0 until 1001).iterator()
        var calls = 0
        do {
            var visited = 0
            val more =
                entries.visitGuestImportBatch {
                    visited++
                    indexed += it
                }
            assertTrue(visited <= 64)
            calls++
        } while (more)
        assertEquals(16, calls)
        assertEquals((0 until 1001).toSet(), indexed)
        // A process restart discovers prior entries again; INSERT IGNORE retains newer values.
        val restarted = (0 until 1001).iterator()
        restarted.visitGuestImportBatch { assertTrue(it in indexed) }
        assertEquals(1001, indexed.size)
    }

    @Test
    fun importNamesRecognizeAtomicBackupsAndRejectTemporaryAndTraversalNames() {
        val identity = "a".repeat(64)
        assertEquals("json" to identity, guestLegacyEntry("$identity.json.bak"))
        assertEquals("upload" to "upload-$identity", guestLegacyEntry("upload-$identity.upload"))
        assertNull(guestLegacyEntry("$identity.json.new"))
        assertNull(guestLegacyEntry("$identity.key"))
        assertNull(guestLegacyEntry("../$identity.json"))
    }

    @Test
    fun oversizedLegacyPayloadStopsAfterOneExtraByteAndKeepsSource() {
        var consumed = 0
        val source =
            object : InputStream() {
                override fun read(): Int {
                    consumed++
                    return 'x'.code
                }

                override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
                    consumed += length
                    buffer.fill('x'.code.toByte(), offset, offset + length)
                    return length
                }
            }
        assertThrows(IllegalArgumentException::class.java) { source.readGuestRecord() }
        assertEquals(GUEST_RECORD_LIMIT + 1, consumed)
        val atLimit = ByteArray(GUEST_RECORD_LIMIT) { 'x'.code.toByte() }
        assertEquals(GUEST_RECORD_LIMIT, ByteArrayInputStream(atLimit).readGuestRecord().length)
        assertEquals(
            "{\"files\":[]}",
            ByteArrayInputStream("{\"files\":[]}".toByteArray()).readGuestRecord(),
        )
    }
}
