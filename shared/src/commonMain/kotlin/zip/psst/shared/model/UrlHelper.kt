package zip.psst.shared.model

import kotlin.io.encoding.Base64
import kotlin.io.encoding.ExperimentalEncodingApi

/**
 * Parsed components from a transfer or drop slot URL.
 */
data class ParsedUrl(
    val id: String,
    val key: ByteArray,
    val type: UrlType,
) {
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other == null || other !is ParsedUrl) return false
        return id == other.id && key.contentEquals(other.key) && type == other.type
    }

    override fun hashCode(): Int {
        var result = id.hashCode()
        result = 31 * result + key.contentHashCode()
        result = 31 * result + type.hashCode()
        return result
    }
}

enum class UrlType {
    DOWNLOAD,
    UPLOAD,
}

/**
 * Builds and parses URLs of the form:
 *   {baseUrl}/d/{transferId}#{base64url_key}   (download/transfer)
 *   {baseUrl}/u/{slotId}#{base64url_key}       (upload/drop slot)
 *
 * The key is in the URL fragment and is never sent to the server.
 */
object UrlHelper {
    @OptIn(ExperimentalEncodingApi::class)
    private val base64Url = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)

    /**
     * Build a download URL for a transfer.
     */
    @OptIn(ExperimentalEncodingApi::class)
    fun buildDownloadUrl(baseUrl: String, transferId: String, key: ByteArray): String {
        val encodedKey = base64Url.encode(key)
        return "${baseUrl.trimEnd('/')}/d/$transferId#$encodedKey"
    }

    /**
     * Build an upload URL for a drop slot.
     */
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
        val fragmentIndex = url.indexOf('#')
        if (fragmentIndex == -1) return null

        val path = url.substring(0, fragmentIndex)
        val fragment = url.substring(fragmentIndex + 1)

        if (fragment.isBlank()) return null

        val key = try {
            base64Url.decode(fragment)
        } catch (_: Exception) {
            return null
        }

        // Extract the path portion after the last slash before the type prefix
        val pathSegments = path.trimEnd('/').split('/')
        if (pathSegments.size < 2) return null

        val id = pathSegments.last()
        val typeSegment = pathSegments[pathSegments.size - 2]

        val type = when (typeSegment) {
            "d" -> UrlType.DOWNLOAD
            "u" -> UrlType.UPLOAD
            else -> return null
        }

        if (id.isBlank()) return null

        return ParsedUrl(id = id, key = key, type = type)
    }
}
