package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.ServerOrigin
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
    companion object {
        /** Fresh client with no account token or cookie storage, even for the signed-in origin. */
        @Throws(Exception::class)
        fun anonymous(origin: String): ApiClient = ApiClient(validatedConfig(origin))

        /**
         * A fresh guest upload client, authorized only by the newly created child-transfer
         * capability.
         */
        @Throws(Exception::class)
        fun slotUpload(origin: String, capability: String): ApiClient {
            require(capability.matches(Regex("[A-Za-z0-9_-]{32,128}"))) {
                "Invalid upload capability"
            }
            return ApiClient(validatedConfig(origin), sessionToken = capability)
        }

        private fun validatedConfig(origin: String): ServerConfig =
            ServerConfig(
                requireNotNull(ServerOrigin.normalize(origin)) { "Invalid server address" }
            )
    }

    // Preserve the two-argument initializer exported to the existing Swift client.
    constructor(config: ServerConfig, httpClient: HttpClient) : this(config, httpClient, null)

    // Swift callers should not depend on the generated facade for an expect/actual function.
    constructor(
        config: ServerConfig,
        sessionToken: String?,
    ) : this(config, createPlatformHttpClient(), sessionToken)

    val transfers: TransferApi = TransferApi(httpClient, config, sessionToken)
    val slots: SlotApi = SlotApi(httpClient, config, sessionToken)
    val tus: TusClient = TusClient(httpClient, config.normalizedBaseUrl, sessionToken)
    val auth: AuthApi = AuthApi(httpClient, config, sessionToken)
    val limits: LimitsApi = LimitsApi(httpClient, config)
    val traffic: TrafficApi = TrafficApi(httpClient, config, sessionToken)

    @Throws(Exception::class)
    suspend fun createFileUpload(transferId: String, wireSize: Long): String =
        tus.create("${config.apiBaseUrl}/transfers/$transferId/files", wireSize)

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
