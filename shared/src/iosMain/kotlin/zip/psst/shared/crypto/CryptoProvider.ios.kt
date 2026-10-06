@file:OptIn(ExperimentalForeignApi::class, DelicateCryptographyApi::class)

package zip.psst.shared.crypto

import dev.whyoleg.cryptography.CryptographyProvider
import dev.whyoleg.cryptography.DelicateCryptographyApi
import dev.whyoleg.cryptography.algorithms.AES
import dev.whyoleg.cryptography.providers.cryptokit.CryptoKit
import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.addressOf
import kotlinx.cinterop.usePinned
import platform.Security.SecRandomCopyBytes
import platform.Security.errSecSuccess
import platform.Security.kSecRandomDefault

actual object CryptoProvider {
    private const val AES_KEY_SIZE = 32
    private const val GCM_NONCE_SIZE = 12
    private const val GCM_TAG_SIZE = 16

    @Throws(Exception::class) actual fun generateKey(): ByteArray = secureRandomBytes(AES_KEY_SIZE)

    @Throws(Exception::class)
    actual fun generateNonce(): ByteArray = secureRandomBytes(GCM_NONCE_SIZE)

    @Throws(Exception::class)
    actual fun encrypt(key: ByteArray, nonce: ByteArray, plaintext: ByteArray): ByteArray {
        require(key.size == AES_KEY_SIZE) { "Key must be $AES_KEY_SIZE bytes" }
        require(nonce.size == GCM_NONCE_SIZE) { "Nonce must be $GCM_NONCE_SIZE bytes" }

        // The explicit-IV API returns ciphertext || tag, without prepending the nonce.
        // CryptoKit authenticates empty plaintext as well as non-empty chunks.
        return CryptographyProvider.CryptoKit.get(AES.GCM)
            .keyDecoder()
            .decodeFromByteArrayBlocking(AES.Key.Format.RAW, key)
            .cipher()
            .encryptWithIvBlocking(nonce, plaintext)
    }

    @Throws(Exception::class)
    actual fun decrypt(key: ByteArray, nonce: ByteArray, ciphertext: ByteArray): ByteArray {
        require(key.size == AES_KEY_SIZE) { "Key must be $AES_KEY_SIZE bytes" }
        require(nonce.size == GCM_NONCE_SIZE) { "Nonce must be $GCM_NONCE_SIZE bytes" }
        require(ciphertext.size >= GCM_TAG_SIZE) { "Ciphertext too short to contain GCM tag" }

        return CryptographyProvider.CryptoKit.get(AES.GCM)
            .keyDecoder()
            .decodeFromByteArrayBlocking(AES.Key.Format.RAW, key)
            .cipher()
            .decryptWithIvBlocking(nonce, ciphertext)
    }

    private fun secureRandomBytes(size: Int): ByteArray {
        val bytes = ByteArray(size)
        bytes.usePinned { pinned ->
            val status = SecRandomCopyBytes(kSecRandomDefault, size.toULong(), pinned.addressOf(0))
            require(status == errSecSuccess) { "SecRandomCopyBytes failed with status: $status" }
        }
        return bytes
    }
}
