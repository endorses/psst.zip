package zip.psst.android.data

import org.junit.Assert.*
import org.junit.Test

class ReleaseSourceTest {
    @Test
    fun metadataUsesConfiguredOriginAndRejectsSecrets() {
        assertEquals(
            "https://operator.example:8443/licenses/release.json",
            ReleaseSource.metadataURL("https://operator.example:8443/"),
        )
        for (url in
            listOf(
                "http://operator.example",
                "https://user:pass@operator.example",
                "https://operator.example/?secret=x",
                "https://operator.example/#secret",
                "https://operator.example/path",
                "https://operator.example/\n",
            )) assertNull(ReleaseSource.metadataURL(url))
    }

    @Test
    fun exactSourceNeedsFullRevisionAndSafeLinks() {
        val metadata =
            """{"name":"psst.zip","license":"AGPL-3.0-only","version":"v1.2.3","revision":"${"a".repeat(40)}","source":"https://example.org/fork","source_archive":"https://example.org/source/a.tar.gz"}"""
        assertEquals("a".repeat(40), ReleaseSource.parse(metadata)?.revision)
        assertNull(ReleaseSource.parse(metadata.replace("a".repeat(40), "main")))
        assertNull(
            ReleaseSource.parse(
                metadata.replace("https://example.org/source/a.tar.gz", "javascript:alert(1)")
            )
        )
        assertNull(ReleaseSource.parse(" ".repeat(16 * 1024 + 1) + metadata))
    }
}
