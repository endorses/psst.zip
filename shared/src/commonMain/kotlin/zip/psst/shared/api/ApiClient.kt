package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import io.ktor.client.HttpClient

/**
 * Central API client that holds the HTTP client and server configuration. All API operations are
 * accessed through this client.
 */
class ApiClient(
    val config: ServerConfig,
    private val httpClient: HttpClient = createPlatformHttpClient(),
) {
    val transfers: TransferApi = TransferApi(httpClient, config)
    val slots: SlotApi = SlotApi(httpClient, config)
    val tus: TusClient = TusClient(httpClient)

    /**
     * Upload an encrypted file to a transfer via the tus protocol.
     *
     * @param transferId the transfer ID
     * @param data encrypted file data
     * @param metadata optional non-sensitive tus metadata; never include plaintext filenames
     * @param onProgress callback with bytes uploaded so far
     * @return the tus resource URL for this upload
     */
    @Throws(Exception::class)
    suspend fun uploadFile(
        transferId: String,
        data: ByteArray,
        metadata: Map<String, String> = emptyMap(),
        onProgress: ((uploaded: Long) -> Unit)? = null,
    ): String {
        val tusUrl = "${config.apiBaseUrl}/transfers/$transferId/files"
        val resourceUrl = tus.create(tusUrl, data.size.toLong(), metadata)
        tus.upload(resourceUrl, data, onProgress = onProgress)
        return resourceUrl
    }

    fun close() {
        httpClient.close()
    }
}
