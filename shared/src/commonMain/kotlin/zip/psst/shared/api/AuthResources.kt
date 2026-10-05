package zip.psst.shared.api

import zip.psst.shared.model.InboxSummary
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Account-scoped metadata only. Encryption keys and deletion capabilities never leave clients. */
@Serializable
data class AuthResources(
    val transfers: List<AuthResourceTransfer> = emptyList(),
    val slots: List<AuthResourceSlot> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String? = null,
    val paginated: Boolean = false,
    @SerialName("sync_cursor") val syncCursor: String? = null,
    val generation: String? = null,
)

@Serializable
data class AuthResourceTransfer(
    val id: String,
    val status: String,
    val revision: Long = 0,
    @SerialName("history_after") val historyAfter: String? = null,
    @SerialName("history_after_kind") val historyAfterKind: String? = null,
    @SerialName("file_count") val fileCount: Long? = null,
    @SerialName("total_size") val totalSize: Long? = null,
    val summary: InboxSummary? = null,
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("download_count") val downloadCount: Int = 0,
    @SerialName("downloaded_at") val downloadedAt: String? = null,
    @SerialName("max_downloads") val maxDownloads: Int? = null,
    val title: String? = null,
    @SerialName("inactive_reason") val inactiveReason: String? = null,
)

@Serializable
data class AuthResourceSlot(
    val id: String,
    val status: String,
    val revision: Long = 0,
    @SerialName("history_after") val historyAfter: String? = null,
    @SerialName("history_after_kind") val historyAfterKind: String? = null,
    val transfers: List<AuthResourceChild> = emptyList(),
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("file_count") val fileCount: Long? = null,
    @SerialName("completed_files") val completedFiles: Long? = null,
    @SerialName("total_size") val totalSize: Long? = null,
    val summary: InboxSummary? = null,
    @SerialName("max_files") val maxFiles: Int? = null,
    @SerialName("reserved_files") val reservedFiles: Long? = null,
    val title: String? = null,
)

@Serializable
data class AuthResourceChild(
    @SerialName("transfer_id") val transferId: String,
    val status: String,
    @SerialName("file_count") val fileCount: Long? = null,
)
