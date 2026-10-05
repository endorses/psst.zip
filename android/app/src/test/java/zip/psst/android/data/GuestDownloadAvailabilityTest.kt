package zip.psst.android.data

import zip.psst.shared.model.FileMetadata
import org.junit.Assert.*
import org.junit.Test

class GuestDownloadAvailabilityTest {
    private val first = "11111111-1111-4111-8111-111111111111"
    private val second = "22222222-2222-4222-8222-222222222222"

    private fun file(id: String) = FileMetadata("file", 1, blobId = id)

    private fun saved(id: String) =
        SavedGuestFile(id, "content://saved/$id", "file", 1, "text/plain")

    private val row =
        GuestDownload(
            "local",
            "https://host",
            "transfer",
            files = listOf(file(first), file(second)),
        )

    @Test
    fun allMissingExhaustedDisablesServerActionsButLocalCopiesRemainUsable() {
        val attempts = mapOf(first to 0L, second to 0L)
        val local = row.copy(saved = listOf(saved(first)))
        val availability = guestDownloadAvailability(local, attempts) { true }
        assertEquals(1, availability.missing)
        assertTrue(availability.allMissingExhausted)
        assertEquals(1, local.saved.size)
        // Missing physical output must be fetched again and its exhausted cap still applies.
        assertEquals(2, guestDownloadAvailability(local, attempts) { false }.missing)
        val completeLocal = local.copy(saved = listOf(saved(first), saved(second)))
        val allSaved = guestDownloadAvailability(completeLocal, attempts) { true }
        assertEquals(0, allSaved.missing)
        assertFalse(allSaved.allMissingExhausted)
    }

    @Test
    fun partialAvailabilityIsExplicitAndUnknownRefreshDoesNotMeanExhausted() {
        val partial = guestDownloadAvailability(row, mapOf(first to 0L, second to 1L)) { true }
        assertTrue(partial.partiallyExhausted)
        assertFalse(partial.allMissingExhausted)
        val unknown = guestDownloadAvailability(row, mapOf(first to 0L, second to null)) { true }
        assertFalse(unknown.allMissingExhausted)
        assertEquals(1, unknown.unknown)
        val unlimited = guestDownloadAvailability(row, emptyMap()) { true }
        assertFalse(unlimited.allMissingExhausted)
        assertFalse(unlimited.partiallyExhausted)
    }
}
