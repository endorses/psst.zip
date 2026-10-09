package zip.psst.android.viewmodel

import kotlin.io.encoding.Base64
import org.junit.Assert.*
import org.junit.Test
import zip.psst.android.data.*

class ReceiveEntryTest {
    private val key =
        Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)
            .encode(ByteArray(32) { it.toByte() })
    private val access = HistoryAccess("https://self-hosted.example", "alice")

    private fun row() =
        TransferHistoryEntity(
            "original-slot",
            "received",
            0,
            0,
            access.serverUrl,
            key,
            "waiting",
            accountId = "alice",
        )

    @Test
    fun historyReopensTheOriginalSlotKeyAndSavedState() {
        val saved =
            row()
                .copy(
                    status = "complete",
                    fileCount = 2,
                    totalSize = 123,
                    checkpointSavedFiles = 2,
                    checkpointKnownFiles = 2,
                    checkpointKnownBytes = 123,
                )
        val restored = restoreReceiveEntry(saved, access)
        assertEquals("original-slot", restored.slotId)
        assertEquals(key, restored.encryptionKey)
        assertNull(restored.uploadUrl)
        assertTrue(restored.legacyReadOnly)
        assertEquals(2, restored.savedFileCount)
        assertTrue(restored.downloadComplete)
    }

    @Test
    fun failedSaveRetriesOriginalDownloadNeverCreatesAnotherLink() {
        val restored = restoreReceiveEntry(row(), access)
        val failed =
            restored.copy(
                error = zip.psst.android.i18n.userText("Download failed"),
                isDownloading = false,
            )
        assertEquals(ReceiveRetry.SAVE, failed.retryAction("original-slot"))
        assertEquals(restored.slotId, failed.slotId)
        assertEquals(restored.encryptionKey, failed.encryptionKey)
        val storageDenied = restored.copy(savedFileCount = 1).storagePermissionDenied()
        assertEquals(ReceiveRetry.SAVE, storageDenied.retryAction("original-slot"))
        assertEquals(restored.slotId, storageDenied.slotId)
        assertEquals(restored.encryptionKey, storageDenied.encryptionKey)
        assertEquals(1, storageDenied.savedFileCount)
        assertNotNull(storageDenied.error)
        assertFalse(storageDenied.isDownloading)
        assertEquals(
            ReceiveRetry.REOPEN,
            ReceiveUiState(error = zip.psst.android.i18n.userText("Offline"))
                .retryAction("original-slot"),
        )
        assertEquals(
            ReceiveRetry.CREATE,
            ReceiveUiState(error = zip.psst.android.i18n.userText("Creation failed"))
                .retryAction(null),
        )
    }

    @Test
    fun missingPrivateKeyKeepsOwnerSlotIdentityForPolicyPollingWithoutExposingSaveLink() {
        val modern =
            row().copy(id = "11111111-1111-4111-8111-111111111111", encryptionKey = "v2.$key")
        val missing = restoreReceiveEntry(modern, access, privateKeyAvailable = false)
        assertEquals(modern.id, missing.slotId)
        assertTrue(missing.keyUnavailable)
        assertNull(missing.uploadUrl)
        val synced = restoreReceiveEntry(modern.copy(encryptionKey = ""), access)
        assertEquals(modern.id, synced.slotId)
        assertTrue(synced.keyUnavailable)
    }

    @Test
    fun anotherAccountCannotRestoreTheLocalKey() {
        try {
            restoreReceiveEntry(row(), access.copy(accountId = "bob"))
            fail("Other account was allowed to reopen the link")
        } catch (_: IllegalArgumentException) {}
    }
}
