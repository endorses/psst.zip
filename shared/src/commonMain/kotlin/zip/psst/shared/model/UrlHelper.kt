package zip.psst.shared.model

import kotlin.io.encoding.Base64
import kotlin.io.encoding.ExperimentalEncodingApi

/** Parsed components from a transfer or drop slot URL. */
data class ParsedUrl(
    val id: String,
    val key: ByteArray,
    val type: UrlType,
    val origin: String = "",
) {
    override fun toString(): String =
        "ParsedUrl(id=$id, type=$type, origin=$origin, key=[redacted])"

    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other == null || other !is ParsedUrl) return false
        return id == other.id &&
            key.contentEquals(other.key) &&
            type == other.type &&
            origin == other.origin
    }

    override fun hashCode(): Int {
        var result = id.hashCode()
        result = 31 * result + key.contentHashCode()
        result = 31 * result + type.hashCode()
        return 31 * result + origin.hashCode()
    }
}

enum class UrlType {
    DOWNLOAD,
    UPLOAD,
}

/**
 * Builds and parses URLs of the form: {baseUrl}/d/{transferId}#{base64url_key} (download/transfer)
 * {baseUrl}/u/{slotId}#{base64url_key} (upload/drop slot)
 *
 * The key is in the URL fragment and is never sent to the server.
 */
object UrlHelper {
    @OptIn(ExperimentalEncodingApi::class)
    private val base64Url = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)

    /** Build a download URL for a transfer. */
    @OptIn(ExperimentalEncodingApi::class)
    fun buildDownloadUrl(baseUrl: String, transferId: String, key: ByteArray): String {
        val encodedKey = base64Url.encode(key)
        return "${baseUrl.trimEnd('/')}/d/$transferId#$encodedKey"
    }

    /** Build an upload URL for a drop slot. */
    @OptIn(ExperimentalEncodingApi::class)
    fun buildUploadUrl(baseUrl: String, slotId: String, key: ByteArray): String {
        val encodedKey = base64Url.encode(key)
        return "${baseUrl.trimEnd('/')}/u/$slotId#$encodedKey"
    }

    /**
     * Parse a download or upload URL, extracting the ID and encryption key.
     *
     * @return Parsed URL components, or null if the URL format is invalid.
     */
    @OptIn(ExperimentalEncodingApi::class)
    fun parse(url: String): ParsedUrl? {
        if (url.length > ScanInputClassifier.MAX_INPUT_LENGTH) return null
        val input = url.trim()
        val match =
            Regex(
                    "^(https?://[^/?#]+)/([du])/([0-9a-fA-F-]+)#([A-Za-z0-9_-]{43}=?$)",
                    RegexOption.IGNORE_CASE,
                )
                .matchEntire(input) ?: return null
        val origin = ServerOrigin.normalize(match.groupValues[1]) ?: return null
        val id = match.groupValues[3]
        if (!isResourceId(id)) return null
        val fragment = match.groupValues[4].removeSuffix("=")
        val key =
            try {
                base64Url.decode(fragment)
            } catch (_: Exception) {
                return null
            }
        if (key.size != 32 || base64Url.encode(key) != fragment) return null
        val type =
            when (match.groupValues[2]) {
                "d" -> UrlType.DOWNLOAD
                "u" -> UrlType.UPLOAD
                else -> return null
            }
        return ParsedUrl(id.lowercase(), key, type, origin)
    }

    fun isResourceId(value: String): Boolean =
        Regex("[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")
            .matches(value)
}
