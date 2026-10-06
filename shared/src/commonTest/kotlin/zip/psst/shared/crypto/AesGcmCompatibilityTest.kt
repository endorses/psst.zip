package zip.psst.shared.crypto

import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertFailsWith

class AesGcmCompatibilityTest {
    // AES-256 vectors from the GCM revised specification, Appendix B cases 13 and 14:
    // https://csrc.nist.gov/groups/ST/toolkit/BCM/documents/proposedmodes/gcm/gcm-revised-spec.pdf
    // Public test keys/nonces only; never reuse this nonce/key pair for real encryption.
    private val key = ByteArray(32)
    private val nonce = ByteArray(12)
    private val emptyTag = hex("530f8afbc74536b9a963b4f1c4cb738b")
    private val blockCiphertext =
        hex("cea7403d4d606b6e074ec5d3baf39d18d0d1c8a799996bf0265b98b5d48ab919")

    @Test
    fun emptyPlaintextMatchesIndependentTagAndDecrypts() {
        assertContentEquals(emptyTag, CryptoProvider.encrypt(key, nonce, ByteArray(0)))
        assertContentEquals(ByteArray(0), CryptoProvider.decrypt(key, nonce, emptyTag))
    }

    @Test
    fun singleBlockMatchesIndependentCiphertextWithAppendedTag() {
        assertContentEquals(blockCiphertext, CryptoProvider.encrypt(key, nonce, ByteArray(16)))
        assertContentEquals(ByteArray(16), CryptoProvider.decrypt(key, nonce, blockCiphertext))
    }

    @Test
    fun emptyAndNonemptyMessagesRejectModifiedAuthenticationInputs() {
        for (ciphertext in listOf(emptyTag, blockCiphertext)) {
            val modifiedTag = ciphertext.copyOf()
            modifiedTag[modifiedTag.lastIndex] = (modifiedTag.last().toInt() xor 1).toByte()
            assertFailsWith<Exception> { CryptoProvider.decrypt(key, nonce, modifiedTag) }

            val wrongKey = key.copyOf().also { it[0] = 1 }
            assertFailsWith<Exception> { CryptoProvider.decrypt(wrongKey, nonce, ciphertext) }

            val wrongNonce = nonce.copyOf().also { it[0] = 1 }
            assertFailsWith<Exception> { CryptoProvider.decrypt(key, wrongNonce, ciphertext) }
        }

        val modifiedCiphertext = blockCiphertext.copyOf().also { it[0] = 1 }
        assertFailsWith<Exception> { CryptoProvider.decrypt(key, nonce, modifiedCiphertext) }
    }

    @Test
    fun invalidKeyNonceAndTruncatedTagAreRejected() {
        for (size in listOf(0, 16, 24, 31, 33)) {
            assertFailsWith<IllegalArgumentException> {
                CryptoProvider.encrypt(ByteArray(size), nonce, ByteArray(0))
            }
            assertFailsWith<IllegalArgumentException> {
                CryptoProvider.decrypt(ByteArray(size), nonce, emptyTag)
            }
        }
        for (size in listOf(0, 11, 13)) {
            assertFailsWith<IllegalArgumentException> {
                CryptoProvider.encrypt(key, ByteArray(size), ByteArray(0))
            }
            assertFailsWith<IllegalArgumentException> {
                CryptoProvider.decrypt(key, ByteArray(size), emptyTag)
            }
        }
        for (size in listOf(0, 15)) {
            assertFailsWith<Exception> { CryptoProvider.decrypt(key, nonce, ByteArray(size)) }
        }
    }

    private fun hex(value: String): ByteArray =
        value.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
}
