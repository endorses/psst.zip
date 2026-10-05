package zip.psst.shared.api

import zip.psst.shared.model.LinkTitle
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.Transfer
import zip.psst.shared.model.TransferLimits
import io.ktor.client.HttpClient
import io.ktor.client.plugins.expectSuccess
import io.ktor.client.request.bearerAuth
import io.ktor.client.request.get
import io.ktor.client.request.patch
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
import kotlinx.serialization.Serializable

/** API operations for file transfers (send flow). */
class TransferApi(
    private val httpClient: HttpClient,
    private val config: ServerConfig,
    private val sessionToken: String? = null,
) {
    /** Create a new transfer. Returns the created transfer with its server-assigned ID. */
    /** Metadata-only classification after interrupted IO; never downloads file data. */
    @Throws(Exception::class)
    suspend fun trafficStatus(id: String): TransferTrafficStatus = trafficStatus(id, "download")

    @Throws(Exception::class)
    suspend fun trafficStatus(id: String, direction: String): TransferTrafficStatus {
        require(direction in listOf("upload", "download")) { "Invalid traffic direction" }
        require(id.matches(Regex("[0-9a-fA-F-]{36}"))) { "Invalid resource ID" }
        val response =
            httpClient.get(
                "${config.apiBaseUrl}/transfers/$id/traffic-status?direction=$direction"
            ) {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
            }
        if (response.status.value == 401 && sessionToken != null)
            throw AuthenticationRequiredException()
        return response.readControlJson<TransferTrafficStatus>(4096).also { it.policyException() }
    }

    @Throws(Exception::class) suspend fun create(): Transfer = create(0)

    @Throws(Exception::class)
    suspend fun create(maxDownloads: Int): Transfer = create(maxDownloads, null)

    @Throws(Exception::class)
    suspend fun create(maxDownloads: Int, title: String?): Transfer {
        require(maxDownloads >= 0) { "Invalid download limit" }
        val response =
            httpClient.post("${config.apiBaseUrl}/transfers") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
                contentType(ContentType.Application.Json)
                setBody(CreateTitledTransfer(maxDownloads, LinkTitle.normalize(title)))
            }
        response.checkAuthenticatedWrite()
        return response.readControlJson<Transfer>(4096).also {
            require(LinkTitle.normalize(it.title) == it.title)
        }
    }

    @Throws(Exception::class)
    suspend fun renameTitle(id: String, title: String?): LinkTitle {
        require(zip.psst.shared.model.UrlHelper.isResourceId(id))
        val response =
            httpClient.patch("${config.apiBaseUrl}/transfers/$id/title") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
                contentType(ContentType.Application.Json)
                setBody(LinkTitle(LinkTitle.normalize(title)))
            }
        response.checkAuthenticatedWrite()
        return response.readControlJson<LinkTitle>(4096).also {
            require(LinkTitle.normalize(it.title) == it.title)
        }
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
        val response =
            httpClient.get("${config.apiBaseUrl}/transfers/$transferId") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
            }
        if (response.status.value == 401 && sessionToken != null)
            throw AuthenticationRequiredException()
        return response.readControlJson<Transfer>().also {
            require(LinkTitle.normalize(it.title) == it.title)
            require(it.id == transferId) { "The server returned details for a different transfer" }
        }
    }

    /** Only the invited uploader's exact scoped capability can inspect completion state. */
    @Throws(Exception::class)
    suspend fun uploadStatus(transferId: String): Transfer {
        val response =
            httpClient.get("${config.apiBaseUrl}/transfers/$transferId/upload-status") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
            }
        if (response.status.value == 401 && sessionToken != null)
            throw AuthenticationRequiredException()
        return response.readControlJson<Transfer>(4096).also { require(it.id == transferId) }
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
                httpClient.post("${config.apiBaseUrl}/transfers/$transferId/downloaded") {
                    expectSuccess = false
                    sessionToken?.let { bearerAuth(it) }
                }
            }
        response.checkAccountRestriction()
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

    /** Delivers exact encrypted frames serially; false aborts without reading another frame. */
    @Throws(Exception::class)
    suspend fun downloadFileChunks(
        transferId: String,
        fileId: String,
        wireSize: Long,
        chunkBytes: Int,
        onChunk: (ByteArray) -> Boolean,
    ) {
        require(
            chunkBytes ==
                zip.psst.shared.crypto.ChunkedFileCrypto.CHUNK_SIZE +
                    zip.psst.shared.crypto.ChunkedFileCrypto.FRAME_OVERHEAD
        )
        require(
            wireSize in
                60..zip.psst.shared.crypto.ChunkedFileCrypto.wireSize(
                        zip.psst.shared.crypto.ChunkedFileCrypto.MAX_FILE_SIZE
                    )
        )
        httpClient
            .prepareGet("${config.apiBaseUrl}/transfers/$transferId/files/$fileId") {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
            }
            .execute { response ->
                response.checkAccountRestriction()
                if (response.status.value == 401 && sessionToken != null)
                    throw AuthenticationRequiredException()
                require(response.status.value == 200) { "Download failed: ${response.status}" }
                val declared = response.headers[HttpHeaders.ContentLength]?.toLongOrNull()
                require(declared == null || declared == wireSize) {
                    "The encrypted file length does not match its manifest"
                }
                val channel = response.bodyAsChannel()
                var remaining = wireSize
                while (remaining > 0) {
                    val frame = ByteArray(minOf(chunkBytes.toLong(), remaining).toInt())
                    var filled = 0
                    while (filled < frame.size) {
                        val count = channel.readAvailable(frame, filled, frame.size - filled)
                        if (count < 0) throw TransferDownloadInterruptedException()
                        filled += count
                    }
                    require(onChunk(frame)) { "File processing was stopped" }
                    remaining -= frame.size
                }
                val extra = ByteArray(1)
                require(channel.readAvailable(extra, 0, 1) == -1) {
                    "The encrypted file has unexpected trailing data"
                }
            }
    }

    private suspend fun downloadBounded(
        url: String,
        maxBytes: Int,
        onProgress: ((Long, Long?) -> Unit)? = null,
    ): ByteArray =
        httpClient
            .prepareGet(url) {
                expectSuccess = false
                sessionToken?.let { bearerAuth(it) }
            }
            .execute { response ->
                response.checkAccountRestriction()
                if (response.status.value == 401 && sessionToken != null)
                    throw AuthenticationRequiredException()
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
                if (declaredSize != null && total.toLong() != declaredSize)
                    throw TransferDownloadInterruptedException()
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

@Serializable private data class CreateTitledTransfer(val max_downloads: Int, val title: String?)
