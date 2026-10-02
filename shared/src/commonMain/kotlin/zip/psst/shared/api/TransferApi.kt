package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.Transfer
import zip.psst.shared.model.TransferLimits
import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.prepareGet
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsChannel
import io.ktor.http.ContentType
import io.ktor.http.contentType
import io.ktor.utils.io.readAvailable

/** API operations for file transfers (send flow). */
class TransferApi(private val httpClient: HttpClient, private val config: ServerConfig) {
    /** Create a new transfer. Returns the created transfer with its server-assigned ID. */
    @Throws(Exception::class)
    suspend fun create(): Transfer {
        val response =
            httpClient.post("${config.apiBaseUrl}/transfers") {
                contentType(ContentType.Application.Json)
                setBody(mapOf<String, String>())
            }
        return response.body()
    }

    /** Get transfer metadata (status, file count, sizes, expiry). */
    @Throws(Exception::class)
    suspend fun get(transferId: String): Transfer {
        val response = httpClient.get("${config.apiBaseUrl}/transfers/$transferId")
        return response.body()
    }

    /**
     * Upload the encrypted manifest for a transfer.
     *
     * @param transferId the transfer ID
     * @param manifestBytes the encrypted manifest bytes (nonce + ciphertext)
     */
    @Throws(Exception::class)
    suspend fun uploadManifest(transferId: String, manifestBytes: ByteArray) {
        httpClient.post("${config.apiBaseUrl}/transfers/$transferId/manifest") {
            contentType(ContentType.Application.OctetStream)
            setBody(manifestBytes)
        }
    }

    /** Mark a transfer as complete (all files and manifest uploaded). */
    @Throws(Exception::class)
    suspend fun complete(transferId: String) {
        httpClient.post("${config.apiBaseUrl}/transfers/$transferId/complete")
    }

    /**
     * Download an encrypted file blob.
     *
     * @param transferId the transfer ID
     * @param fileId the file/blob ID within the transfer
     * @return raw encrypted bytes
     */
    @Throws(Exception::class)
    suspend fun downloadFile(transferId: String, fileId: String): ByteArray {
        return downloadBounded(
            "${config.apiBaseUrl}/transfers/$transferId/files/$fileId",
            TransferLimits.MAX_FILE_BYTES + 28,
        )
    }

    /**
     * Download the encrypted manifest for a transfer.
     *
     * @return raw encrypted manifest bytes (nonce + ciphertext)
     */
    @Throws(Exception::class)
    suspend fun downloadManifest(transferId: String): ByteArray {
        return downloadBounded(
            "${config.apiBaseUrl}/transfers/$transferId/manifest",
            TransferLimits.MAX_MANIFEST_BYTES,
        )
    }

    private suspend fun downloadBounded(url: String, maxBytes: Int): ByteArray =
        httpClient.prepareGet(url).execute { response ->
            require(response.status.value in 200..299) { "Download failed: ${response.status}" }
            val channel = response.bodyAsChannel()
            val buffer = ByteArray(8192)
            val chunks = mutableListOf<ByteArray>()
            var total = 0
            while (true) {
                val count =
                    channel.readAvailable(buffer, 0, minOf(buffer.size, maxBytes - total + 1))
                if (count == -1) break
                total += count
                require(total <= maxBytes) { "Download exceeds this app's supported size limit" }
                chunks += buffer.copyOf(count)
            }
            ByteArray(total).also { result ->
                var offset = 0
                for (chunk in chunks) {
                    chunk.copyInto(result, offset)
                    offset += chunk.size
                }
            }
        }
}
