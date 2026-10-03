package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.Transfer
import zip.psst.shared.model.TransferLimits
import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.bearerAuth
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.prepareGet
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsChannel
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.utils.io.readAvailable
import kotlinx.coroutines.withTimeout

/** API operations for file transfers (send flow). */
class TransferApi(
    private val httpClient: HttpClient,
    private val config: ServerConfig,
    private val sessionToken: String? = null,
) {
    /** Create a new transfer. Returns the created transfer with its server-assigned ID. */
    @Throws(Exception::class)
    suspend fun create(): Transfer {
        val response =
            httpClient.post("${config.apiBaseUrl}/transfers") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
                contentType(ContentType.Application.Json)
                setBody(mapOf<String, String>())
            }
        response.checkAuthenticatedWrite()
        return response.body()
    }

    /** Revoke the link and stored uploads. Missing resources are already revoked. */
    @Throws(Exception::class)
    suspend fun delete(transferId: String, deleteToken: String? = null) {
        deleteLink(
            httpClient,
            "${config.apiBaseUrl}/transfers/$transferId",
            deleteToken ?: sessionToken,
        )
    }

    /** Get transfer metadata (status, file count, sizes, expiry). */
    @Throws(Exception::class)
    suspend fun get(transferId: String): Transfer {
        val response = httpClient.get("${config.apiBaseUrl}/transfers/$transferId")
        return response.body<Transfer>().also {
            require(it.id == transferId) { "The server returned details for a different transfer" }
        }
    }

    /**
     * Upload the encrypted manifest for a transfer.
     *
     * @param transferId the transfer ID
     * @param manifestBytes the encrypted manifest bytes (nonce + ciphertext)
     */
    @Throws(Exception::class)
    suspend fun uploadManifest(transferId: String, manifestBytes: ByteArray) {
        httpClient
            .post("${config.apiBaseUrl}/transfers/$transferId/manifest") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
                contentType(ContentType.Application.OctetStream)
                setBody(manifestBytes)
            }
            .checkAuthenticatedWrite()
    }

    /** Mark a transfer as complete (all files and manifest uploaded). */
    @Throws(Exception::class)
    suspend fun complete(transferId: String) {
        httpClient
            .post("${config.apiBaseUrl}/transfers/$transferId/complete") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
            }
            .checkAuthenticatedWrite()
    }

    /** Confirm successful download and decryption of every file; contains no keys or file data. */
    @Throws(Exception::class)
    suspend fun acknowledgeDownload(transferId: String) {
        val response =
            withTimeout(5_000L) {
                httpClient.post("${config.apiBaseUrl}/transfers/$transferId/downloaded")
            }
        require(response.status == HttpStatusCode.NoContent) {
            "Download acknowledgement failed: ${response.status}"
        }
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

    /** Actual bytes received; callbacks are bounded to one per 64 KiB plus start/final updates. */
    @Throws(Exception::class)
    suspend fun downloadFileWithProgress(
        transferId: String,
        fileId: String,
        onProgress: (received: Long, total: Long?) -> Unit,
    ): ByteArray =
        downloadBounded(
            "${config.apiBaseUrl}/transfers/$transferId/files/$fileId",
            TransferLimits.MAX_FILE_BYTES + 28,
            onProgress,
        )

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

    private suspend fun downloadBounded(
        url: String,
        maxBytes: Int,
        onProgress: ((Long, Long?) -> Unit)? = null,
    ): ByteArray =
        httpClient
            .prepareGet(url) { expectSuccess = false }
            .execute { response ->
                require(response.status.value in 200..299) { "Download failed: ${response.status}" }
                val declaredSize = response.headers[HttpHeaders.ContentLength]?.toLongOrNull()
                require(declaredSize == null || declaredSize in 0..maxBytes.toLong()) {
                    "Download exceeds this app's supported size limit"
                }
                onProgress?.invoke(0, declaredSize)
                var reported = 0
                val channel = response.bodyAsChannel()
                val buffer = ByteArray(8192)
                val chunks = mutableListOf<ByteArray>()
                var total = 0
                while (true) {
                    val count =
                        channel.readAvailable(buffer, 0, minOf(buffer.size, maxBytes - total + 1))
                    if (count == -1) break
                    if (count == 0) continue
                    total += count
                    require(total <= maxBytes) {
                        "Download exceeds this app's supported size limit"
                    }
                    chunks += buffer.copyOf(count)
                    if (total - reported >= 64 * 1024) {
                        reported = total
                        onProgress?.invoke(total.toLong(), declaredSize)
                    }
                }
                require(declaredSize == null || total.toLong() == declaredSize) {
                    "The download was interrupted"
                }
                if (total != reported) onProgress?.invoke(total.toLong(), declaredSize)
                ByteArray(total).also { result ->
                    var offset = 0
                    for (chunk in chunks) {
                        chunk.copyInto(result, offset)
                        offset += chunk.size
                    }
                }
            }
}
