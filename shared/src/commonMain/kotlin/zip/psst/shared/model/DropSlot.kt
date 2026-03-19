package zip.psst.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * A drop slot allows others to upload files to the slot creator.
 * The creator generates the encryption key and shares it via a URL fragment.
 */
@Serializable
data class DropSlot(
    val id: String,
    val status: DropSlotStatus = DropSlotStatus.WAITING,
    @SerialName("file_count") val fileCount: Int = 0,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
)

@Serializable
enum class DropSlotStatus {
    @SerialName("waiting")
    WAITING,

    @SerialName("has_uploads")
    HAS_UPLOADS,

    @SerialName("expired")
    EXPIRED,
}
