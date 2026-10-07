package zip.psst.android.data

import java.net.URI
import javax.net.ssl.HttpsURLConnection
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/** Public metadata only: no session token, cookies, redirects or capability-bearing URLs. */
data class ReleaseSource(
    val version: String,
    val revision: String,
    val source: String,
    val sourceArchive: String,
) {
    companion object {
        fun safeSourceURL(value: String): String? = runCatching {
            if (value.length > 2048 || value.any { it <= ' ' || it == '\u007f' }) return null
            val uri = URI(value)
            if (
                uri.scheme != "https" ||
                    uri.host.isNullOrEmpty() ||
                    uri.userInfo != null ||
                    uri.query != null ||
                    uri.fragment != null ||
                    (uri.port != -1 && uri.port !in 1..65535)
            )
                return null
            value.trimEnd('/')
        }
            .getOrNull()

        fun metadataURL(server: String): String? {
            val safe = safeSourceURL(server) ?: return null
            if (URI(safe).path !in listOf("", "/")) return null
            return "$safe/licenses/release.json"
        }

        fun parse(json: String): ReleaseSource? = runCatching {
            if (json.toByteArray(Charsets.UTF_8).size > 16 * 1024) return null
            val data = Json.parseToJsonElement(json) as? JsonObject ?: return null
            fun string(key: String): String =
                (data[key] as? JsonPrimitive)?.takeIf { it.isString }?.content ?: error(key)
            val version = string("version")
            val revision = string("revision")
            if (
                string("name") != "psst.zip" ||
                    string("license") != "AGPL-3.0-only" ||
                    !version.matches(Regex("[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}")) ||
                    !revision.matches(Regex("[a-f0-9]{40}"))
            )
                return null
            ReleaseSource(
                version,
                revision,
                safeSourceURL(string("source")) ?: return null,
                safeSourceURL(string("source_archive")) ?: return null,
            )
        }
            .getOrNull()

        fun fetch(server: String): ReleaseSource? {
            val url = metadataURL(server) ?: return null
            val connection = URI(url).toURL().openConnection() as HttpsURLConnection
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 10_000
            connection.readTimeout = 10_000
            connection.useCaches = false
            try {
                if (connection.responseCode != 200) return null
                val bytes =
                    connection.inputStream.use { input ->
                        val output = java.io.ByteArrayOutputStream()
                        val buffer = ByteArray(4096)
                        while (output.size() <= 16 * 1024) {
                            val count =
                                input.read(
                                    buffer,
                                    0,
                                    minOf(buffer.size, 16 * 1024 + 1 - output.size()),
                                )
                            if (count < 0) break
                            output.write(buffer, 0, count)
                        }
                        output.toByteArray()
                    }
                if (bytes.size > 16 * 1024) return null
                return parse(bytes.toString(Charsets.UTF_8))
            } finally {
                connection.disconnect()
            }
        }
    }
}
