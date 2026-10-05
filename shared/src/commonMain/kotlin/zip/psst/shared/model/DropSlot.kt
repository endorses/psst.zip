package zip.psst.shared.model

import zip.psst.shared.api.ClientFailureException
import zip.psst.shared.api.clientRequire
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * A private receive inbox lets guests submit encrypted files. Its public receive key is shared in
 * the invitation fragment; only the creator retains the private key and owner capability.
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
    val paginated: Boolean = false,
    @SerialName("next_cursor") val nextCursor: String? = null,
    val summary: InboxSummary? = null,
    val title: String? = null,
) {
    val fileCount: Int
        get() = completedTransfers.sumOf { it.fileCount }

    val completedTransfers: List<SlotTransfer>
        get() = transfers.filter { it.status == TransferStatus.COMPLETE }
}

/** Canonical retained-row totals; updating means every total is unknown. */
@Serializable
data class InboxSummary(
    val state: String,
    @SerialName("completed_files") val completedFiles: Long?,
    @SerialName("file_count") val fileCount: Long?,
    @SerialName("total_size") val totalSize: Long?,
) {
    val ready: Boolean
        get() = state == "ready"
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
    @SerialName("upload_capacity") val uploadCapacity: UploadCapacity? = null,
    val title: String? = null,
) {
    @Throws(Exception::class)
    fun validateInvitation(slotId: String, publicKey: ByteArray) {
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
        require(maxFiles >= 0 && remainingBytes >= 0 && remainingTransfers > 0) {
            "This receive link cannot accept another submission"
        }
        require(
            if (maxFiles == 0) remainingFiles == null
            else remainingFiles != null && remainingFiles in 0..maxFiles.toLong()
        ) {
            "The server returned an invalid receive allowance"
        }
        uploadCapacity?.validate()
    }

    @Throws(Exception::class)
    fun validateForSubmission(
        slotId: String,
        publicKey: ByteArray,
        fileCount: Int,
        totalWireBytes: Long,
    ) {
        validateInvitation(slotId, publicKey)
        clientRequire(
            fileCount in 1..TransferLimits.MAX_FILES,
            "selection_file_limit",
            mapOf("count" to TransferLimits.MAX_FILES.toString()),
        ) {
            "Select between 1 and 100 files"
        }
        clientRequire(
            remainingFiles == null || fileCount.toLong() <= remainingFiles,
            "receive_selection_file_limit",
        ) {
            "This receive link has too few file allocations remaining"
        }
        clientRequire(
            totalWireBytes >= 0 && totalWireBytes <= remainingBytes,
            "receive_selection_byte_limit",
        ) {
            "The selected files exceed this receive link's remaining byte allowance"
        }
        (uploadCapacity
                ?: throw ClientFailureException(CAPACITY_RETRY, "receive_capacity_unavailable"))
            .validateSelection(fileCount, totalWireBytes)
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
