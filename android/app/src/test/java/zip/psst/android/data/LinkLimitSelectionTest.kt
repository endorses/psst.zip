package zip.psst.android.data

import org.junit.Assert.*
import org.junit.Test

class LinkLimitSelectionTest {
    @Test
    fun anEnabledLimitNeverSilentlyFallsBackToUnlimited() {
        for (value in
            listOf(
                "",
                " ",
                "0",
                "-1",
                "1.5",
                "+1",
                "2147483648",
                "999999999999",
                "21474836470",
                "00000000001",
            )) {
            assertNotNull("Expected inline validation for '$value'", linkLimitError(true, value))
            try {
                selectedLinkLimit(true, value)
                fail("Enabled invalid limit was accepted: '$value'")
            } catch (_: IllegalArgumentException) {}
        }
        assertEquals(1, selectedLinkLimit(true, "1"))
        assertEquals(Int.MAX_VALUE, selectedLinkLimit(true, "2147483647"))
        assertNull(linkLimitError(true, "12"))
    }

    @Test
    fun disablingTheLimitIsAnExplicitUnlimitedChoice() {
        for (value in listOf("", "12", "invalid", "2147483648")) {
            assertEquals(0, selectedLinkLimit(false, value))
            assertNull(linkLimitError(false, value))
        }
    }
}
