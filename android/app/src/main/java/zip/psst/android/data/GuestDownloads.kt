package zip.psst.android.data

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import zip.psst.shared.model.FileMetadata
import java.io.File
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

@Serializable
data class SavedGuestFile(
    val blobId: String,
    val uri: String,
    val name: String,
    val size: Long,
    val mimeType: String,
    val sha256: String = "",
    val temporaryName: String? = null,
)

@Serializable
data class GuestDownload(
    val identity: String,
    val origin: String,
    val transferId: String,
    val createdAt: Long = System.currentTimeMillis(),
    val files: List<FileMetadata> = emptyList(),
    val saved: List<SavedGuestFile> = emptyList(),
    val pending: SavedGuestFile? = null,
    val complete: Boolean = false,
    val receiptPending: Boolean = false,
)

@Serializable
data class GuestReceipt(val identity: String, val origin: String, val transferId: String)

@Serializable
data class GuestUploadCleanup(val identity: String, val origin: String, val transferId: String)

/** Independent of account history: removal can never revoke a remote transfer. */
class GuestDownloadStore(context: Context) {
    private val directory = File(context.filesDir, "guest-downloads").apply { mkdirs() }
    private val json = Json {
        ignoreUnknownKeys = true
        encodeDefaults = true
    }

    @Synchronized
    fun all(): List<GuestDownload> =
        entries("json").map { read(it.nameWithoutExtension) }.sortedByDescending { it.createdAt }

    @Synchronized
    fun read(identity: String): GuestDownload =
        json.decodeFromString(
            AtomicFile(File(directory, "$identity.json")).readFully().decodeToString()
        )

    @Synchronized
    fun save(record: GuestDownload) {
        val file = AtomicFile(File(directory, "${record.identity}.json"))
        writeAtomic(file, json.encodeToString(record).encodeToByteArray())
    }

    @Synchronized
    fun open(origin: String, transferId: String, key: ByteArray): GuestDownload {
        val id = identity(origin, transferId)
        if (File(directory, "$id.json").exists() || File(directory, "$id.json.bak").exists()) {
            val existing = read(id)
            if (!MessageDigest.isEqual(readKey(id), key)) {
                require(canReplaceGuestKey(existing)) { "This saved transfer has a different key" }
                writeKey(id, key)
            }
            return existing
        }
        writeKey(id, key)
        return GuestDownload(id, origin, transferId).also(::save)
    }

    @Synchronized
    fun remove(identity: String) {
        val record = read(identity)
        if (record.receiptPending) queueReceipt(record)
        deleteAtomic(File(directory, "$identity.json"))
        deleteAtomic(File(directory, "$identity.key"))
    }

    fun readKey(identity: String): ByteArray {
        val bytes = AtomicFile(File(directory, "$identity.key")).readFully()
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(
            Cipher.DECRYPT_MODE,
            deviceKey(),
            GCMParameterSpec(128, bytes.copyOfRange(0, 12)),
        )
        cipher.updateAAD(identity.encodeToByteArray())
        return cipher.doFinal(bytes.copyOfRange(12, bytes.size))
    }

    @Synchronized
    fun queueReceipt(record: GuestDownload) {
        val receipt = GuestReceipt(record.identity, record.origin, record.transferId)
        val file = AtomicFile(File(directory, "${record.identity}.receipt"))
        writeAtomic(file, json.encodeToString(receipt).encodeToByteArray())
    }

    @Synchronized
    fun receipts(): List<GuestReceipt> =
        entries("receipt").map {
            json.decodeFromString(AtomicFile(it).readFully().decodeToString())
        }

    @Synchronized
    fun finishReceipt(identity: String) {
        deleteAtomic(File(directory, "$identity.receipt"))
    }

    @Synchronized
    fun queueUpload(origin: String, transferId: String, token: String): GuestUploadCleanup {
        val entry = GuestUploadCleanup("upload-" + identity(origin, transferId), origin, transferId)
        writeKey(entry.identity, token.encodeToByteArray())
        val file = AtomicFile(File(directory, "${entry.identity}.upload"))
        writeAtomic(file, json.encodeToString(entry).encodeToByteArray())
        return entry
    }

    @Synchronized
    fun uploads(): List<GuestUploadCleanup> =
        entries("upload").map { json.decodeFromString(AtomicFile(it).readFully().decodeToString()) }

    @Synchronized
    fun finishUpload(entry: GuestUploadCleanup) {
        deleteAtomic(File(directory, "${entry.identity}.upload"))
        deleteAtomic(File(directory, "${entry.identity}.key"))
    }

    private fun writeKey(identity: String, key: ByteArray) {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, deviceKey())
        cipher.updateAAD(identity.encodeToByteArray())
        val file = AtomicFile(File(directory, "$identity.key"))
        writeAtomic(file, cipher.iv + cipher.doFinal(key))
    }

    private fun entries(extension: String): List<File> =
        guestAtomicNames(directory.listFiles().orEmpty().map { it.name }, extension).map {
            File(directory, it)
        }

    private fun deleteAtomic(file: File) {
        AtomicFile(file).delete()
        check(
            !file.exists() &&
                !File(file.path + ".bak").exists() &&
                !File(file.path + ".new").exists()
        ) {
            "Could not remove local transfer metadata"
        }
    }

    private fun writeAtomic(file: AtomicFile, bytes: ByteArray) {
        val stream = file.startWrite()
        try {
            stream.write(bytes)
            stream.fd.sync()
            file.finishWrite(stream)
        } catch (e: Exception) {
            file.failWrite(stream)
            throw e
        }
        check(file.readFully().contentEquals(bytes)) { "Could not persist local transfer metadata" }
    }

    private fun deviceKey(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey("psst_guest_downloads_v1", null) as? SecretKey)?.let {
            return it
        }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
            .apply {
                init(
                    KeyGenParameterSpec.Builder(
                            "psst_guest_downloads_v1",
                            KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
                        )
                        .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                        .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                        .build()
                )
            }
            .generateKey()
    }

    companion object {
        fun identity(origin: String, transferId: String): String =
            MessageDigest.getInstance("SHA-256")
                .digest("$origin/d/$transferId".encodeToByteArray())
                .joinToString("") { "%02x".format(it) }
    }
}

internal fun canReplaceGuestKey(record: GuestDownload): Boolean =
    record.files.isEmpty() && record.saved.isEmpty() && record.pending == null

/** Older Android AtomicFile revisions can leave only the backup after an interrupted rename. */
internal fun guestAtomicNames(names: List<String>, extension: String): List<String> =
    names.map { it.removeSuffix(".bak") }.filter { it.endsWith(".$extension") }.distinct()
