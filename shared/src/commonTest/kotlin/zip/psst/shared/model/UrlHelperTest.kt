package zip.psst.shared.model

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class UrlHelperTest {
    private val testKey = ByteArray(32) { it.toByte() }

    @Test
    fun buildDownloadUrlContainsCorrectPath() {
        val url = UrlHelper.buildDownloadUrl("https://example.com", "abc123", testKey)
        assertTrue(url.startsWith("https://example.com/d/abc123#"))
    }

    @Test
    fun buildUploadUrlContainsCorrectPath() {
        val url = UrlHelper.buildUploadUrl("https://example.com", "slot456", testKey)
        assertTrue(url.startsWith("https://example.com/u/slot456#"))
    }

    @Test
    fun buildDownloadUrlStripsTrailingSlash() {
        val url = UrlHelper.buildDownloadUrl("https://example.com/", "abc123", testKey)
        assertTrue(url.startsWith("https://example.com/d/abc123#"))
        assertTrue(!url.contains("//d/"))
    }

    @Test
    fun parseDownloadUrl() {
        val url =
            UrlHelper.buildDownloadUrl(
                "https://example.com",
                "01234567-89ab-cdef-0123-456789abcdef",
                testKey,
            )
        val parsed = UrlHelper.parse(url)

        assertNotNull(parsed)
        assertEquals("01234567-89ab-cdef-0123-456789abcdef", parsed.id)
        assertEquals(UrlType.DOWNLOAD, parsed.type)
        assertEquals(testKey.toList(), parsed.key.toList())
    }

    @Test
    fun parseUploadUrl() {
        val url =
            UrlHelper.buildUploadUrl(
                "https://example.com",
                "01234567-89ab-cdef-0123-456789abcdef",
                testKey,
            )
        val parsed = UrlHelper.parse(url)

        assertNotNull(parsed)
        assertEquals("01234567-89ab-cdef-0123-456789abcdef", parsed.id)
        assertEquals(UrlType.UPLOAD, parsed.type)
        assertEquals(testKey.toList(), parsed.key.toList())
    }

    @Test
    fun roundTripDownloadUrl() {
        val originalKey = ByteArray(32) { (it * 7 + 3).toByte() }
        val url =
            UrlHelper.buildDownloadUrl(
                "https://drop.example.com",
                "01234567-89ab-cdef-0123-456789abcdef",
                originalKey,
            )
        val parsed = UrlHelper.parse(url)

        assertNotNull(parsed)
        assertEquals("01234567-89ab-cdef-0123-456789abcdef", parsed.id)
        assertEquals(UrlType.DOWNLOAD, parsed.type)
        assertEquals(originalKey.toList(), parsed.key.toList())
    }

    @Test
    fun roundTripUploadUrl() {
        val originalKey = ByteArray(32) { (it * 13 + 1).toByte() }
        val url =
            UrlHelper.buildUploadUrl(
                "https://drop.example.com",
                "01234567-89ab-cdef-0123-456789abcdef",
                originalKey,
            )
        val parsed = UrlHelper.parse(url)

        assertNotNull(parsed)
        assertEquals("01234567-89ab-cdef-0123-456789abcdef", parsed.id)
        assertEquals(UrlType.UPLOAD, parsed.type)
        assertEquals(originalKey.toList(), parsed.key.toList())
    }

    @Test
    fun parseReturnsNullForNoFragment() {
        assertNull(UrlHelper.parse("https://example.com/d/abc123"))
    }

    @Test
    fun parseReturnsNullForEmptyFragment() {
        assertNull(UrlHelper.parse("https://example.com/d/abc123#"))
    }

    @Test
    fun parseReturnsNullForUnknownType() {
        assertNull(UrlHelper.parse("https://example.com/x/abc123#AAAA"))
    }

    @Test
    fun parseReturnsNullForTooFewSegments() {
        assertNull(UrlHelper.parse("abc123#AAAA"))
    }

    @Test
    fun parsedUrlEquality() {
        val key = ByteArray(32) { it.toByte() }
        val a = ParsedUrl("id1", key, UrlType.DOWNLOAD)
        val b = ParsedUrl("id1", key.copyOf(), UrlType.DOWNLOAD)
        assertEquals(a, b)
        assertEquals(a.hashCode(), b.hashCode())
    }
}
