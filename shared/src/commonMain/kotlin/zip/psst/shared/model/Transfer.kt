package zip.psst.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Represents a file transfer created by the sender. The server stores encrypted blobs; the
 * encryption key is never sent to the server.
 */
@Serializable
data class Transfer(
    val id: String,
    @SerialName("file_count") val fileCount: Int = 0,
    @SerialName("total_size") val totalSize: Long = 0,
    val status: TransferStatus = TransferStatus.PENDING,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
    // Owner capability returned only at creation; never include it in a shared URL.
    @SerialName("delete_token") val deleteToken: String? = null,
    // Counts requested file sets, not confirmed saves on the recipient's device.
    @SerialName("download_count") val downloadCount: Int = 0,
    @SerialName("downloaded_at") val downloadedAt: String? = null,
)

@Serializable
enum class TransferStatus {
    @SerialName("pending") PENDING,
    @SerialName("complete") COMPLETE,
    @SerialName("expired") EXPIRED,
}
