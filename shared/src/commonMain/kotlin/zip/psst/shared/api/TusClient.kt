package zip.psst.shared.api

import io.ktor.client.HttpClient
import io.ktor.client.request.headers
import io.ktor.client.request.patch
import io.ktor.client.request.head
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.HttpResponse
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType

/**
 * Minimal tus (resumable upload) protocol client built on Ktor.
 *
 * Implements the core tus protocol operations:
 * - Creation: POST to create an upload resource
 * - Offset resume: HEAD to get current offset, PATCH to send remaining data
 *
 * @see <a href="https://tus.io/protocols/resumable-upload">tus protocol</a>
 */
class TusClient(
    private val httpClient: HttpClient,
) {
    companion object {
        const val TUS_VERSION = "1.0.0"
        private val TUS_CONTENT_TYPE = ContentType("application", "offset+octet-stream")
    }

    /**
     * Create a new tus upload resource.
     *
     * @param uploadUrl the tus endpoint URL (e.g., .../transfers/{id}/files)
     * @param totalSize total size of the upload in bytes
     * @param metadata optional metadata key-value pairs (base64 encoded values)
     * @return the Location header value (URL of the created upload resource)
     */
    suspend fun create(
        uploadUrl: String,
        totalSize: Long,
        metadata: Map<String, String> = emptyMap(),
    ): String {
        val metadataHeader = if (metadata.isNotEmpty()) {
            metadata.entries.joinToString(",") { (k, v) ->
                val encoded = v.encodeToByteArray().toBase64()
                "$k $encoded"
            }
        } else {
            null
        }

        val response: HttpResponse = httpClient.post(uploadUrl) {
            headers {
                append("Tus-Resumable", TUS_VERSION)
                append("Upload-Length", totalSize.toString())
                if (metadataHeader != null) {
                    append("Upload-Metadata", metadataHeader)
                }
            }
        }

        require(response.status == HttpStatusCode.Created) {
            "tus creation failed with status ${response.status}"
        }

        return response.headers[HttpHeaders.Location]
            ?: throw IllegalStateException("tus creation response missing Location header")
    }

    /**
     * Upload data to an existing tus resource, resuming from a given offset.
     *
     * @param resourceUrl the URL of the tus upload resource (from create's Location header)
     * @param data the full file data
     * @param offset byte offset to resume from (0 for a fresh upload)
     * @param chunkSize size of each upload chunk (default 1 MB)
     * @param onProgress callback with bytes uploaded so far
     */
    suspend fun upload(
        resourceUrl: String,
        data: ByteArray,
        offset: Long = 0,
        chunkSize: Int = 1024 * 1024,
        onProgress: ((uploaded: Long) -> Unit)? = null,
    ) {
        var currentOffset = offset

        while (currentOffset < data.size) {
            val end = minOf(currentOffset + chunkSize, data.size.toLong())
            val chunk = data.copyOfRange(currentOffset.toInt(), end.toInt())

            val response: HttpResponse = httpClient.patch(resourceUrl) {
                headers {
                    append("Tus-Resumable", TUS_VERSION)
                    append("Upload-Offset", currentOffset.toString())
                }
                contentType(TUS_CONTENT_TYPE)
                setBody(chunk)
            }

            require(response.status == HttpStatusCode.NoContent) {
                "tus upload failed with status ${response.status}"
            }

            val newOffset = response.headers["Upload-Offset"]?.toLongOrNull()
                ?: (currentOffset + chunk.size)

            currentOffset = newOffset
            onProgress?.invoke(currentOffset)
        }
    }

    /**
     * Get the current upload offset for a tus resource.
     * Used for resuming interrupted uploads.
     *
     * @param resourceUrl the URL of the tus upload resource
     * @return current byte offset
     */
    suspend fun getOffset(resourceUrl: String): Long {
        val response: HttpResponse = httpClient.head(resourceUrl) {
            headers {
                append("Tus-Resumable", TUS_VERSION)
            }
        }

        require(response.status == HttpStatusCode.OK) {
            "tus HEAD failed with status ${response.status}"
        }

        return response.headers["Upload-Offset"]?.toLongOrNull()
            ?: throw IllegalStateException("tus HEAD response missing Upload-Offset header")
    }
}

// Simple base64 encoding for tus metadata
private fun ByteArray.toBase64(): String {
    val table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
    val sb = StringBuilder()
    var i = 0
    while (i < size) {
        val b0 = this[i].toInt() and 0xFF
        val b1 = if (i + 1 < size) this[i + 1].toInt() and 0xFF else 0
        val b2 = if (i + 2 < size) this[i + 2].toInt() and 0xFF else 0

        sb.append(table[(b0 shr 2) and 0x3F])
        sb.append(table[((b0 shl 4) or (b1 shr 4)) and 0x3F])
        if (i + 1 < size) sb.append(table[((b1 shl 2) or (b2 shr 6)) and 0x3F]) else sb.append('=')
        if (i + 2 < size) sb.append(table[b2 and 0x3F]) else sb.append('=')
        i += 3
    }
    return sb.toString()
}
