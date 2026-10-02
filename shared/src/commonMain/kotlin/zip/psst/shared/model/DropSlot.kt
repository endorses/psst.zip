package zip.psst.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * A drop slot allows others to upload files to the slot creator. The creator generates the
 * encryption key and shares it via a URL fragment.
 */
@Serializable
data class DropSlot(
    val id: String,
    val status: DropSlotStatus = DropSlotStatus.WAITING,
    val transfers: List<SlotTransfer> = emptyList(),
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
) {
    val fileCount: Int
        get() = completedTransfers.sumOf { it.fileCount }

    val completedTransfers: List<SlotTransfer>
        get() = transfers.filter { it.status == TransferStatus.COMPLETE }
}

@Serializable
data class SlotTransfer(
    @SerialName("transfer_id") val transferId: String,
    val status: TransferStatus = TransferStatus.PENDING,
    @SerialName("file_count") val fileCount: Int = 0,
)

@Serializable
enum class DropSlotStatus {
    @SerialName("waiting") WAITING,
    @SerialName("has_uploads") HAS_UPLOADS,
    @SerialName("expired") EXPIRED,
}
