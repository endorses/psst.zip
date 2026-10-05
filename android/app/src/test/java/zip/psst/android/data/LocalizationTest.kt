package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.api.ClientFailureException
import java.util.Locale
import org.junit.Assert.*
import org.junit.Test

class LocalizationTest {
    @Test
    fun transferProgressFormatsBothSizesWithoutChangingByteCounts() {
        val transferred = 16_777_456L
        val total = 24_428_863L
        val caption = transferSizeProgress(transferred, total)
        assertEquals("16 MiB / 23.3 MiB", caption.english())
        assertEquals("16 MiB / 23,3 MiB", caption.localized("de"))
        assertEquals(listOf(UiByteCount(transferred), UiByteCount(total)), caption.arguments)
        assertEquals("1 KiB / 2 KiB", transferSizeProgress(1024, 2048).english())
        assertEquals("16 MiB / ?", transferSizeProgress(transferred, null).localized("de"))
    }

    @Test
    fun systemSelectsFirstSupportedRegionalLanguageAndFallsBack() {
        assertEquals(
            "de",
            AppLanguage.resolve(AppLanguage.SYSTEM, listOf("fr-FR", "de-CH", "en-US")),
        )
        assertEquals("en", AppLanguage.resolve(AppLanguage.SYSTEM, listOf("en-GB", "de-DE")))
        assertEquals("en", AppLanguage.resolve(AppLanguage.SYSTEM, listOf("ja-JP")))
        assertEquals("de", AppLanguage.resolve(AppLanguage.GERMAN, listOf("en-US")))
        assertEquals("en", AppLanguage.resolve(AppLanguage.ENGLISH, listOf("de-AT")))
        assertEquals(AppLanguage.GERMAN, AppLanguage.fromTag("de-AT"))
        assertEquals(AppLanguage.SYSTEM, AppLanguage.fromTag(null))
    }

    @Test
    fun numericAndBytePresentationDoesNotChangeInputSemantics() {
        assertEquals("1 MiB", UiFormatting.bytes(1024L * 1024, Locale.GERMAN))
        assertEquals("1,5 MiB", UiFormatting.bytes(1536L * 1024, Locale.GERMAN))
        assertEquals("1.5 MiB", UiFormatting.bytes(1536L * 1024, Locale.ENGLISH))
        assertEquals("1.234", UiFormatting.number(1234, Locale.GERMAN))
        assertEquals("1,234", UiFormatting.number(1234, Locale.ENGLISH))
        assertEquals(1234, optionalLinkLimit("1234"))
        assertThrows(IllegalArgumentException::class.java) { optionalLinkLimit("1.234") }
        assertThrows(IllegalArgumentException::class.java) { optionalLinkLimit("1,234") }
    }

    @Test
    fun unknownFailuresCannotExposeServerProseAndKnownFailuresStaySemantic() {
        val hostile = IllegalArgumentException("https://private.invalid/u/id#SECRET <html>")
        val unknown = failureText(hostile)
        assertEquals(R.string.failure_unknown, unknown.resource)
        assertTrue(unknown.arguments.isEmpty())
        val known = failureText(ClientFailureException("hostile ignored", "invalid_credentials"))
        assertEquals(R.string.failure_invalid_credentials, known.resource)
        assertEquals(
            R.string.failure_resource_revoked,
            failureText(ClientFailureException("ignored", "resource_revoked")).resource,
        )
        val pending = SendUiStateForTest.messageState(known)
        assertSame(known, pending)
    }

    @Test
    fun captionsNeverDriveScanStagesOrRecordIdentity() {
        assertNotEquals(ScanStage.DOWNLOADING, ScanStage.UPLOADING)
        assertEquals(
            ScanStage.UNAVAILABLE,
            ScanStage.forFailure(
                ClientFailureException("English diagnostics may change", "resource_revoked")
            ),
        )
        assertEquals(
            ScanStage.BUDGET,
            ScanStage.forFailure(ClientFailureException("ignored", "traffic_budget_exhausted")),
        )
        val title = automaticHistoryTitle("Meine Fotos 🐈.jpg", 3)!!
        assertEquals(2, title.quantity)
        assertEquals("Meine Fotos 🐈.jpg", (title.arguments[0] as UiText).literal)
        assertEquals(2, title.arguments[1])
        assertEquals("Meine Fotos 🐈.jpg + 2 files", title.english())
        for (count in listOf(0L, 1L, 2L)) {
            val message = pluralMessage(R.plurals.files_saved_short, count, count)
            assertEquals(count.toInt(), message.quantity)
            assertEquals(listOf(count), message.arguments)
        }
    }
}

private object SendUiStateForTest {
    fun messageState(message: UiText): UiText =
        zip.psst.android.viewmodel.SendUiState(error = message).error!!
}
