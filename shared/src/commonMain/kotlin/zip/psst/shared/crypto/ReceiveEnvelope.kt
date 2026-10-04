package zip.psst.shared.crypto

import zip.psst.shared.model.TransferLimits

/** The receive-v2 framing carries only a wrapped submission key and encrypted manifest. */
object ReceiveEnvelope {
    const val WRAPPED_KEY_BYTES = 80
    const val HEADER_BYTES = 88
    const val MIN_BYTES = HEADER_BYTES + 12 + 16
    const val MAX_BYTES = TransferLimits.MAX_MANIFEST_BYTES
    private val magic = "PSSTRCV2".encodeToByteArray()
    private val contextPrefix = "psst.zip/receive-key/v2\u0000".encodeToByteArray()

    /** Expected identities come from the caller's slot/submission, never from the envelope. */
    @Throws(Exception::class)
    fun info(slotId: String, submissionId: String, recipientPublicKey: ByteArray): ByteArray {
        require(recipientPublicKey.size == 32) { "Invalid receive public key length" }
        return contextPrefix +
            byteArrayOf(0, 0x20, 0, 1, 0, 2) +
            uuidBytes(slotId) +
            uuidBytes(submissionId) +
            recipientPublicKey
    }

    @Throws(Exception::class)
    fun encode(wrappedKey: ByteArray, encryptedManifest: ByteArray): ByteArray {
        require(wrappedKey.size == WRAPPED_KEY_BYTES) { "Invalid wrapped receive key length" }
        require(encryptedManifest.size in (MIN_BYTES - HEADER_BYTES)..(MAX_BYTES - HEADER_BYTES)) {
            "Invalid receive manifest length"
        }
        return magic + wrappedKey + encryptedManifest
    }

    @Throws(Exception::class)
    fun decode(bytes: ByteArray): ReceiveEnvelopeParts {
        require(bytes.size in MIN_BYTES..MAX_BYTES) { "Invalid receive envelope length" }
        require(bytes.copyOfRange(0, magic.size).contentEquals(magic)) {
            "Unsupported receive envelope version"
        }
        return ReceiveEnvelopeParts(
            bytes.copyOfRange(magic.size, HEADER_BYTES),
            bytes.copyOfRange(HEADER_BYTES, bytes.size),
        )
    }

    private fun uuidBytes(value: String): ByteArray {
        require(
            value.matches(Regex("[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"))
        ) {
            "Invalid canonical receive identifier"
        }
        val hex = value.replace("-", "")
        return ByteArray(16) { hex.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
    }
}

class ReceiveEnvelopeParts(val wrappedKey: ByteArray, val encryptedManifest: ByteArray)
