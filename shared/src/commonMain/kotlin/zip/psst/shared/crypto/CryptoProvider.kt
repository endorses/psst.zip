package zip.psst.shared.crypto

/**
 * Result of an encryption operation.
 */
data class EncryptionResult(
    val ciphertext: ByteArray,
    val nonce: ByteArray,
) {
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other == null || other !is EncryptionResult) return false
        return ciphertext.contentEquals(other.ciphertext) && nonce.contentEquals(other.nonce)
    }

    override fun hashCode(): Int {
        var result = ciphertext.contentHashCode()
        result = 31 * result + nonce.contentHashCode()
        return result
    }
}

/**
 * Platform-specific AES-256-GCM cryptographic operations.
 *
 * Each platform provides its own implementation:
 * - Android: javax.crypto.Cipher
 * - iOS: CryptoKit.AES.GCM
 */
expect object CryptoProvider {
    /**
     * Generate a random 256-bit (32-byte) AES key.
     */
    fun generateKey(): ByteArray

    /**
     * Generate a random 96-bit (12-byte) GCM nonce.
     */
    fun generateNonce(): ByteArray

    /**
     * Encrypt a plaintext chunk using AES-256-GCM.
     *
     * @param key 32-byte AES key
     * @param nonce 12-byte GCM nonce (must be unique per encryption with the same key)
     * @param plaintext data to encrypt
     * @return ciphertext with appended GCM authentication tag
     */
    fun encrypt(key: ByteArray, nonce: ByteArray, plaintext: ByteArray): ByteArray

    /**
     * Decrypt a ciphertext chunk using AES-256-GCM.
     *
     * @param key 32-byte AES key
     * @param nonce 12-byte GCM nonce used during encryption
     * @param ciphertext encrypted data with appended GCM authentication tag
     * @return decrypted plaintext
     * @throws Exception if authentication fails or data is corrupted
     */
    fun decrypt(key: ByteArray, nonce: ByteArray, ciphertext: ByteArray): ByteArray
}
