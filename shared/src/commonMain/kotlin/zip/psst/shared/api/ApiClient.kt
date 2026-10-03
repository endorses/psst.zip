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
    sessionToken: String? = null,
) {
    // Preserve the two-argument initializer exported to the existing Swift client.
    constructor(config: ServerConfig, httpClient: HttpClient) : this(config, httpClient, null)

    val transfers: TransferApi = TransferApi(httpClient, config, sessionToken)
    val slots: SlotApi = SlotApi(httpClient, config, sessionToken)
    val tus: TusClient = TusClient(httpClient, config.normalizedBaseUrl, sessionToken)
    val auth: AuthApi = AuthApi(httpClient, config, sessionToken)

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

    /** Verify the server origin, API version, and public upload/download pages before saving it. */
    @Throws(Exception::class)
    suspend fun validateServer() {
        validateServer(config, httpClient)
    }

    fun close() {
        httpClient.close()
    }
}
