package zip.psst.shared.model

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ScanInputTest {
    private val id = "01234567-89ab-cdef-0123-456789abcdef"
    private val key = ByteArray(32) { (it * 7).toByte() }
    private val fragment =
        UrlHelper.buildDownloadUrl("https://example.com", id, key).substringAfter('#')

    @Test
    fun versionedReceiveInvitationsCarryPublicKeysAndLegacyInvitationsCannotSubmit() {
        val parsed =
            assertNotNull(
                    ScanInputClassifier.classify(
                        UrlHelper.buildReceiveUrl("https://example.com", id, key)
                    )
                )
                .link!!
        assertEquals(2, parsed.receiveVersion)
        assertEquals(UrlType.UPLOAD, parsed.type)
        assertTrue(parsed.key.contentEquals(key))
        assertNull(
            ScanInputClassifier.classify(UrlHelper.buildUploadUrl("https://example.com", id, key))
        )
        assertNull(ScanInputClassifier.classify("https://example.com/u/$id#v3.$fragment"))
        assertNull(ScanInputClassifier.classify("https://example.com/d/$id#v2.$fragment"))
        assertNull(ScanInputClassifier.classify("https://example.com/u/$id#v2.$fragment.extra"))
    }

    @Test
    fun generatedLinksAndOriginsRoundTrip() {
        val origins =
            mapOf(
                "https://EXAMPLE.com:443/" to "https://example.com",
                "http://192.168.178.29:8080" to "http://192.168.178.29:8080",
                "http://localhost:80" to "http://localhost",
                "https://server.example:8443" to "https://server.example:8443",
                "http://[::1]:8080" to "http://[0:0:0:0:0:0:0:1]:8080",
                "https://[2001:DB8::1]:443" to "https://[2001:db8:0:0:0:0:0:1]",
                "http://[::ffff:192.0.2.1]" to "http://[0:0:0:0:0:ffff:c000:201]",
            )
        for ((input, expected) in origins) for (type in listOf(UrlType.DOWNLOAD, UrlType.UPLOAD)) {
            val url =
                if (type == UrlType.DOWNLOAD) UrlHelper.buildDownloadUrl(input, id.uppercase(), key)
                else UrlHelper.buildReceiveUrl(input, id.uppercase(), key)
            val parsed = assertNotNull(ScanInputClassifier.classify(" \n$url\t")).link!!
            assertEquals(expected, parsed.origin)
            assertEquals(id, parsed.id)
            assertEquals(type, parsed.type)
            assertTrue(key.contentEquals(parsed.key))
            assertFalse(parsed.toString().contains(fragment))
            assertFalse(parsed.toString().contains(key.joinToString()))
        }
    }

    @Test
    fun hostileOrMalformedLinksNeverClassify() {
        val urls =
            listOf(
                "https://user:pass@example.com/d/$id#$fragment",
                "file://example.com/d/$id#$fragment",
                "//example.com/d/$id#$fragment",
                "https://example.com/prefix/d/$id#$fragment",
                "https://example.com/d/$id/?x#$fragment",
                "https://example.com/d/$id?#$fragment",
                "https://example.com/d/$id?x=1#$fragment",
                "https://example.com/d/../../$id#$fragment",
                "https://example.com/d/%30${id.drop(1)}#$fragment",
                "https://example.com/d/abc#$fragment",
                "https://example.com/d/$id#AAAA",
                "https://example.com/d/$id#$fragment#extra",
                "https://example.com:0/d/$id#$fragment",
                "https://example.com:65536/d/$id#$fragment",
                "https://example.com:invalid/d/$id#$fragment",
                "https://[:::1]/d/$id#$fragment",
                "https://[1:2:3]/d/$id#$fragment",
                "https://[::ffff:999.0.0.1]/d/$id#$fragment",
                "https://example.com\\evil/d/$id#$fragment",
                "https://exam\nple.com/d/$id#$fragment",
                "https://%65xample.com/d/$id#$fragment",
                "https://127.1/d/$id#$fragment",
                "https://example.com/d/$id#${fragment.dropLast(1)}B",
                "https://example.com/d/$id#${fragment}=extra",
                "x".repeat(4097),
            )
        for (input in urls) assertNull(
            ScanInputClassifier.classify(input),
            "Rejected input must remain local",
        )
        for (length in listOf(0, 1, 16, 31, 33, 64)) assertNull(
            UrlHelper.parse(
                UrlHelper.buildDownloadUrl("https://example.com", id, ByteArray(length))
            )
        )
    }

    @Test
    fun pairingPreservesVersionedIdentityAndNormalizesOrigin() {
        val payload =
            """{"type":"psst-pairing","version":1,"server_url":"https://EXAMPLE.com:443/","code":"${"a".repeat(43)}"}"""
        val parsed = assertNotNull(ScanInputClassifier.classify(payload))
        assertEquals(ScanInputKind.PAIRING, parsed.kind)
        assertEquals("https://example.com", parsed.pairing!!.serverUrl)
        assertNull(parsed.link)
        assertNull(ScanInputClassifier.classify(payload.replace("psst-pairing", "unknown")))
        assertNull(
            ScanInputClassifier.classify(
                payload.replace("https://EXAMPLE.com:443/", "https://u:p@example.com")
            )
        )
        assertNull(ScanInputClassifier.classify(payload.replace("\"version\":1", "\"version\":2")))
    }
}
