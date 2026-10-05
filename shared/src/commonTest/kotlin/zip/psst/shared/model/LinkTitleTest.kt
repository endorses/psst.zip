package zip.psst.shared.model

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

class LinkTitleTest {
    @Test
    fun unicodeWhitespaceAndScalarLimit() {
        assertNull(LinkTitle.normalize(null))
        assertNull(LinkTitle.normalize(" \u2000\u00a0"))
        assertEquals("音楽", LinkTitle.normalize("\u00a0 音楽 \u2000"))
        assertEquals("😀".repeat(200), LinkTitle.normalize("😀".repeat(200)))
        assertFailsWith<IllegalArgumentException> { LinkTitle.normalize("😀".repeat(201)) }
    }

    @Test
    fun controlsAndUnpairedSurrogatesRejected() {
        for (title in listOf("\n", "a\u0085", "a\u007f", "\ud800", "\udc00")) {
            assertFailsWith<IllegalArgumentException> { LinkTitle.normalize(title) }
        }
        assertEquals("<script>hello</script>", LinkTitle.normalize("<script>hello</script>"))
    }
}
