package zip.psst.android.data

import zip.psst.shared.crypto.AndroidReceiveCrypto
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.crypto.ReceiveEnvelope
import zip.psst.shared.model.EncryptedManifest
import zip.psst.shared.model.Manifest
import zip.psst.shared.model.ManifestValidator
import kotlin.io.encoding.Base64
import kotlinx.serialization.json.Json

internal fun decodeInboxKeyMarker(value: String): ByteArray =
    Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).decode(value.removePrefix("v2.")).also {
        require(it.size == 32) { "Invalid receive key" }
    }

internal class InboxManifest(val key: ByteArray, val manifest: Manifest)

internal fun decryptInboxManifest(
    row: TransferHistoryEntity,
    transferId: String,
    bytes: ByteArray,
    privateKey: ByteArray?,
): InboxManifest {
    val storedKey = decodeInboxKeyMarker(row.encryptionKey)
    val (key, encryptedBytes) =
        if (row.encryptionKey.startsWith("v2.")) {
            val envelope = ReceiveEnvelope.decode(bytes)
            AndroidReceiveCrypto.openSubmissionKey(
                requireNotNull(privateKey) { "Receive private key is not on this device" },
                storedKey,
                row.id,
                transferId,
                envelope.wrappedKey,
            ) to envelope.encryptedManifest
        } else
            storedKey to bytes // Owner-only legacy reads; never used to produce a new invitation.
    val encrypted = EncryptedManifest.fromBytes(encryptedBytes)
    val manifest =
        Json.decodeFromString<Manifest>(
            CryptoProvider.decrypt(key, encrypted.nonce, encrypted.ciphertext).decodeToString()
        )
    ManifestValidator.validate(manifest)
    return InboxManifest(key, manifest)
}

internal fun optionalLinkLimit(value: String): Int {
    if (value.isBlank()) return 0
    require(value.all { it in '0'..'9' }) { "Enter a whole number or leave the limit empty" }
    return requireNotNull(value.toIntOrNull()?.takeIf { it > 0 }) {
        "Use a limit between 1 and 2147483647"
    }
}
