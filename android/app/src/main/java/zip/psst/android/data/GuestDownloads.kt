package zip.psst.android.data

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import zip.psst.shared.model.FileMetadata
import java.io.File
import java.nio.file.DirectoryStream
import java.nio.file.Files
import java.nio.file.Path
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

    private val index = GuestHistoryIndex(context.applicationContext)
    private var legacyStream: DirectoryStream<Path>? = null
    private var legacyIterator: Iterator<Path>? = null
    private var importDone = false

    /** Each call examines at most 64 directory entries, including keys and AtomicFile sidecars. */
    @Synchronized
    fun importLegacyBatch(): Boolean {
        if (importDone || index.importComplete()) {
            importDone = true
            return false
        }
        if (legacyIterator == null) {
            legacyStream = Files.newDirectoryStream(directory.toPath())
            legacyIterator = legacyStream!!.iterator()
        }
        val iterator = legacyIterator!!
        val more =
            iterator.visitGuestImportBatch { path ->
                val entry = guestLegacyEntry(path.fileName.toString())
                if (entry != null) {
                    val (kind, id) = entry
                    if (!index.contains(kind, id)) {
                        try {
                            importRecord(kind, id)
                        } catch (error: java.io.IOException) {
                            index.put(kind, id, null, importing = true)
                        } catch (error: IllegalArgumentException) {
                            // Preserve the original metadata and key; later entries remain
                            // browseable.
                            index.put(kind, id, null, importing = true)
                        }
                    }
                }
            }
        if (!more) {
            legacyStream?.close()
            legacyStream = null
            legacyIterator = null
            index.finishImport()
            importDone = true
        }
        return more
    }

    @Synchronized
    fun page(after: LocalHistoryCursor?): GuestHistoryPage {
        val rows = index.page(after)
        val shown = rows.take(50)
        return GuestHistoryPage(
            shown.mapNotNull {
                it.payload?.let { value -> json.decodeFromString<GuestDownload>(value) }
            },
            shown
                .lastOrNull()
                ?.takeIf { rows.size > 50 }
                ?.let { LocalHistoryCursor(it.createdAt, it.identity) },
            !importDone,
            index.hasImportErrors(),
        )
    }

    @Synchronized
    fun read(identity: String): GuestDownload {
        require(validIdentity(identity))
        if (!index.contains("json", identity)) importRecord("json", identity)
        return json.decodeFromString(
            requireNotNull(index.read("json", identity)) {
                "Local transfer metadata is unavailable; any original metadata file is retained"
            }
        )
    }

    @Synchronized
    fun save(record: GuestDownload) {
        require(validIdentity(record.identity))
        index.transaction {
            check(!index.isDeleted("json", record.identity)) {
                "This local history record was removed"
            }
            persist(record)
        }
    }

    private fun persist(record: GuestDownload) {
        index.put(
            "json",
            record.identity,
            json.encodeToString(record),
            record.createdAt,
            record.complete && record.receiptPending,
        )
    }

    /** Update only receipt state, retaining any newer saved-file checkpoint. */
    @Synchronized
    fun markReceiptSent(identity: String): GuestDownload {
        lateinit var updated: GuestDownload
        index.transaction {
            updated = read(identity).copy(receiptPending = false)
            save(updated)
        }
        return updated
    }

    private fun importRecord(kind: String, id: String) {
        val payload =
            AtomicFile(File(directory, "$id.$kind")).openRead().use { it.readGuestRecord() }
        when (kind) {
            "json" -> {
                val record = json.decodeFromString<GuestDownload>(payload)
                require(record.identity == id)
                index.put(
                    kind,
                    id,
                    payload,
                    record.createdAt,
                    record.complete && record.receiptPending,
                    importing = true,
                )
            }
            "receipt" -> {
                require(json.decodeFromString<GuestReceipt>(payload).identity == id)
                index.put(kind, id, payload, importing = true)
            }
            "upload" -> {
                require(json.decodeFromString<GuestUploadCleanup>(payload).identity == id)
                index.put(kind, id, payload, importing = true)
            }
        }
    }

    @Synchronized
    fun open(origin: String, transferId: String, key: ByteArray): GuestDownload {
        val id = identity(origin, transferId)
        if (
            (index.contains("json", id) && !index.isDeleted("json", id)) ||
                (!index.contains("json", id) &&
                    (File(directory, "$id.json").exists() ||
                        File(directory, "$id.json.bak").exists()))
        ) {
            val existing = read(id)
            if (!MessageDigest.isEqual(readKey(id), key)) {
                require(canReplaceGuestKey(existing)) { "This saved transfer has a different key" }
                writeKey(id, key)
            }
            return existing
        }
        writeKey(id, key)
        return GuestDownload(id, origin, transferId).also(::persist)
    }

    @Synchronized
    fun remove(identity: String) {
        val record = read(identity)
        index.transaction {
            if (record.receiptPending) queueReceipt(record)
            index.tombstone("json", identity)
        }
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
        index.put("receipt", record.identity, json.encodeToString(receipt))
    }

    @Synchronized
    fun receipts(after: String? = null): List<GuestReceipt> =
        index.queue("receipt", after).mapNotNull {
            it.payload?.let { value -> json.decodeFromString<GuestReceipt>(value) }
        }

    @Synchronized
    fun pendingDownloads(after: String? = null): List<GuestDownload> =
        index.queue("json", after, pendingOnly = true).mapNotNull {
            it.payload?.let { value -> json.decodeFromString<GuestDownload>(value) }
        }

    fun hasReceipts(): Boolean =
        index.hasPending("receipt") || index.hasPending("json", pendingOnly = true)

    fun hasUploads(): Boolean = index.hasPending("upload")

    @Synchronized
    fun finishReceipt(identity: String) {
        index.tombstone("receipt", identity)
        deleteAtomic(File(directory, "$identity.receipt"))
    }

    @Synchronized
    fun queueUpload(origin: String, transferId: String, token: String): GuestUploadCleanup {
        val entry = GuestUploadCleanup("upload-" + identity(origin, transferId), origin, transferId)
        writeKey(entry.identity, token.encodeToByteArray())
        index.put("upload", entry.identity, json.encodeToString(entry))
        return entry
    }

    @Synchronized
    fun uploads(after: String? = null): List<GuestUploadCleanup> =
        index.queue("upload", after).mapNotNull {
            it.payload?.let { value -> json.decodeFromString<GuestUploadCleanup>(value) }
        }

    @Synchronized
    fun finishUpload(entry: GuestUploadCleanup) {
        index.tombstone("upload", entry.identity)
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

    @Synchronized
    fun close() {
        legacyStream?.close()
        legacyStream = null
        legacyIterator = null
        index.close()
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

internal fun validIdentity(identity: String): Boolean =
    identity.matches(Regex("(?:upload-)?[a-f0-9]{64}"))

data class GuestHistoryPage(
    val records: List<GuestDownload>,
    val next: LocalHistoryCursor?,
    val importing: Boolean,
    val importErrors: Boolean,
)
