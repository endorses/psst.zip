package zip.psst.shared.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Account-scoped metadata only. Encryption keys and deletion capabilities never leave clients. */
@Serializable
data class AuthResources(
    val transfers: List<AuthResourceTransfer> = emptyList(),
    val slots: List<AuthResourceSlot> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String? = null,
)

@Serializable
data class AuthResourceTransfer(
    val id: String,
    val status: String,
    @SerialName("file_count") val fileCount: Int = 0,
    @SerialName("total_size") val totalSize: Long = 0,
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("download_count") val downloadCount: Int = 0,
    @SerialName("downloaded_at") val downloadedAt: String? = null,
)

@Serializable
data class AuthResourceSlot(
    val id: String,
    val status: String,
    val transfers: List<AuthResourceChild> = emptyList(),
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("file_count") val fileCount: Int = 0,
    @SerialName("completed_files") val completedFiles: Long = 0,
    @SerialName("total_size") val totalSize: Long = 0,
)

@Serializable
data class AuthResourceChild(
    @SerialName("transfer_id") val transferId: String,
    val status: String,
    @SerialName("file_count") val fileCount: Int = 0,
)
