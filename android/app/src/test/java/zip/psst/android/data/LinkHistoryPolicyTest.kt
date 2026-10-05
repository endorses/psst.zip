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
            historyLinkPolicyLabel(incoming),
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
        assertNull(historyLinkPolicyLabel(row.copy(maxFiles = null)))
        assertEquals(
            "No optional file-count limit · 3 file allowances used",
            historyLinkPolicyLabel(row.copy(maxFiles = 0)),
        )
        val sent = row.copy(type = "sent", maxDownloads = 2)
        assertEquals("Limit: 2 download attempts per file", historyLinkPolicyLabel(sent))
        assertEquals(
            "No optional download limit",
            historyLinkPolicyLabel(sent.copy(maxDownloads = 0)),
        )
        assertNull(historyLinkPolicyLabel(sent.copy(maxDownloads = null)))
        assertEquals(2, sent.metadata().maxDownloads)
    }
}
