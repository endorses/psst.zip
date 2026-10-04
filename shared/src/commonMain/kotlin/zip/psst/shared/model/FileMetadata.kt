package zip.psst.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Metadata for a single file within a transfer. This data is stored inside the encrypted manifest
 * and is never visible to the server.
 */
@Serializable
data class FileMetadata(
    val name: String,
    val size: Long,
    @SerialName("mime_type") val mimeType: String = "application/octet-stream",
    @SerialName("blob_id") val blobId: String = "",
    val encoding: String = "",
    @SerialName("chunk_size") val chunkSize: Int = 0,
    @SerialName("encryption_id") val encryptionId: String = "",
)
