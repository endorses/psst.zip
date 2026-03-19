package zip.psst.shared.api

import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.Transfer
import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.contentType

/**
 * API operations for file transfers (send flow).
 */
class TransferApi(
    private val httpClient: HttpClient,
    private val config: ServerConfig,
) {
    /**
     * Create a new transfer. Returns the created transfer with its server-assigned ID.
     */
    suspend fun create(): Transfer {
        val response = httpClient.post("${config.apiBaseUrl}/transfers") {
            contentType(ContentType.Application.Json)
            setBody(mapOf<String, String>())
        }
        return response.body()
    }

    /**
     * Get transfer metadata (status, file count, sizes, expiry).
     */
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
    suspend fun uploadManifest(transferId: String, manifestBytes: ByteArray) {
        httpClient.post("${config.apiBaseUrl}/transfers/$transferId/manifest") {
            contentType(ContentType.Application.OctetStream)
            setBody(manifestBytes)
        }
    }

    /**
     * Mark a transfer as complete (all files and manifest uploaded).
     */
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
    suspend fun downloadFile(transferId: String, fileId: String): ByteArray {
        val response = httpClient.get(
            "${config.apiBaseUrl}/transfers/$transferId/files/$fileId"
        )
        return response.body()
    }

    /**
     * Download the encrypted manifest for a transfer.
     *
     * @return raw encrypted manifest bytes (nonce + ciphertext)
     */
    suspend fun downloadManifest(transferId: String): ByteArray {
        val response = httpClient.get(
            "${config.apiBaseUrl}/transfers/$transferId/manifest"
        )
        return response.body()
    }
}
