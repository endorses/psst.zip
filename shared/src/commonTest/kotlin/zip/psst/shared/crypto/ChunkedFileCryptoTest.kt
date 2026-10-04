package zip.psst.shared.crypto

import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class ChunkedFileCryptoTest {
    private val key = ByteArray(32) { it.toByte() }
    private val id = "00112233445566778899aabbccddeeff"

    @Test
    fun emptyAndBoundaryFilesRoundTripWithExactWireSize() {
        for (size in
            listOf(
                0L,
                1L,
                ChunkedFileCrypto.CHUNK_SIZE.toLong(),
                ChunkedFileCrypto.CHUNK_SIZE + 1L,
            )) {
            var wire = 0L
            for (index in 0 until ChunkedFileCrypto.chunkCount(size)) {
                val plain =
                    ByteArray(ChunkedFileCrypto.plaintextSize(size, index)) { (it % 251).toByte() }
                val frame = ChunkedFileCrypto.encrypt(key, id, size, index, plain)
                wire += frame.size
                assertContentEquals(plain, ChunkedFileCrypto.decrypt(key, id, size, index, frame))
            }
            assertEquals(ChunkedFileCrypto.wireSize(size), wire)
        }
    }

    @Test
    fun moreThan100MiBProcessesOneFrameAtATime() {
        val size = 101L * 1024 * 1024 + 19
        var received = 0L
        for (index in 0 until ChunkedFileCrypto.chunkCount(size)) {
            val plain = ByteArray(ChunkedFileCrypto.plaintextSize(size, index)) { index.toByte() }
            val encrypted = ChunkedFileCrypto.encrypt(key, id, size, index, plain)
            val decrypted = ChunkedFileCrypto.decrypt(key, id, size, index, encrypted)
            assertContentEquals(plain, decrypted)
            received += decrypted.size
        }
        assertEquals(size, received)
    }

    @Test
    fun rejectsReorderingSubstitutionTruncationAndTampering() {
        val size = 2L * ChunkedFileCrypto.CHUNK_SIZE
        val frame =
            ChunkedFileCrypto.encrypt(key, id, size, 0, ByteArray(ChunkedFileCrypto.CHUNK_SIZE))
        assertFailsWith<Exception> { ChunkedFileCrypto.decrypt(key, id, size, 1, frame) }
        assertFailsWith<Exception> {
            ChunkedFileCrypto.decrypt(key, "f".repeat(32), size, 0, frame)
        }
        assertFailsWith<Exception> { ChunkedFileCrypto.decrypt(key, id, size + 1, 0, frame) }
        assertFailsWith<Exception> {
            ChunkedFileCrypto.decrypt(key, id, size, 0, frame.copyOf(frame.size - 1))
        }
        frame[frame.lastIndex] = (frame.last().toInt() xor 1).toByte()
        assertFailsWith<Exception> { ChunkedFileCrypto.decrypt(key, id, size, 0, frame) }
    }

    @Test
    fun invalidSizesAndContextAreRejectedBeforeAllocation() {
        for (size in listOf(-1L, Long.MAX_VALUE, ChunkedFileCrypto.MAX_FILE_SIZE + 1)) {
            assertFailsWith<IllegalArgumentException> { ChunkedFileCrypto.wireSize(size) }
        }
        assertFailsWith<IllegalArgumentException> {
            ChunkedFileCrypto.encrypt(key, "bad", 0, 0, ByteArray(0))
        }
        assertFailsWith<IllegalArgumentException> { ChunkedFileCrypto.plaintextSize(0, 1) }
    }

    @Test
    fun decryptsIndependentNodeAesGcmVector() {
        val hex =
            "202122232425262728292a2bd22b844328cd7c7992e5e8750dc51a06d049ec9c878063ee6cf66c12498a5e1e05fd9670a05f5caa349e06e736623fae8b5298fbe859278064c1cb8958bad91aea26d71db5bbdf"
        val bytes = hex.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
        assertContentEquals(
            "psst.zip chunk fixture\n".encodeToByteArray(),
            ChunkedFileCrypto.decrypt(key, id, 23L, 0, bytes),
        )
    }
}
