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
    // Owner capability returned only at creation; never include it in a shared URL.
    @SerialName("delete_token") val deleteToken: String? = null,
    @SerialName("receive_protocol") val receiveProtocol: Int = 1,
    @SerialName("recipient_public_key") val recipientPublicKey: String = "",
    @SerialName("max_files") val maxFiles: Int = 0,
    @SerialName("reserved_files") val reservedFiles: Long = 0,
    @SerialName("completed_files") val completedFiles: Long = 0,
    @SerialName("remaining_files") val remainingFiles: Long? = null,
) {
    val fileCount: Int
        get() = completedTransfers.sumOf { it.fileCount }

    val completedTransfers: List<SlotTransfer>
        get() = transfers.filter { it.status == TransferStatus.COMPLETE }
}

@Serializable
data class SlotAvailability(
    val id: String,
    val available: Boolean = false,
    @SerialName("receive_protocol") val receiveProtocol: Int = 1,
    @SerialName("recipient_public_key") val recipientPublicKey: String = "",
    @SerialName("max_files") val maxFiles: Int = 0,
    @SerialName("remaining_files") val remainingFiles: Long? = null,
    @SerialName("remaining_bytes") val remainingBytes: Long = 0,
    @SerialName("remaining_transfers") val remainingTransfers: Int = 0,
) {
    @Throws(Exception::class)
    fun validateForSubmission(slotId: String, publicKey: ByteArray, fileCount: Int) {
        require(id == slotId && available && receiveProtocol == 2) {
            "This receive link is unavailable"
        }
        require(
            publicKey.size == 32 &&
                recipientPublicKey ==
                    kotlin.io.encoding.Base64.UrlSafe.withPadding(
                            kotlin.io.encoding.Base64.PaddingOption.ABSENT
                        )
                        .encode(publicKey)
        ) {
            "The server's receive key does not match the invitation"
        }
        require(
            fileCount in 0..TransferLimits.MAX_FILES &&
                maxFiles >= 0 &&
                remainingBytes >= 0 &&
                remainingTransfers > 0
        )
        remainingFiles?.let {
            require(it >= fileCount && maxFiles > 0 && it <= maxFiles) {
                "This receive link has too few file allocations remaining"
            }
        }
    }
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
