package zip.psst.android.data

import zip.psst.shared.model.Transfer
import zip.psst.shared.model.TransferStatus
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Test

class MergedHistoryPageTest {
    private fun owned(id: String, time: Long) =
        TransferHistoryEntity(
            id,
            "sent",
            1,
            1,
            "https://host",
            "key",
            "complete",
            time,
            accountId = "owner",
        )

    private fun downloaded(id: String, time: Long) = GuestDownload(id, "https://host", id, time)

    @Test
    fun interleavedSourcesAndEqualTimesTraverseExactlyOnceWithBoundedWindows() {
        val accounts =
            (0..122)
                .map { owned("%04d".format(it), (it / 3).toLong()) }
                .sortedWith(
                    compareByDescending<TransferHistoryEntity> { it.createdAt }
                        .thenByDescending { it.id }
                )
        val downloads =
            (0..138)
                .map { downloaded("%04d".format(it), (it / 4).toLong()) }
                .sortedWith(
                    compareByDescending<GuestDownload> { it.createdAt }
                        .thenByDescending { it.identity }
                )
        var cursor = MergedHistoryCursor()
        val seen = mutableSetOf<String>()
        do {
            val account =
                accounts
                    .filter {
                        cursor.account == null ||
                            it.createdAt < cursor.account!!.createdAt ||
                            (it.createdAt == cursor.account!!.createdAt &&
                                it.id < cursor.account!!.id)
                    }
                    .take(51)
            val guest =
                downloads
                    .filter {
                        cursor.downloads == null ||
                            it.createdAt < cursor.downloads!!.createdAt ||
                            (it.createdAt == cursor.downloads!!.createdAt &&
                                it.identity < cursor.downloads!!.id)
                    }
                    .take(51)
            val page =
                mergeHistoryPage(
                    account,
                    guest.take(50),
                    cursor,
                    account.size > 50,
                    guest.size > 50,
                )
            assertTrue(page.rows.size <= 50)
            page.rows.forEach { assertTrue(seen.add(it.key)) }
            if (page.next == null) break
            assertNotEquals(cursor, page.next)
            cursor = page.next!!
        } while (true)
        assertEquals(accounts.size + downloads.size, seen.size)
    }

    @Test
    fun emptyAndSinglePagesHaveNoNavigationAndCorruptDownloadPagesAdvance() {
        assertNull(mergeHistoryPage(emptyList(), emptyList()).next)
        assertNull(mergeHistoryPage(listOf(owned("one", 1)), listOf(downloaded("two", 2))).next)
        val rawAnchor = LocalHistoryCursor(3, "damaged-record")
        assertEquals(
            rawAnchor,
            mergeHistoryPage(
                    emptyList(),
                    emptyList(),
                    downloadsHaveMore = true,
                    downloadsAfterPage = rawAnchor,
                )
                .next
                ?.downloads,
        )
    }

    @Test
    fun sharedTitlesOverrideLocalLabelsAndSnapshotSurvivesOfflineDecoding() {
        val row = owned("one", 1).copy(title = "Old local label")
        assertEquals(
            "Shared Unicode 🐈",
            historyTitle(row.copy(sharedTitle = "Shared Unicode 🐈")).english(),
        )
        assertEquals("Old local label", historyTitle(row).english())
        val snapshot = downloaded("two", 2).copy(sharedTitle = "Shared Unicode 🐈")
        assertEquals(
            snapshot.sharedTitle,
            Json.decodeFromString<GuestDownload>(Json.encodeToString(snapshot)).sharedTitle,
        )
        assertNull(
            Json.decodeFromString<GuestDownload>(
                    "{\"identity\":\"old\",\"origin\":\"https://host\",\"transferId\":\"old\"}"
                )
                .sharedTitle
        )
    }

    @Test
    fun exhaustionOverridesPriorStartedOrDeliveredStateWithoutClaimingSave() {
        val exhausted = Transfer("one", status = TransferStatus.EXHAUSTED)
        assertEquals(
            "exhausted",
            mergeSentHistory(owned("one", 1).copy(status = "downloaded"), exhausted).status,
        )
        assertEquals("Download limit reached", historyStatusLabel("sent", "exhausted").english())
    }
}
