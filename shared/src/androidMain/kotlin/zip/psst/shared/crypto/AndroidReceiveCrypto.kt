package zip.psst.shared.crypto

import com.google.crypto.tink.AccessesPartialKey
import com.google.crypto.tink.HybridDecrypt
import com.google.crypto.tink.HybridEncrypt
import com.google.crypto.tink.InsecureSecretKeyAccess
import com.google.crypto.tink.Key
import com.google.crypto.tink.KeysetHandle
import com.google.crypto.tink.RegistryConfiguration
import com.google.crypto.tink.hybrid.HpkeParameters
import com.google.crypto.tink.hybrid.HpkePrivateKey
import com.google.crypto.tink.hybrid.HpkePublicKey
import com.google.crypto.tink.hybrid.HybridConfig
import com.google.crypto.tink.util.Bytes
import com.google.crypto.tink.util.SecretBytes

/** Raw secrets are returned only for protected account/slot storage. Never log or share them. */
class ReceiveKeyPair(val privateKey: ByteArray, val publicKey: ByteArray)

/** Narrow raw-key bridge. Tink owns all HPKE arithmetic, key schedule, nonce and randomness. */
@AccessesPartialKey
object AndroidReceiveCrypto {
    private val parameters =
        HpkeParameters.builder()
            .setKemId(HpkeParameters.KemId.DHKEM_X25519_HKDF_SHA256)
            .setKdfId(HpkeParameters.KdfId.HKDF_SHA256)
            .setAeadId(HpkeParameters.AeadId.AES_256_GCM)
            .setVariant(HpkeParameters.Variant.NO_PREFIX)
            .build()

    init {
        HybridConfig.register()
    }

    fun generateKeyPair(): ReceiveKeyPair {
        val key = KeysetHandle.generateNew(parameters).getAt(0).key as HpkePrivateKey
        return ReceiveKeyPair(
            key.privateKeyBytes.toByteArray(InsecureSecretKeyAccess.get()),
            key.publicKey.publicKeyBytes.toByteArray(),
        )
    }

    /** A fresh submission key/context must be created once, then persisted for upload retries. */
    fun sealSubmissionKey(
        publicKey: ByteArray,
        slotId: String,
        submissionId: String,
        submissionKey: ByteArray,
    ): ByteArray {
        require(submissionKey.size == 32) { "Invalid submission key length" }
        val info = ReceiveEnvelope.info(slotId, submissionId, publicKey)
        return handle(importPublic(publicKey))
            .getPrimitive(RegistryConfiguration.get(), HybridEncrypt::class.java)
            .encrypt(submissionKey, info)
            .also {
                check(it.size == ReceiveEnvelope.WRAPPED_KEY_BYTES) { "Invalid HPKE output length" }
            }
    }

    fun openSubmissionKey(
        privateKey: ByteArray,
        publicKey: ByteArray,
        slotId: String,
        submissionId: String,
        wrappedKey: ByteArray,
    ): ByteArray {
        require(privateKey.size == 32 && wrappedKey.size == ReceiveEnvelope.WRAPPED_KEY_BYTES) {
            "Invalid receive key length"
        }
        val info = ReceiveEnvelope.info(slotId, submissionId, publicKey)
        // Public Tink import verifies that the private key corresponds to the expected public key.
        val key =
            HpkePrivateKey.create(
                importPublic(publicKey),
                SecretBytes.copyFrom(privateKey, InsecureSecretKeyAccess.get()),
            )
        return handle(key)
            .getPrimitive(RegistryConfiguration.get(), HybridDecrypt::class.java)
            .decrypt(wrappedKey, info)
            .also { require(it.size == 32) { "Invalid decrypted submission key length" } }
    }

    private fun importPublic(raw: ByteArray): HpkePublicKey {
        require(raw.size == 32) { "Invalid receive public key length" }
        return HpkePublicKey.create(parameters, Bytes.copyFrom(raw), null)
    }

    private fun handle(key: Key): KeysetHandle =
        KeysetHandle.newBuilder()
            .addEntry(KeysetHandle.importKey(key).withRandomId().makePrimary())
            .build()
}
