package zip.psst.android.data

import zip.psst.shared.api.AuthResourceSlot
import zip.psst.shared.model.InboxSummary
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Test

class AccountHistoryTest {
    private val access = HistoryAccess("https://host", "alice")

    private fun incoming() =
        TransferHistoryEntity(
            "resource",
            "received",
            2,
            0,
            "https://host",
            "",
            "has_uploads",
            accountId = "alice",
        )

    @Test
    fun compactInboxSummaryCountsHundredsOfChildrenWithoutEmptyArraysResettingHistory() {
        val slot =
            AuthResourceSlot(
                "large-inbox",
                "has_uploads",
                fileCount = 350,
                completedFiles = 250,
                summary = InboxSummary("ready", 250, 350, 0),
            )
        assertTrue(slot.transfers.isEmpty())
        val summary = slotHistoryResource(slot, access)
        assertEquals(250, summary.fileCount)
        val first = mergeAccountResource(null, summary, access)!!
        assertEquals(250, first.fileCount)
        assertEquals("has_uploads", first.status)
        val local =
            first.copy(
                encryptionKey = "private-local-marker",
                savedFileIdsJson =
                    Json.encodeToString((0 until 250).map { "child-$it/blob" }.toSet()),
                status = "complete",
            )
        val unchanged = mergeAccountResource(local, summary, access)!!
        assertEquals("has_uploads", unchanged.status)
        assertEquals(local.savedFileIdsJson, unchanged.savedFileIdsJson)
        assertEquals(local.encryptionKey, unchanged.encryptionKey)
        val newUpload =
            mergeAccountResource(
                local,
                slotHistoryResource(slot.copy(completedFiles = 251), access),
                access,
            )!!
        assertEquals("has_uploads", newUpload.status)
        assertEquals(251, newUpload.fileCount)
        assertEquals(local.savedFileIdsJson, newUpload.savedFileIdsJson)
    }

    @Test
    fun remoteEntryHasNoInventedKeyAndBelongsToTheSignedInAccount() {
        val merged = mergeAccountResource(null, incoming(), access)!!
        assertEquals("", merged.encryptionKey)
        assertNull(merged.deletionToken)
        assertEquals("alice", merged.accountId)
        assertEquals(2, merged.fileCount)
    }

    @Test
    fun serverMetadataNeverReplacesLocalSecretsTitlesOrCheckpoints() {
        val local =
            incoming()
                .copy(
                    encryptionKey = "local-secret",
                    deletionToken = "owner-secret",
                    title = "private.txt",
                    totalSize = 123,
                    receivedTransfersJson = "{\"child\":{\"fileCount\":2,\"plaintextSize\":123}}",
                    savedTransferIdsJson = "[\"child\"]",
                    savedFileIdsJson = "[\"child/blob\"]",
                )
        val merged = mergeAccountResource(local, incoming(), access)!!
        assertEquals(local.encryptionKey, merged.encryptionKey)
        assertEquals(local.deletionToken, merged.deletionToken)
        assertEquals(local.title, merged.title)
        assertEquals(local.savedFileIdsJson, merged.savedFileIdsJson)
        assertEquals(local.savedTransferIdsJson, merged.savedTransferIdsJson)
        assertEquals(123L, merged.totalSize)
        assertEquals("has_uploads", merged.status)
    }

    @Test
    fun matchingIdNeverAdoptsLegacyOrAnotherAccountsLocalKeys() {
        assertNull(
            mergeAccountResource(
                incoming().copy(accountId = null, encryptionKey = "legacy"),
                incoming(),
                access,
            )
        )
        assertNull(
            mergeAccountResource(
                incoming().copy(accountId = "bob", encryptionKey = "other"),
                incoming(),
                access,
            )
        )
        assertNull(
            mergeAccountResource(
                incoming().copy(serverUrl = "https://other", encryptionKey = "other"),
                incoming(),
                access,
            )
        )
    }

    @Test
    fun readyCountsReplacePriorCountsAndUpdatingPreservesLocalFacts() {
        val local =
            incoming()
                .copy(
                    fileCount = 100,
                    encryptionKey = "key",
                    savedFileIdsJson = "[\"child/blob\"]",
                    title = "mine",
                )
        val ready = mergeAccountResource(local, incoming().copy(fileCount = 1), access)!!
        assertEquals(1, ready.fileCount)
        val updating =
            mergeAccountResource(
                ready,
                incoming().copy(fileCount = 0, summaryUpdating = true),
                access,
            )!!
        assertEquals(1, updating.fileCount)
        assertTrue(updating.summaryUpdating)
        assertEquals(local.encryptionKey, updating.encryptionKey)
        assertEquals(local.savedFileIdsJson, updating.savedFileIdsJson)
        assertEquals(local.title, updating.title)
    }
}
