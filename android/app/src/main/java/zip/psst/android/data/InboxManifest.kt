package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
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
        uiRequire(it.size == 32) { message(R.string.l_invalid_receive_key_1fc927) }
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
                uiRequireNotNull(privateKey) {
                    message(R.string.l_receive_private_key_is_not_on_this_device_112180)
                },
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
    uiRequire(value.all { it in '0'..'9' }) {
        message(R.string.l_enter_a_whole_number_or_leave_the_limit_empty_d770b7)
    }
    return uiRequireNotNull(value.toIntOrNull()?.takeIf { it > 0 }) {
        message(R.string.l_use_a_limit_between_1_and_2147483647_a467c3)
    }
}
