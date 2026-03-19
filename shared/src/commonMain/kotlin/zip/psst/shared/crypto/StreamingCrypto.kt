package zip.psst.shared.crypto

/**
 * Default chunk size for streaming encryption/decryption: 64 KB.
 * Chosen to stay well within iOS share extension memory limits (~120 MB).
 */
const val DEFAULT_CHUNK_SIZE = 64 * 1024

/**
 * Overhead per encrypted chunk: 12 bytes nonce + 16 bytes GCM auth tag.
 */
const val CHUNK_OVERHEAD = 12 + 16

/**
 * Streaming encryption: processes input data in chunks, producing encrypted chunks.
 *
 * Each output chunk is formatted as: [nonce (12 bytes)] [ciphertext + tag].
 * This allows each chunk to be independently decryptable.
 *
 * @param key 32-byte AES-256 key
 * @param input raw plaintext bytes
 * @param chunkSize size of plaintext chunks to encrypt (default 64 KB)
 * @return sequence of encrypted chunks, each prefixed with its nonce
 */
fun encryptStreaming(
    key: ByteArray,
    input: ByteArray,
    chunkSize: Int = DEFAULT_CHUNK_SIZE,
): List<ByteArray> {
    require(key.size == 32) { "Key must be 32 bytes (256 bits)" }
    require(chunkSize > 0) { "Chunk size must be positive" }

    val chunks = mutableListOf<ByteArray>()
    var offset = 0

    while (offset < input.size) {
        val end = minOf(offset + chunkSize, input.size)
        val plainChunk = input.copyOfRange(offset, end)

        val nonce = CryptoProvider.generateNonce()
        val ciphertext = CryptoProvider.encrypt(key, nonce, plainChunk)

        // Output: nonce + ciphertext (which includes GCM tag)
        chunks.add(nonce + ciphertext)
        offset = end
    }

    return chunks
}

/**
 * Streaming decryption: processes encrypted chunks, producing plaintext output.
 *
 * Each input chunk must be formatted as: [nonce (12 bytes)] [ciphertext + tag].
 *
 * @param key 32-byte AES-256 key
 * @param encryptedChunks list of encrypted chunks, each prefixed with its nonce
 * @return concatenated plaintext bytes
 */
fun decryptStreaming(
    key: ByteArray,
    encryptedChunks: List<ByteArray>,
): ByteArray {
    require(key.size == 32) { "Key must be 32 bytes (256 bits)" }

    val plainParts = mutableListOf<ByteArray>()

    for (chunk in encryptedChunks) {
        require(chunk.size > 12) { "Encrypted chunk too small to contain nonce" }
        val nonce = chunk.copyOfRange(0, 12)
        val ciphertext = chunk.copyOfRange(12, chunk.size)

        val plaintext = CryptoProvider.decrypt(key, nonce, ciphertext)
        plainParts.add(plaintext)
    }

    // Concatenate all plaintext parts
    val totalSize = plainParts.sumOf { it.size }
    val result = ByteArray(totalSize)
    var offset = 0
    for (part in plainParts) {
        part.copyInto(result, offset)
        offset += part.size
    }
    return result
}
