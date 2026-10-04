package zip.psst.shared.crypto

import java.io.File
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertFalse
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

class AndroidReceiveCryptoTest {
    private val slot = "00112233-4455-6677-8899-aabbccddeeff"
    private val transfer = "ffeeddcc-bbaa-9988-7766-554433221100"

    @Test
    fun independentBrowserFixtureMatchesApplicationBindingAndDecrypts() {
        val fixture =
            Json.parseToJsonElement(
                    File("../docs/security/fixtures/hpke-receive-v2.json").readText()
                )
                .jsonObject
        fun value(name: String) = fixture.getValue(name).jsonPrimitive.content
        fun bytes(name: String) = value(name).chunked(2).map { it.toInt(16).toByte() }.toByteArray()
        assertContentEquals(
            bytes("context_info"),
            ReceiveEnvelope.info(value("slot_id"), value("transfer_id"), bytes("public_key")),
        )
        assertContentEquals(
            bytes("submission_key"),
            AndroidReceiveCrypto.openSubmissionKey(
                bytes("private_key"),
                bytes("public_key"),
                value("slot_id"),
                value("transfer_id"),
                bytes("wrapped_key"),
            ),
        )
        assertContentEquals(
            bytes("submission_key"),
            AndroidReceiveCrypto.openSubmissionKey(
                bytes("private_key"),
                bytes("public_key"),
                value("slot_id"),
                value("transfer_id"),
                bytes("tink_wrapped_key"),
            ),
        )
        assertContentEquals(
            bytes("submission_key"),
            AndroidReceiveCrypto.openSubmissionKey(
                bytes("private_key"),
                bytes("public_key"),
                value("slot_id"),
                value("transfer_id"),
                bytes("swift_wrapped_key"),
            ),
        )
    }

    @Test
    fun randomRoundTripUsesExactPrefixFreeWrapperAndFreshContexts() {
        repeat(3) {
            val recipient = AndroidReceiveCrypto.generateKeyPair()
            val key = CryptoProvider.generateKey()
            val first =
                AndroidReceiveCrypto.sealSubmissionKey(recipient.publicKey, slot, transfer, key)
            val second =
                AndroidReceiveCrypto.sealSubmissionKey(recipient.publicKey, slot, transfer, key)
            assertEquals(32, recipient.privateKey.size)
            assertEquals(32, recipient.publicKey.size)
            assertEquals(80, first.size)
            assertFalse(first.contentEquals(second))
            assertContentEquals(
                key,
                AndroidReceiveCrypto.openSubmissionKey(
                    recipient.privateKey,
                    recipient.publicKey,
                    slot,
                    transfer,
                    first,
                ),
            )
        }
    }

    @Test
    fun ciphertextAndEveryApplicationBindingMustAuthenticate() {
        val recipient = AndroidReceiveCrypto.generateKeyPair()
        val key = CryptoProvider.generateKey()
        val wrapped =
            AndroidReceiveCrypto.sealSubmissionKey(recipient.publicKey, slot, transfer, key)
        fun open(
            privateKey: ByteArray = recipient.privateKey,
            publicKey: ByteArray = recipient.publicKey,
            slotId: String = slot,
            transferId: String = transfer,
            value: ByteArray = wrapped,
        ) = AndroidReceiveCrypto.openSubmissionKey(privateKey, publicKey, slotId, transferId, value)
        for (index in listOf(0, 31, 32, 79)) {
            assertFails {
                open(
                    value = wrapped.copyOf().also { it[index] = (it[index].toInt() xor 1).toByte() }
                )
            }
        }
        assertFails { open(slotId = transfer) }
        assertFails { open(transferId = slot) }
        val other = AndroidReceiveCrypto.generateKeyPair()
        assertFails { open(privateKey = other.privateKey) }
        assertFails { open(publicKey = other.publicKey) }
        assertFails { open(privateKey = other.privateKey, publicKey = other.publicKey) }
        assertFails { open(value = wrapped + byteArrayOf(0)) }
        assertFails { open(value = wrapped.copyOf(79)) }
        assertFails { open(value = byteArrayOf(1, 2, 3, 4, 5) + wrapped) }
    }

    @Test
    fun lowOrderPublicKeyAndInvalidKeyLengthsFailClosed() {
        for (invalid in
            listOf(ByteArray(32), ByteArray(32).also { it[0] = 1 }, ByteArray(31), ByteArray(33))) {
            assertFails {
                AndroidReceiveCrypto.sealSubmissionKey(invalid, slot, transfer, ByteArray(32))
            }
        }
        val recipient = AndroidReceiveCrypto.generateKeyPair()
        assertFails {
            AndroidReceiveCrypto.sealSubmissionKey(
                recipient.publicKey,
                slot,
                transfer,
                ByteArray(31),
            )
        }
    }

    @Test
    fun fullManifestEnvelopeRejectsTrailingCiphertextBeforeDecode() {
        val recipient = AndroidReceiveCrypto.generateKeyPair()
        val key = CryptoProvider.generateKey()
        val nonce = CryptoProvider.generateNonce()
        val wrapped =
            AndroidReceiveCrypto.sealSubmissionKey(recipient.publicKey, slot, transfer, key)
        val encrypted =
            nonce + CryptoProvider.encrypt(key, nonce, "{\"files\":[]}".encodeToByteArray())
        val envelope = ReceiveEnvelope.encode(wrapped, encrypted)
        val parsed = ReceiveEnvelope.decode(envelope + byteArrayOf(0))
        val recovered =
            AndroidReceiveCrypto.openSubmissionKey(
                recipient.privateKey,
                recipient.publicKey,
                slot,
                transfer,
                parsed.wrappedKey,
            )
        assertFails {
            CryptoProvider.decrypt(
                recovered,
                parsed.encryptedManifest.copyOfRange(0, 12),
                parsed.encryptedManifest.copyOfRange(12, parsed.encryptedManifest.size),
            )
        }
    }
}
