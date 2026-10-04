package zip.psst.shared.crypto

/** Authenticated, bounded file frames. The authenticated manifest carries the per-file context. */
object ChunkedFileCrypto {
    const val ENCODING = "chunked-v1"
    const val CHUNK_SIZE = 4 * 1024 * 1024
    const val FRAME_OVERHEAD = 60
    const val MAX_FILE_SIZE = 1024L * 1024 * 1024 * 1024

    @Throws(Exception::class)
    fun createId(): String =
        CryptoProvider.generateKey().take(16).joinToString("") {
            (it.toInt() and 255).toString(16).padStart(2, '0')
        }

    @Throws(Exception::class)
    fun chunkCount(totalSize: Long): Long {
        require(totalSize in 0..MAX_FILE_SIZE) { "Unsupported file size" }
        return maxOf(1, (totalSize + CHUNK_SIZE - 1) / CHUNK_SIZE)
    }

    @Throws(Exception::class)
    fun wireSize(totalSize: Long): Long = totalSize + chunkCount(totalSize) * FRAME_OVERHEAD

    @Throws(Exception::class)
    fun plaintextSize(totalSize: Long, index: Long): Int {
        require(index in 0 until chunkCount(totalSize)) { "Invalid chunk position" }
        return minOf(CHUNK_SIZE.toLong(), totalSize - index * CHUNK_SIZE).toInt()
    }

    private fun header(id: String, totalSize: Long, index: Long): ByteArray {
        require(id.matches(Regex("[0-9a-f]{32}"))) { "Invalid file encryption identifier" }
        plaintextSize(totalSize, index)
        val result = ByteArray(32)
        for (i in 0 until 16) result[i] = id.substring(i * 2, i * 2 + 2).toInt(16).toByte()
        for (i in 0 until 8) {
            result[16 + i] = (index ushr ((7 - i) * 8)).toByte()
            result[24 + i] = (totalSize ushr ((7 - i) * 8)).toByte()
        }
        return result
    }

    @Throws(Exception::class)
    fun encrypt(
        key: ByteArray,
        id: String,
        totalSize: Long,
        index: Long,
        plaintext: ByteArray,
    ): ByteArray {
        require(key.size == 32 && plaintext.size == plaintextSize(totalSize, index)) {
            "Invalid file chunk"
        }
        val nonce = CryptoProvider.generateNonce()
        return nonce + CryptoProvider.encrypt(key, nonce, header(id, totalSize, index) + plaintext)
    }

    @Throws(Exception::class)
    fun decrypt(
        key: ByteArray,
        id: String,
        totalSize: Long,
        index: Long,
        frame: ByteArray,
    ): ByteArray {
        require(key.size == 32 && frame.size == plaintextSize(totalSize, index) + FRAME_OVERHEAD) {
            "Invalid encrypted chunk length"
        }
        val plain =
            CryptoProvider.decrypt(key, frame.copyOfRange(0, 12), frame.copyOfRange(12, frame.size))
        require(plain.copyOfRange(0, 32).contentEquals(header(id, totalSize, index))) {
            "Encrypted chunk does not belong at this file position"
        }
        return plain.copyOfRange(32, plain.size)
    }
}
