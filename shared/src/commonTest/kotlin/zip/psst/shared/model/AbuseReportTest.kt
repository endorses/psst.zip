package zip.psst.shared.model

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull

class AbuseReportTest {
    private val id = "12345678-1234-1234-1234-123456789ABC"

    @Test
    fun malformedKeyCanStillBeReportedWithoutRetainingIt() {
        val reference =
            assertNotNull(
                AbuseReportReference.fromRawLink(
                    "https://example.com/d/$id?filename=private.pdf#invalid-secret"
                )
            )
        assertEquals(
            "Instance: https://example.com\nResource: transfer ${id.lowercase()}",
            reference.text,
        )
        assertNull(
            AbuseReportReference.fromRawLink("https://user:password@example.com/d/$id#secret")
        )
        assertNull(AbuseReportReference.fromRawLink("https://example.com/d/not-an-id#secret"))
    }

    @Test
    fun reportContainsOnlyCanonicalOriginAndTypedResource() {
        val key = ByteArray(32) { 42 }
        val raw = UrlHelper.buildDownloadUrl("https://EXAMPLE.com:443", id, key)
        val reference =
            assertNotNull(AbuseReportReference.fromLink(assertNotNull(UrlHelper.parse(raw))))
        assertEquals(
            "Instance: https://example.com\nResource: transfer ${id.lowercase()}",
            reference.text,
        )
        assertFalse(reference.text.contains('#'))
        assertFalse(reference.text.contains(raw.substringAfter('#')))
        val slot = ParsedUrl(id, key, UrlType.UPLOAD, "http://localhost:8080", 2)
        assertEquals(
            "Instance: http://localhost:8080\nResource: slot ${id.lowercase()}",
            assertNotNull(AbuseReportReference.fromLink(slot)).text,
        )
        assertEquals(
            "Instance: https://example.com",
            assertNotNull(AbuseReportReference.create("https://example.com")).text,
        )
    }

    @Test
    fun rejectsSecretBearingOrMalformedOriginsAndResourceIdentifiers() {
        for (origin in
            listOf(
                "https://user:secret@example.com",
                "https://example.com/d/$id#key",
                "https://example.com?key=secret",
                "https://example.com#secret",
                "mailto:abuse@example.com",
                "https://example.com\r\nBcc:bad@example.com",
                "https://example.com/filename.pdf",
                "https://example.com\\evil",
            )) assertNull(AbuseReportReference.create(origin), origin)
        for (resource in
            listOf("$id#key", "$id?secret", "a.pdf", "../../secret", "\nsecret")) assertNull(
            AbuseReportReference.create("https://example.com", AbuseResourceKind.TRANSFER, resource)
        )
        assertNull(AbuseReportReference.create("https://example.com", AbuseResourceKind.SLOT))
        assertNull(AbuseReportReference.create("https://example.com", resourceId = id))
    }

    @Test
    fun acceptsOnlySingleBoundedAsciiMailbox() {
        assertEquals(
            "Abuse+tag%box@example.com",
            AbuseContact.normalize("Abuse+tag%box@EXAMPLE.COM"),
        )
        for (email in
            listOf(
                "",
                "abuse@localhost",
                "mailto:abuse@example.com",
                "Person <abuse@example.com>",
                "a@example.com,b@example.com",
                "a@example.com?bcc=b@example.com",
                "a@example.com\r\nBcc:b@example.com",
                "a@example.com ",
                " a@example.com",
                ".a@example.com",
                "a.@example.com",
                "a..b@example.com",
                "a@-example.com",
                "a@example-.com",
                "a@example..com",
                "a@éxample.com",
                "ü@example.com",
                "a@${"a".repeat(64)}.com",
                "${"a".repeat(65)}@example.com",
                "a@${("a".repeat(63) + ".").repeat(4)}com",
            )) assertNull(AbuseContact.normalize(email), email)
    }
}
