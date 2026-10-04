package zip.psst.shared.model

import zip.psst.shared.crypto.ChunkedFileCrypto
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertFalse
import kotlin.time.Clock
import kotlin.time.Duration.Companion.minutes
import kotlinx.serialization.json.Json

class UploadCapacityTest {
    private val key = ByteArray(32)

    private fun capacity(bytes: Long = 10000, files: Long = 100) =
        UploadCapacity(
            Clock.System.now().toString(),
            "ready",
            availableWireBytes = bytes,
            availableFiles = files,
            manifestReserveBytes = 1048576,
        )

    private fun policy(capacity: UploadCapacity? = capacity()) =
        SlotAvailability(
            "slot",
            true,
            2,
            "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            remainingBytes = 10000,
            remainingTransfers = 2,
            uploadCapacity = capacity,
        )

    @Test
    fun accumulatedWireBytesIncludeEveryEmptyFileAndChunkBoundary() {
        assertEquals(120, GuestUploadCapacity.totalWireBytes(listOf(0, 0)))
        assertEquals(
            ChunkedFileCrypto.CHUNK_SIZE.toLong() + 121,
            GuestUploadCapacity.totalWireBytes(listOf(ChunkedFileCrypto.CHUNK_SIZE.toLong() + 1)),
        )
        assertEquals(
            ChunkedFileCrypto.CHUNK_SIZE.toLong() + 120,
            GuestUploadCapacity.totalWireBytes(listOf(0, ChunkedFileCrypto.CHUNK_SIZE.toLong())),
        )
        assertFails { GuestUploadCapacity.totalWireBytes(listOf(Long.MAX_VALUE, 1)) }
        assertFails { GuestUploadCapacity.totalWireBytes(listOf(-1)) }
        assertFails { GuestUploadCapacity.totalWireBytes(List(101) { 0 }) }
    }

    @Test
    fun aggregatesAreCheckedAgainstCapacityAndCumulativeAllowances() {
        policy(capacity(120, 2)).validateForSubmission("slot", key, 2, 120)
        assertFails { policy(capacity(120, 2)).validateForSubmission("slot", key, 2, 121) }
        assertFails { policy(capacity(10000, 1)).validateForSubmission("slot", key, 2, 120) }
        assertFails {
            policy().copy(remainingBytes = 100).validateForSubmission("slot", key, 2, 120)
        }
        assertFails {
            policy()
                .copy(maxFiles = 4, remainingFiles = 1)
                .validateForSubmission("slot", key, 2, 120)
        }
        assertFails {
            policy().copy(remainingTransfers = 0).validateForSubmission("slot", key, 1, 60)
        }
        assertFails { policy().validateForSubmission("other", key, 1, 60) }
        assertFails { policy().validateForSubmission("slot", ByteArray(32) { 1 }, 1, 60) }
        assertFails { policy().validateForSubmission("slot", key, 1, 0) }
        assertFails { policy().validateForSubmission("slot", key, 0, 0) }
    }

    @Test
    fun capacityJsonRejectsQuotedMissingFractionalAndOversizedNumbers() {
        val template =
            """{"state":"ready","checked_at":"${Clock.System.now()}","available_wire_bytes":BYTES,"available_files":1,"manifest_reserve_bytes":1}"""
        for (value in listOf("\"1000\"", "1.5", "9223372036854775808", "null", "-1")) {
            assertFails { Json.decodeFromString<UploadCapacity>(template.replace("BYTES", value)) }
        }
        val valid = template.replace("BYTES", "1000")
        assertEquals(1000, Json.decodeFromString<UploadCapacity>(valid).availableWireBytes)
        assertFails {
            Json.decodeFromString<UploadCapacity>(valid.replace("\"available_files\":1,", ""))
        }
    }

    @Test
    fun unknownMissingMalformedAndStaleNeverBecomeUnlimited() {
        assertFails { policy(null).validateForSubmission("slot", key, 1, 60) }
        val unknown =
            capacity()
                .copy(
                    state = "unknown",
                    reason = "capacity_unavailable",
                    availableFiles = null,
                    availableWireBytes = null,
                )
        unknown.validate()
        assertFails { policy(unknown).validateForSubmission("slot", key, 1, 60) }
        val blocked =
            capacity()
                .copy(
                    state = "blocked",
                    reason = "capacity_limit",
                    availableFiles = 0,
                    availableWireBytes = 0,
                )
        blocked.validate()
        assertFails { policy(blocked).validateForSubmission("slot", key, 1, 60) }
        for (invalid in
            listOf(
                capacity().copy(checkedAt = "garbage"),
                capacity().copy(checkedAt = "2026-10-04T00:00:00+00:00"),
                capacity().copy(availableWireBytes = null),
                capacity().copy(availableWireBytes = -1),
                capacity().copy(availableFiles = 101),
                capacity().copy(manifestReserveBytes = 1048577),
                capacity().copy(manifestReserveBytes = 0),
                capacity().copy(state = "unexpected"),
                capacity().copy(reason = "capacity_limit"),
                unknown.copy(availableFiles = 0),
                blocked.copy(availableFiles = null),
            )) {
            assertFails { invalid.validate() }
        }
        val stale = capacity().copy(checkedAt = (Clock.System.now() - 3.minutes).toString())
        assertFalse(stale.isFresh())
        assertFails { policy(stale).validateForSubmission("slot", key, 1, 60) }
        assertFails {
            policy().copy(maxFiles = 2, remainingFiles = null).validateInvitation("slot", key)
        }
        assertFails {
            Json.decodeFromString<UploadCapacity>(
                """{"state":"ready","checked_at":"2026-10-04T00:00:00Z","available_wire_bytes":1.5,"available_files":1,"manifest_reserve_bytes":1}"""
            )
        }
    }
}
