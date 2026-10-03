package zip.psst.android.data

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import androidx.core.content.FileProvider
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.ManifestValidator
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest
import java.util.UUID
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive

/** Journal before writing; incomplete outputs are removed before retry. */
class GuestFileSaver(private val context: Context) {
    fun exists(file: SavedGuestFile): Boolean =
        try {
            context.contentResolver.openFileDescriptor(Uri.parse(file.uri), "r")?.use {
                it.statSize == file.size
            } ?: false
        } catch (_: Exception) {
            false
        }

    fun published(file: SavedGuestFile): Boolean =
        try {
            val uri = Uri.parse(file.uri)
            val visible =
                if (Build.VERSION.SDK_INT >= 29)
                    context.contentResolver
                        .query(uri, arrayOf(MediaStore.Downloads.IS_PENDING), null, null, null)
                        ?.use { it.moveToFirst() && it.getInt(0) == 0 } == true
                else
                    File(directory(), file.name).isFile &&
                        !File(directory(), file.temporaryName ?: ".${file.name}.partial").exists()
            visible &&
                exists(file) &&
                file.sha256.isNotEmpty() &&
                context.contentResolver.openInputStream(uri)?.use { input ->
                    val digest = MessageDigest.getInstance("SHA-256")
                    val buffer = ByteArray(8192)
                    while (true) {
                        val count = input.read(buffer)
                        if (count < 0) break
                        digest.update(buffer, 0, count)
                    }
                    digest.digest().joinToString("") { "%02x".format(it) } == file.sha256
                } == true
        } catch (_: Exception) {
            false
        }

    fun discard(file: SavedGuestFile) {
        val uri = Uri.parse(file.uri)
        if (Build.VERSION.SDK_INT >= 29) {
            context.contentResolver.delete(uri, null, null)
        } else {
            // Final names are never partially written. Do not delete another application's
            // pre-existing collision when recovering a failed no-replace move.
            File(directory(), file.temporaryName ?: ".${file.name}.partial").delete()
        }
    }

    suspend fun save(
        file: FileMetadata,
        bytes: ByteArray,
        journal: (SavedGuestFile) -> Unit,
    ): SavedGuestFile {
        val hash =
            MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") {
                "%02x".format(it)
            }
        val name = ManifestValidator.safeFilename(file.name)
        if (Build.VERSION.SDK_INT >= 29) {
            val values =
                ContentValues().apply {
                    put(MediaStore.Downloads.DISPLAY_NAME, name)
                    put(MediaStore.Downloads.MIME_TYPE, file.mimeType)
                    put(MediaStore.Downloads.RELATIVE_PATH, "Download/psst.zip")
                    put(MediaStore.Downloads.IS_PENDING, 1)
                }
            val uri =
                checkNotNull(
                    context.contentResolver.insert(
                        MediaStore.Downloads.EXTERNAL_CONTENT_URI,
                        values,
                    )
                )
            val actualName =
                runCatching {
                        context.contentResolver
                            .query(
                                uri,
                                arrayOf(MediaStore.Downloads.DISPLAY_NAME),
                                null,
                                null,
                                null,
                            )
                            ?.use { if (it.moveToFirst()) it.getString(0) else name }
                    }
                    .getOrNull() ?: name
            val output =
                SavedGuestFile(
                    file.blobId,
                    uri.toString(),
                    actualName,
                    file.size,
                    file.mimeType,
                    hash,
                )
            try {
                journal(output)
                context.contentResolver.openFileDescriptor(uri, "w")!!.use { descriptor ->
                    FileOutputStream(descriptor.fileDescriptor).use { stream ->
                        write(bytes, stream)
                        stream.fd.sync()
                    }
                }
                currentCoroutineContext().ensureActive()
                check(
                    context.contentResolver.update(
                        uri,
                        ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) },
                        null,
                        null,
                    ) == 1
                )
                return output
            } catch (e: Exception) {
                context.contentResolver.delete(uri, null, null)
                throw e
            }
        }
        val folder = directory()
        val temporary = File(folder, ".psst-${UUID.randomUUID()}.partial")
        fun output(destination: File) =
            SavedGuestFile(
                file.blobId,
                FileProvider.getUriForFile(context, "${context.packageName}.downloads", destination)
                    .toString(),
                destination.name,
                file.size,
                file.mimeType,
                hash,
                temporary.name,
            )
        var result = output(File(folder, name))
        try {
            journal(result)
            check(temporary.createNewFile())
            FileOutputStream(temporary).use {
                write(bytes, it)
                it.fd.sync()
            }
            currentCoroutineContext().ensureActive()
            publishGuestFile(temporary, folder, name) { destination ->
                result = output(destination)
                journal(result)
            }
            return result
        } catch (e: Exception) {
            temporary.delete()
            throw e
        }
    }

    private suspend fun write(bytes: ByteArray, stream: FileOutputStream) {
        var offset = 0
        while (offset < bytes.size) {
            currentCoroutineContext().ensureActive()
            val count = minOf(64 * 1024, bytes.size - offset)
            stream.write(bytes, offset, count)
            offset += count
        }
    }

    private fun directory() =
        File(
                Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
                "psst.zip",
            )
            .apply { check(isDirectory || mkdirs()) }
}
