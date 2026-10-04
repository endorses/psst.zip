package zip.psst.android.data

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import zip.psst.shared.model.ServerOrigin
import zip.psst.shared.model.UrlHelper
import java.io.File
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Receive private keys never enter Room, navigation, invitation links or device pairing. */
class InboxKeyStore(context: Context) {
    private val directory = File(context.filesDir, "inbox-private-keys").apply { mkdirs() }

    @Synchronized
    fun save(origin: String, account: String, slot: String, privateKey: ByteArray) {
        require(privateKey.size == 32)
        val scope = inboxKeyScope(origin, account, slot)
        val cipher =
            Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key()) }
        cipher.updateAAD(scope)
        val file = file(scope)
        val output = file.startWrite()
        try {
            output.write(cipher.iv + cipher.doFinal(privateKey))
            output.fd.sync()
            file.finishWrite(output)
        } catch (error: Exception) {
            file.failWrite(output)
            throw error
        }
    }

    @Synchronized
    fun read(row: TransferHistoryEntity): ByteArray? {
        if (!row.encryptionKey.startsWith("v2.")) return null
        val account = row.accountId ?: return null
        return runCatching {
                val scope = inboxKeyScope(row.serverUrl, account, row.id)
                val bytes = file(scope).readFully()
                require(bytes.size == 60)
                val cipher =
                    Cipher.getInstance("AES/GCM/NoPadding").apply {
                        init(
                            Cipher.DECRYPT_MODE,
                            key(),
                            GCMParameterSpec(128, bytes.copyOfRange(0, 12)),
                        )
                    }
                cipher.updateAAD(scope)
                cipher.doFinal(bytes.copyOfRange(12, bytes.size))
            }
            .getOrNull()
    }

    @Synchronized
    fun delete(row: TransferHistoryEntity) {
        row.accountId?.let { file(inboxKeyScope(row.serverUrl, it, row.id)).delete() }
    }

    private fun file(scope: ByteArray) =
        AtomicFile(
            File(
                directory,
                MessageDigest.getInstance("SHA-256").digest(scope).joinToString("") {
                    "%02x".format(it)
                } + ".key",
            )
        )

    private fun key(): SecretKey =
        synchronized(KEY_LOCK) {
            val keys = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
            (keys.getKey(ALIAS, null) as? SecretKey)?.let {
                return@synchronized it
            }
            KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
                .apply {
                    init(
                        KeyGenParameterSpec.Builder(
                                ALIAS,
                                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
                            )
                            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                            .build()
                    )
                }
                .generateKey()
        }

    private companion object {
        const val ALIAS = "psst_receive_private_keys_v2"
        val KEY_LOCK = Any()
    }
}

internal fun inboxKeyScope(origin: String, account: String, slot: String): ByteArray {
    val normalized = requireNotNull(ServerOrigin.normalize(origin))
    require(account.isNotBlank() && '\u0000' !in account && UrlHelper.isResourceId(slot))
    return "$normalized\u0000$account\u0000${slot.lowercase()}".encodeToByteArray()
}
