package zip.psst.android.data

import zip.psst.shared.api.AuthResourceSlot
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
            AuthResourceSlot("large-inbox", "has_uploads", fileCount = 350, completedFiles = 250)
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
    fun absenceVerificationSharesTheManifestBudgetAndDoesNotMutateStatus() {
        val missingSent = incoming().copy(id = "omitted", type = "sent", status = "complete")
        val remoteSent = missingSent.copy(id = "present")
        val localReceives =
            (0 until 30).map { incoming().copy(id = "receive-$it", encryptionKey = "local-key") }
        val remoteIds = localReceives.map { it.id }.toSet() + remoteSent.id
        val rows = listOf(missingSent, remoteSent) + localReceives
        val first = historyRefreshBatch(rows, 0, remoteIds)
        val second = historyRefreshBatch(rows, 20, remoteIds)
        assertEquals(20, first.size)
        assertEquals(20, second.size)
        assertTrue(first.any { it.id == missingSent.id })
        assertFalse((first + second).any { it.id == remoteSent.id })
        assertEquals("complete", missingSent.status)
        assertEquals(31, (first + second).map { it.id }.toSet().size)
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
        val merged =
            mergeAccountResource(
                local,
                incoming(),
                access,
                ReceivedSnapshot(mapOf("child" to ReceivedChild(2))),
            )!!
        assertEquals(local.encryptionKey, merged.encryptionKey)
        assertEquals(local.deletionToken, merged.deletionToken)
        assertEquals(local.title, merged.title)
        assertEquals(local.savedFileIdsJson, merged.savedFileIdsJson)
        assertEquals(local.savedTransferIdsJson, merged.savedTransferIdsJson)
        assertEquals(123L, merged.totalSize)
        assertEquals("complete", merged.status)
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
    fun manifestRefreshIsBoundedRotatesAndSkipsMissingKeys() {
        val rows =
            (0 until 45).map { incoming().copy(id = "row-$it", encryptionKey = "key") } + incoming()
        val first = historyManifestBatch(rows, 0)
        val next = historyManifestBatch(rows, 20)
        val wrap = historyManifestBatch(rows, 40)
        assertEquals(20, first.size)
        assertEquals(20, next.size)
        assertEquals(20, wrap.size)
        assertEquals(45, (first + next + wrap).map { it.id }.toSet().size)
        assertFalse((first + next + wrap).any { it.encryptionKey.isBlank() })
    }
}
