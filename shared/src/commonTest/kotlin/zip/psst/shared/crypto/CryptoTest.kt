package zip.psst.shared.crypto

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotEquals

class CryptoTest {
    @Test
    fun generateKeyProduces32Bytes() {
        val key = CryptoProvider.generateKey()
        assertEquals(32, key.size)
    }

    @Test
    fun generateKeyProducesUniqueKeys() {
        val key1 = CryptoProvider.generateKey()
        val key2 = CryptoProvider.generateKey()
        assertNotEquals(key1.toList(), key2.toList())
    }

    @Test
    fun generateNonceProduces12Bytes() {
        val nonce = CryptoProvider.generateNonce()
        assertEquals(12, nonce.size)
    }

    @Test
    fun encryptDecryptRoundTrip() {
        val key = CryptoProvider.generateKey()
        val nonce = CryptoProvider.generateNonce()
        val plaintext = "Hello, encrypted world!".encodeToByteArray()

        val ciphertext = CryptoProvider.encrypt(key, nonce, plaintext)
        val decrypted = CryptoProvider.decrypt(key, nonce, ciphertext)

        assertEquals(plaintext.toList(), decrypted.toList())
    }

    @Test
    fun encryptDecryptEmptyData() {
        val key = CryptoProvider.generateKey()
        val nonce = CryptoProvider.generateNonce()
        val plaintext = ByteArray(0)

        val ciphertext = CryptoProvider.encrypt(key, nonce, plaintext)
        val decrypted = CryptoProvider.decrypt(key, nonce, ciphertext)

        assertEquals(0, decrypted.size)
    }

    @Test
    fun encryptProducesDifferentOutputPerNonce() {
        val key = CryptoProvider.generateKey()
        val plaintext = "Same data, different nonce".encodeToByteArray()

        val nonce1 = CryptoProvider.generateNonce()
        val nonce2 = CryptoProvider.generateNonce()

        val ct1 = CryptoProvider.encrypt(key, nonce1, plaintext)
        val ct2 = CryptoProvider.encrypt(key, nonce2, plaintext)

        assertNotEquals(ct1.toList(), ct2.toList())
    }

    @Test
    fun decryptWithWrongKeyFails() {
        val key1 = CryptoProvider.generateKey()
        val key2 = CryptoProvider.generateKey()
        val nonce = CryptoProvider.generateNonce()
        val plaintext = "Secret data".encodeToByteArray()

        val ciphertext = CryptoProvider.encrypt(key1, nonce, plaintext)

        assertFailsWith<Exception> {
            CryptoProvider.decrypt(key2, nonce, ciphertext)
        }
    }

    @Test
    fun decryptWithTamperedCiphertextFails() {
        val key = CryptoProvider.generateKey()
        val nonce = CryptoProvider.generateNonce()
        val plaintext = "Authenticated data".encodeToByteArray()

        val ciphertext = CryptoProvider.encrypt(key, nonce, plaintext)
        // Tamper with the ciphertext
        val tampered = ciphertext.copyOf()
        tampered[0] = (tampered[0].toInt() xor 0xFF).toByte()

        assertFailsWith<Exception> {
            CryptoProvider.decrypt(key, nonce, tampered)
        }
    }

    @Test
    fun streamingEncryptDecryptRoundTrip() {
        val key = CryptoProvider.generateKey()
        val plaintext = "A longer message that should be split into multiple chunks for testing".encodeToByteArray()

        val encrypted = encryptStreaming(key, plaintext, chunkSize = 16)
        val decrypted = decryptStreaming(key, encrypted)

        assertEquals(plaintext.toList(), decrypted.toList())
    }

    @Test
    fun streamingEncryptProducesMultipleChunks() {
        val key = CryptoProvider.generateKey()
        val plaintext = ByteArray(100) { it.toByte() }

        val encrypted = encryptStreaming(key, plaintext, chunkSize = 32)

        // 100 bytes / 32 byte chunks = 4 chunks (32 + 32 + 32 + 4)
        assertEquals(4, encrypted.size)
    }

    @Test
    fun streamingEncryptDecryptLargeData() {
        val key = CryptoProvider.generateKey()
        val plaintext = ByteArray(10_000) { (it % 256).toByte() }

        val encrypted = encryptStreaming(key, plaintext, chunkSize = DEFAULT_CHUNK_SIZE)
        val decrypted = decryptStreaming(key, encrypted)

        assertEquals(plaintext.toList(), decrypted.toList())
    }
}
