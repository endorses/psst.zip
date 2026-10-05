package zip.psst.android.data

import zip.psst.shared.api.AuthResourceSlot
import org.junit.Assert.*
import org.junit.Test

class LinkHistoryPolicyTest {
    private val access = HistoryAccess("https://host", "alice")
    private val slot =
        AuthResourceSlot(
            "slot",
            "has_uploads",
            fileCount = 2,
            completedFiles = 1,
            maxFiles = 5,
            reservedFiles = 3,
        )

    @Test
    fun completedFilesAndCumulativeAllowancesRemainDistinctAcrossRefreshes() {
        val incoming = slotHistoryResource(slot, access)
        assertEquals(1, incoming.fileCount)
        assertEquals(
            "3 of 5 file allowances used · unfinished uploads also count",
            historyLinkPolicyLabel(incoming).english(),
        )
        val local =
            incoming.copy(
                encryptionKey = "local-key",
                title = "private name",
                savedFileIdsJson = "[\"saved\"]",
            )
        val stale = slotHistoryResource(slot.copy(reservedFiles = 1), access)
        val merged = mergeAccountResource(local, stale, access)!!
        assertEquals(3L, merged.reservedFiles)
        assertEquals(local.encryptionKey, merged.encryptionKey)
        assertEquals(local.title, merged.title)
        assertEquals(local.savedFileIdsJson, merged.savedFileIdsJson)
        val missingPolicy =
            mergeAccountResource(
                merged,
                stale.copy(maxFiles = null, reservedFiles = null),
                access,
            )!!
        assertEquals(5, missingPolicy.maxFiles)
        assertEquals(3L, missingPolicy.reservedFiles)
        assertEquals(3L, missingPolicy.metadata().reservedFiles)
    }

    @Test
    fun unknownAndExplicitUnlimitedPoliciesAreNotConfused() {
        val row = slotHistoryResource(slot, access)
        assertNull(historyLinkPolicyLabel(row.copy(maxFiles = null)).english())
        assertEquals(
            "No optional file-count limit · 3 file allowances used",
            historyLinkPolicyLabel(row.copy(maxFiles = 0)).english(),
        )
        val sent = row.copy(type = "sent", maxDownloads = 2)
        assertEquals("Limit: 2 download attempts per file", historyLinkPolicyLabel(sent).english())
        assertEquals(
            "No optional download limit",
            historyLinkPolicyLabel(sent.copy(maxDownloads = 0)).english(),
        )
        assertNull(historyLinkPolicyLabel(sent.copy(maxDownloads = null)).english())
        assertEquals(2, sent.metadata().maxDownloads)
    }

    @Test
    fun receiveUsageUsesTheTotalLimitForNativeSingularAndPluralSentences() {
        val row = slotHistoryResource(slot, access)
        val examples =
            listOf(
                Triple(
                    0L,
                    1,
                    listOf(
                        "0 of 1 file allowance used · unfinished uploads also count",
                        "0 von 1 Dateiplatz belegt · unvollständige Uploads zählen mit",
                    ),
                ),
                Triple(
                    1L,
                    1,
                    listOf(
                        "1 of 1 file allowance used · unfinished uploads also count",
                        "1 von 1 Dateiplatz belegt · unvollständige Uploads zählen mit",
                    ),
                ),
                Triple(
                    1L,
                    2,
                    listOf(
                        "1 of 2 file allowances used · unfinished uploads also count",
                        "1 von 2 Dateiplätzen belegt · unvollständige Uploads zählen mit",
                    ),
                ),
            )
        for ((used, limit, expected) in examples) {
            val caption = historyLinkPolicyLabel(row.copy(reservedFiles = used, maxFiles = limit))!!
            assertEquals(limit, caption.quantity)
            assertEquals(expected[0], caption.localized("en"))
            assertEquals(expected[1], caption.localized("de"))
        }
    }

    @Test
    fun downloadPolicyUsesNativeSingularAndPluralSentencesInBothLanguages() {
        val row = slotHistoryResource(slot, access).copy(type = "sent")
        val singular = historyLinkPolicyLabel(row.copy(maxDownloads = 1))!!
        val plural = historyLinkPolicyLabel(row.copy(maxDownloads = 2))!!
        assertEquals("Limit: 1 download attempt per file", singular.localized("en"))
        assertEquals("Limit: 2 download attempts per file", plural.localized("en"))
        assertEquals("Limit: 1 Download-Versuch pro Datei", singular.localized("de"))
        assertEquals("Limit: 2 Download-Versuche pro Datei", plural.localized("de"))
    }
}
