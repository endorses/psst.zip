package zip.psst.android.data

import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.model.FileMetadata
import org.junit.Assert.*
import org.junit.Test

class GuestDownloadPreflightTest {
    private val mib = 1024L * 1024

    private fun file(size: Long, digit: Char = 'a') =
        FileMetadata(
            "file-$digit.bin",
            size,
            blobId = "${digit.toString().repeat(8)}-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
            encoding = ChunkedFileCrypto.ENCODING,
            chunkSize = ChunkedFileCrypto.CHUNK_SIZE,
            encryptionId = digit.toString().repeat(32),
        )

    private fun inspect(files: List<FileMetadata>, saved: Set<String> = emptySet()) =
        GuestDownloadPreflight.inspect("https://remote.test", "transfer", files, saved, false)

    @Test
    fun smallTransfersAreAutomaticAndAggregateLargeTransfersRequireConsent() {
        val small = inspect(listOf(file(100 * mib)))
        assertFalse(GuestDownloadPreflight.needsConsent(small, null))
        val large = inspect(listOf(file(60 * mib), file(41 * mib, 'b')))
        assertTrue(GuestDownloadPreflight.needsConsent(large, null))
        assertFalse(GuestDownloadPreflight.needsConsent(large, large.copy()))
        assertEquals(101 * mib, large.totalBytes)
    }

    @Test
    fun consentIsBoundToOriginTransferAndFullAuthenticatedManifest() {
        val approved = inspect(listOf(file(101 * mib)))
        for (changed in
            listOf(
                approved.copy(origin = "https://another.test"),
                approved.copy(transferId = "different"),
                inspect(listOf(file(102 * mib))),
                inspect(listOf(file(101 * mib).copy(name = "different.apk"))),
                inspect(listOf(file(101 * mib).copy(encryptionId = "b".repeat(32)))),
            )) assertTrue(GuestDownloadPreflight.needsConsent(changed, approved))
    }

    @Test
    fun retriesAccountOnlyForMissingOutputsWithoutBypassingTotalConsent() {
        val first = file(80 * mib)
        val second = file(30 * mib, 'b')
        val retry = inspect(listOf(first, second), setOf(first.blobId))
        assertEquals(110 * mib, retry.totalBytes)
        assertEquals(30 * mib, retry.remainingBytes)
        assertTrue(GuestDownloadPreflight.needsConsent(retry, null))
        GuestDownloadPreflight.requireSpace(286 * mib, retry.remainingBytes)
        assertThrows(InsufficientDownloadSpaceException::class.java) {
            GuestDownloadPreflight.requireSpace(286 * mib - 1, retry.remainingBytes)
        }
    }

    @Test
    fun reserveChecksDoNotOverflowAndRejectUnavailableOrInsufficientStorage() {
        GuestDownloadPreflight.requireSpace(256 * mib, 0)
        GuestDownloadPreflight.requireSpace(Long.MAX_VALUE, Long.MAX_VALUE - 256 * mib)
        for ((available, remaining) in
            listOf(-1L to 0L, 255 * mib to 0L, Long.MAX_VALUE to Long.MAX_VALUE, 256 * mib to 1L)) {
            assertThrows(InsufficientDownloadSpaceException::class.java) {
                GuestDownloadPreflight.requireSpace(available, remaining)
            }
        }
    }

    @Test
    fun hostileManifestCannotReachStorageOrConsent() {
        for (files in
            listOf(
                emptyList(),
                listOf(file(-1)),
                listOf(file(Long.MAX_VALUE)),
                listOf(file(1), file(2)),
                listOf(file(1).copy(name = "../escape")),
                listOf(file(1).copy(chunkSize = Int.MAX_VALUE)),
            )) {
            assertThrows(IllegalArgumentException::class.java) { inspect(files) }
        }
    }

    @Test
    fun aggregateCapRejectsMultipleIndividuallyValidFiles() {
        val limit = GuestDownloadPreflight.MAX_TOTAL_BYTES
        assertEquals(limit, inspect(listOf(file(limit))).totalBytes)
        assertThrows(IllegalArgumentException::class.java) {
            inspect(listOf(file(limit), file(1, 'b')))
        }
    }
}
