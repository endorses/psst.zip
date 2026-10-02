package zip.psst.shared.crypto

import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

actual object CryptoProvider {
    private const val AES_KEY_SIZE = 32
    private const val GCM_NONCE_SIZE = 12
    private const val GCM_TAG_BITS = 128
    private const val ALGORITHM = "AES/GCM/NoPadding"

    private val secureRandom = SecureRandom()

    @Throws(Exception::class)
    actual fun generateKey(): ByteArray {
        val key = ByteArray(AES_KEY_SIZE)
        secureRandom.nextBytes(key)
        return key
    }

    @Throws(Exception::class)
    actual fun generateNonce(): ByteArray {
        val nonce = ByteArray(GCM_NONCE_SIZE)
        secureRandom.nextBytes(nonce)
        return nonce
    }

    @Throws(Exception::class)
    actual fun encrypt(key: ByteArray, nonce: ByteArray, plaintext: ByteArray): ByteArray {
        require(key.size == AES_KEY_SIZE) { "Key must be $AES_KEY_SIZE bytes" }
        require(nonce.size == GCM_NONCE_SIZE) { "Nonce must be $GCM_NONCE_SIZE bytes" }

        val cipher = Cipher.getInstance(ALGORITHM)
        val keySpec = SecretKeySpec(key, "AES")
        val gcmSpec = GCMParameterSpec(GCM_TAG_BITS, nonce)
        cipher.init(Cipher.ENCRYPT_MODE, keySpec, gcmSpec)

        return cipher.doFinal(plaintext)
    }

    @Throws(Exception::class)
    actual fun decrypt(key: ByteArray, nonce: ByteArray, ciphertext: ByteArray): ByteArray {
        require(key.size == AES_KEY_SIZE) { "Key must be $AES_KEY_SIZE bytes" }
        require(nonce.size == GCM_NONCE_SIZE) { "Nonce must be $GCM_NONCE_SIZE bytes" }

        val cipher = Cipher.getInstance(ALGORITHM)
        val keySpec = SecretKeySpec(key, "AES")
        val gcmSpec = GCMParameterSpec(GCM_TAG_BITS, nonce)
        cipher.init(Cipher.DECRYPT_MODE, keySpec, gcmSpec)

        return cipher.doFinal(ciphertext)
    }
}
