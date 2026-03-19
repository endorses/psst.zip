package zip.psst.shared.model

import kotlinx.serialization.Serializable

/**
 * The plaintext manifest that gets encrypted before upload.
 * Contains metadata about all files in a transfer.
 * The server only ever sees the encrypted form.
 */
@Serializable
data class Manifest(
    val files: List<FileMetadata>,
)

/**
 * The encrypted form of the manifest, ready for upload to the server.
 */
data class EncryptedManifest(
    val ciphertext: ByteArray,
    val nonce: ByteArray,
) {
    /**
     * Serialized form: nonce prepended to ciphertext.
     */
    fun toBytes(): ByteArray = nonce + ciphertext

    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other == null || other !is EncryptedManifest) return false
        return ciphertext.contentEquals(other.ciphertext) && nonce.contentEquals(other.nonce)
    }

    override fun hashCode(): Int {
        var result = ciphertext.contentHashCode()
        result = 31 * result + nonce.contentHashCode()
        return result
    }

    companion object {
        /** GCM nonce size in bytes */
        const val NONCE_SIZE = 12

        /**
         * Parse from serialized bytes (nonce + ciphertext).
         */
        fun fromBytes(data: ByteArray): EncryptedManifest {
            require(data.size > NONCE_SIZE) { "Data too short to contain nonce and ciphertext" }
            return EncryptedManifest(
                nonce = data.copyOfRange(0, NONCE_SIZE),
                ciphertext = data.copyOfRange(NONCE_SIZE, data.size),
            )
        }
    }
}
