@file:OptIn(ExperimentalForeignApi::class)

package zip.psst.shared.crypto

import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.addressOf
import kotlinx.cinterop.alloc
import kotlinx.cinterop.memScoped
import kotlinx.cinterop.ptr
import kotlinx.cinterop.usePinned
import kotlinx.cinterop.value
import platform.CoreCrypto.CCCryptorGCMDecrypt
import platform.CoreCrypto.CCCryptorGCMEncrypt
import platform.CoreCrypto.CCCryptorGCMFinalize
import platform.CoreCrypto.CCCryptorGCMOneshotDecrypt
import platform.CoreCrypto.CCCryptorGCMOneshotEncrypt
import platform.CoreCrypto.kCCAlgorithmAES
import platform.CoreCrypto.kCCSuccess
import platform.Security.SecRandomCopyBytes
import platform.Security.errSecSuccess
import platform.Security.kSecRandomDefault

actual object CryptoProvider {
    private const val AES_KEY_SIZE = 32
    private const val GCM_NONCE_SIZE = 12
    private const val GCM_TAG_SIZE = 16

    actual fun generateKey(): ByteArray = secureRandomBytes(AES_KEY_SIZE)

    actual fun generateNonce(): ByteArray = secureRandomBytes(GCM_NONCE_SIZE)

    actual fun encrypt(key: ByteArray, nonce: ByteArray, plaintext: ByteArray): ByteArray {
        require(key.size == AES_KEY_SIZE) { "Key must be $AES_KEY_SIZE bytes" }
        require(nonce.size == GCM_NONCE_SIZE) { "Nonce must be $GCM_NONCE_SIZE bytes" }

        val ciphertext = ByteArray(plaintext.size)
        val tag = ByteArray(GCM_TAG_SIZE)

        key.usePinned { keyPinned ->
            nonce.usePinned { noncePinned ->
                tag.usePinned { tagPinned ->
                    if (plaintext.isEmpty()) {
                        // Handle empty plaintext
                        val status = CCCryptorGCMOneshotEncrypt(
                            alg = kCCAlgorithmAES,
                            key = keyPinned.addressOf(0),
                            keyLength = key.size.toULong(),
                            iv = noncePinned.addressOf(0),
                            ivLen = nonce.size.toULong(),
                            aad = null,
                            aadLen = 0u,
                            dataIn = null,
                            dataInLength = 0u,
                            cipherOut = null,
                            tag = tagPinned.addressOf(0),
                            tagLength = GCM_TAG_SIZE.toULong(),
                        )
                        require(status == kCCSuccess) { "CCCryptorGCMOneshotEncrypt failed: $status" }
                    } else {
                        plaintext.usePinned { ptPinned ->
                            ciphertext.usePinned { ctPinned ->
                                val status = CCCryptorGCMOneshotEncrypt(
                                    alg = kCCAlgorithmAES,
                                    key = keyPinned.addressOf(0),
                                    keyLength = key.size.toULong(),
                                    iv = noncePinned.addressOf(0),
                                    ivLen = nonce.size.toULong(),
                                    aad = null,
                                    aadLen = 0u,
                                    dataIn = ptPinned.addressOf(0),
                                    dataInLength = plaintext.size.toULong(),
                                    cipherOut = ctPinned.addressOf(0),
                                    tag = tagPinned.addressOf(0),
                                    tagLength = GCM_TAG_SIZE.toULong(),
                                )
                                require(status == kCCSuccess) {
                                    "CCCryptorGCMOneshotEncrypt failed: $status"
                                }
                            }
                        }
                    }
                }
            }
        }

        // Return ciphertext + tag (same format as Android's Cipher output)
        return ciphertext + tag
    }

    actual fun decrypt(key: ByteArray, nonce: ByteArray, ciphertext: ByteArray): ByteArray {
        require(key.size == AES_KEY_SIZE) { "Key must be $AES_KEY_SIZE bytes" }
        require(nonce.size == GCM_NONCE_SIZE) { "Nonce must be $GCM_NONCE_SIZE bytes" }
        require(ciphertext.size >= GCM_TAG_SIZE) { "Ciphertext too short to contain GCM tag" }

        val ctLength = ciphertext.size - GCM_TAG_SIZE
        val ct = ciphertext.copyOfRange(0, ctLength)
        val tag = ciphertext.copyOfRange(ctLength, ciphertext.size)
        val plaintext = ByteArray(ctLength)

        key.usePinned { keyPinned ->
            nonce.usePinned { noncePinned ->
                tag.usePinned { tagPinned ->
                    if (ct.isEmpty()) {
                        val status = CCCryptorGCMOneshotDecrypt(
                            alg = kCCAlgorithmAES,
                            key = keyPinned.addressOf(0),
                            keyLength = key.size.toULong(),
                            iv = noncePinned.addressOf(0),
                            ivLen = nonce.size.toULong(),
                            aad = null,
                            aadLen = 0u,
                            dataIn = null,
                            dataInLength = 0u,
                            dataOut = null,
                            tag = tagPinned.addressOf(0),
                            tagLength = GCM_TAG_SIZE.toULong(),
                        )
                        require(status == kCCSuccess) {
                            "CCCryptorGCMOneshotDecrypt failed: $status (authentication failure or corrupted data)"
                        }
                    } else {
                        ct.usePinned { ctPinned ->
                            plaintext.usePinned { ptPinned ->
                                val status = CCCryptorGCMOneshotDecrypt(
                                    alg = kCCAlgorithmAES,
                                    key = keyPinned.addressOf(0),
                                    keyLength = key.size.toULong(),
                                    iv = noncePinned.addressOf(0),
                                    ivLen = nonce.size.toULong(),
                                    aad = null,
                                    aadLen = 0u,
                                    dataIn = ctPinned.addressOf(0),
                                    dataInLength = ct.size.toULong(),
                                    dataOut = ptPinned.addressOf(0),
                                    tag = tagPinned.addressOf(0),
                                    tagLength = GCM_TAG_SIZE.toULong(),
                                )
                                require(status == kCCSuccess) {
                                    "CCCryptorGCMOneshotDecrypt failed: $status (authentication failure or corrupted data)"
                                }
                            }
                        }
                    }
                }
            }
        }

        return plaintext
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
