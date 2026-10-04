package zip.psst.android.data

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Device credentials are encrypted with a non-exportable Android Keystore key. */
class SessionStorage(context: Context) {
    private val prefs = context.getSharedPreferences("psst_device_session", Context.MODE_PRIVATE)

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(ALIAS, null) as? SecretKey)?.let {
            return it
        }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
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

    fun save(
        serverUrl: String,
        username: String,
        token: String,
        accountId: String,
        role: String,
        mustChangePassword: Boolean = false,
    ) {
        val cipher =
            Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key()) }
        cipher.updateAAD(serverUrl.trimEnd('/').toByteArray())
        val encrypted = cipher.doFinal(token.toByteArray())
        check(
            prefs
                .edit()
                .putString("origin", serverUrl.trimEnd('/'))
                .putString("username", username)
                .putString("account_id", accountId)
                .putString("role", role)
                .putBoolean("must_change_password", mustChangePassword)
                .putString("ciphertext", Base64.encodeToString(encrypted, Base64.NO_WRAP))
                .putString("iv", Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
                .commit()
        ) {
            "Could not save the device session"
        }
    }

    fun token(serverUrl: String): String? {
        if (prefs.getString("origin", null) != serverUrl.trimEnd('/')) return null
        val encrypted = prefs.getString("ciphertext", null) ?: return null
        return try {
            val cipher =
                Cipher.getInstance("AES/GCM/NoPadding").apply {
                    init(
                        Cipher.DECRYPT_MODE,
                        key(),
                        GCMParameterSpec(
                            128,
                            Base64.decode(prefs.getString("iv", ""), Base64.NO_WRAP),
                        ),
                    )
                }
            cipher.updateAAD(serverUrl.trimEnd('/').toByteArray())
            String(cipher.doFinal(Base64.decode(encrypted, Base64.NO_WRAP)))
        } catch (_: Exception) {
            clear()
            null
        }
    }

    fun accountId(): String? = prefs.getString("account_id", null)

    fun isAdmin(): Boolean = prefs.getString("role", null) == "admin"

    fun mustChangePassword(): Boolean = prefs.getBoolean("must_change_password", false)

    fun setPasswordChangeRequired(required: Boolean) {
        prefs.edit().putBoolean("must_change_password", required).commit()
    }

    fun username(): String = prefs.getString("username", "") ?: ""

    fun clear() {
        prefs.edit().clear().commit()
    }

    private companion object {
        const val ALIAS = "psst_device_session_v1"
    }
}
