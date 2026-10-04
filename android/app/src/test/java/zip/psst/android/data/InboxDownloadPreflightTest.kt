package zip.psst.android.data

import zip.psst.shared.model.FileMetadata
import org.junit.Assert.*
import org.junit.Test

class InboxDownloadPreflightTest {
    private val mib = 1024L * 1024

    private fun inspect(
        size: Long,
        fingerprint: String = "first",
        saved: Set<String> = emptySet(),
        account: String = "alice",
    ) =
        InboxDownloadPreflight.inspect(
            "https://host.test",
            account,
            "slot",
            listOf("child" to FileMetadata("large.bin", size, blobId = "blob")),
            saved,
            mapOf("child" to fingerprint),
        )

    @Test
    fun aggregateAcrossChildrenRequiresConsentAndDoesNotForgetAlreadySavedFiles() {
        val files =
            listOf(
                "a" to FileMetadata("one.bin", 60 * mib, blobId = "x"),
                "b" to FileMetadata("two.bin", 60 * mib, blobId = "y"),
            )
        val current =
            InboxDownloadPreflight.inspect(
                "https://host.test",
                "alice",
                "slot",
                files,
                emptySet(),
                mapOf("a" to "a-fingerprint", "b" to "b-fingerprint"),
            )
        assertTrue(InboxDownloadPreflight.needsConsent(current, null))
        assertEquals(120 * mib, current.remainingBytes)
        val retry =
            InboxDownloadPreflight.inspect(
                current.origin,
                current.accountId,
                current.slotId,
                files,
                setOf("a/x"),
                current.manifestFingerprints,
            )
        assertEquals(60 * mib, retry.remainingBytes)
        assertEquals(1, retry.fileCount)
        assertFalse(InboxDownloadPreflight.needsConsent(retry, null))
    }

    @Test
    fun approvalBindsEncryptedManifestsAndOwnerSoMutationMustAskAgain() {
        val original = inspect(101 * mib)
        assertFalse(InboxDownloadPreflight.needsConsent(original, original))
        assertTrue(InboxDownloadPreflight.needsConsent(inspect(101 * mib, "changed"), original))
        assertTrue(
            InboxDownloadPreflight.needsConsent(inspect(101 * mib, account = "bob"), original)
        )
        assertFalse(InboxDownloadPreflight.needsConsent(inspect(100 * mib), null))
        val saved = inspect(101 * mib, saved = setOf("child/blob"))
        assertEquals(0L, saved.remainingBytes)
    }

    @Test
    fun aggregateRejectsOverflowAndEnforcesFreeSpaceReserveBeforeDownloads() {
        for (size in listOf(-1L, Long.MAX_VALUE, GuestDownloadPreflight.MAX_TOTAL_BYTES + 1)) {
            try {
                inspect(size)
                fail("Rejected size was accepted")
            } catch (_: IllegalArgumentException) {}
        }
        val current = inspect(101 * mib)
        try {
            GuestDownloadPreflight.requireSpace(
                current.remainingBytes + GuestDownloadPreflight.FREE_SPACE_RESERVE - 1,
                current.remainingBytes,
            )
            fail("Reserve was not enforced")
        } catch (_: InsufficientDownloadSpaceException) {}
        GuestDownloadPreflight.requireSpace(
            current.remainingBytes + GuestDownloadPreflight.FREE_SPACE_RESERVE,
            current.remainingBytes,
        )
    }
}
