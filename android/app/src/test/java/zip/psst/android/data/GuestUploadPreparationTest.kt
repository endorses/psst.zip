package zip.psst.android.data

import zip.psst.shared.model.SlotAvailability
import zip.psst.shared.model.UploadCapacity
import java.io.ByteArrayInputStream
import kotlin.time.Clock
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

class GuestUploadPreparationTest {
    private fun source(size: Int, name: String = "file") =
        GuestUploadSource(name, "application/octet-stream") {
            ByteArrayInputStream(ByteArray(size))
        }

    private fun policy(bytes: Long = 1000) =
        SlotAvailability(
            "slot",
            true,
            2,
            "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            remainingBytes = 1000,
            remainingTransfers = 1,
            uploadCapacity =
                UploadCapacity(
                    Clock.System.now().toString(),
                    "ready",
                    availableWireBytes = bytes,
                    availableFiles = 1,
                    manifestReserveBytes = 1048576,
                ),
        )

    private fun directory() =
        java.nio.file.Files.createTempDirectory("guest-preflight-test").toFile()

    @Test
    fun pickerActionsAccumulateAndDoNotDropPriorSelection() {
        val first = appendGuestSelection(emptyList(), listOf("a", "b"))
        assertEquals(listOf("a", "b", "c"), appendGuestSelection(first, listOf("b", "c")))
        try {
            appendGuestSelection(List(100) { it.toString() }, listOf("overflow"))
            fail("accepted101")
        } catch (_: IllegalArgumentException) {}
        assertEquals(listOf("a", "b"), first)
    }

    @Test
    fun unknownProviderLengthsAreReadOnceAndActualWireTotalIncludesEmptyFiles() = runTest {
        val dir = directory()
        var opened = 0
        try {
            val prepared =
                prepareGuestUpload(
                    listOf(
                        source(0),
                        GuestUploadSource("unknown", "x") {
                            opened++
                            ByteArrayInputStream(ByteArray(7))
                        },
                    ),
                    dir,
                    1000,
                ) {
                    UPLOAD_DISK_RESERVE + 1000
                }
            assertEquals(127, prepared.totalWireBytes)
            assertEquals(1, opened)
            assertEquals(2, dir.listFiles()!!.size)
            prepared.close()
            assertEquals(0, dir.listFiles()!!.size)
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun aggregateDiskBoundCleansAllSnapshots() = runTest {
        for (maxBytes in listOf(1000L, 4L)) {
            val dir = directory()
            try {
                try {
                    prepareGuestUpload(listOf(source(3), source(5)), dir, maxBytes) {
                        UPLOAD_DISK_RESERVE + 6
                    }
                    fail("budget bypass")
                } catch (_: IllegalArgumentException) {}
                assertEquals(0, dir.listFiles()!!.size)
            } finally {
                dir.deleteRecursively()
            }
        }
    }

    @Test
    fun providerFailureCleansAlreadyPreparedFiles() = runTest {
        val dir = directory()
        try {
            try {
                prepareGuestUpload(
                    listOf(
                        source(3),
                        GuestUploadSource("broken", "x") {
                            throw IllegalStateException("provider gone")
                        },
                    ),
                    dir,
                    1000,
                ) {
                    UPLOAD_DISK_RESERVE + 1000
                }
                fail("failure ignored")
            } catch (_: IllegalStateException) {}
            assertEquals(0, dir.listFiles()!!.size)
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun changedServerCapacityOrFileLimitBlocksBeforeAllocation() = runTest {
        val dir = directory()
        try {
            prepareGuestUpload(listOf(source(40)), dir, 1000) { UPLOAD_DISK_RESERVE + 1000 }
                .use { prepared ->
                    var refreshes = 0
                    var allocated = false
                    try {
                        validatePreparedGuestUpload(prepared, "slot", ByteArray(32)) {
                            refreshes++
                            policy(60) to 1000L
                        }
                        allocated = true
                        fail("stale advisory authorized allocation")
                    } catch (_: IllegalArgumentException) {}
                    assertEquals(1, refreshes)
                    assertFalse(allocated)
                    assertEquals(1, prepared.files.size)
                    try {
                        validatePreparedGuestUpload(prepared, "slot", ByteArray(32)) {
                            policy() to 39L
                        }
                        fail("new file limit bypass")
                    } catch (_: IllegalArgumentException) {}
                    try {
                        validatePreparedGuestUpload(prepared, "slot", ByteArray(32)) {
                            policy().copy(uploadCapacity = null) to 1000L
                        }
                        fail("missing capacity bypass")
                    } catch (_: IllegalArgumentException) {}
                    validatePreparedGuestUpload(prepared, "slot", ByteArray(32)) {
                        policy() to 1000L
                    }
                    assertEquals(100, prepared.totalWireBytes)
                }
            assertEquals(0, dir.listFiles()!!.size)
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun emptyFilesStillRequireLocalReserveAndCancellationCleansSnapshots() = runTest {
        val dir = directory()
        try {
            try {
                prepareGuestUpload(listOf(source(0)), dir, 1000) { 0 }
                fail("missing disk reserve")
            } catch (_: IllegalArgumentException) {}
            try {
                prepareGuestUpload(
                    listOf(
                        source(1),
                        GuestUploadSource("cancel", "x") {
                            throw kotlinx.coroutines.CancellationException("cancelled")
                        },
                    ),
                    dir,
                    1000,
                ) {
                    UPLOAD_DISK_RESERVE + 1000
                }
                fail("cancellation ignored")
            } catch (_: kotlinx.coroutines.CancellationException) {}
            assertEquals(0, dir.listFiles()!!.size)
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun manifestReserveIsCheckedBeforeAllocation() = runTest {
        val dir = directory()
        try {
            prepareGuestUpload(listOf(source(0)), dir, 1000) { UPLOAD_DISK_RESERVE + 1000 }
                .use { prepared ->
                    val small =
                        policy()
                            .copy(
                                uploadCapacity =
                                    policy().uploadCapacity!!.copy(manifestReserveBytes = 116)
                            )
                    try {
                        validatePreparedGuestUpload(prepared, "slot", ByteArray(32)) {
                            small to 1000L
                        }
                        fail("manifest allowance bypass")
                    } catch (_: IllegalArgumentException) {}
                }
            assertEquals(0, dir.listFiles()!!.size)
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun plaintextPerFileBoundaryAllowsEncryptionOverheadAndEmptyFiles() = runTest {
        for (limit in listOf(1, 64, 1024)) {
            val dir = directory()
            try {
                prepareGuestUpload(listOf(source(limit), source(0)), dir, limit.toLong()) {
                        UPLOAD_DISK_RESERVE + 10000
                    }
                    .use { prepared ->
                        assertEquals(limit + 120L, prepared.totalWireBytes)
                        val ready =
                            policy(10000)
                                .copy(
                                    remainingBytes = 10000,
                                    uploadCapacity =
                                        policy(10000).uploadCapacity!!.copy(availableFiles = 2),
                                )
                        validatePreparedGuestUpload(prepared, "slot", ByteArray(32)) {
                            ready to limit.toLong()
                        }
                    }
                try {
                    prepareGuestUpload(listOf(source(limit + 1)), dir, limit.toLong()) {
                        UPLOAD_DISK_RESERVE + 10000
                    }
                    fail("plaintext limit bypass")
                } catch (_: IllegalArgumentException) {}
                assertEquals(0, dir.listFiles()!!.size)
            } finally {
                dir.deleteRecursively()
            }
        }
    }
}
