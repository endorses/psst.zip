package zip.psst.android.data

import android.content.Context
import android.net.Uri
import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.model.FileMetadata
import java.io.File
import java.io.InputStream
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive

/** A bounded plaintext producer; callers must publish only after it returns successfully. */
internal typealias FileContent = suspend ((ByteArray) -> Unit) -> Unit

internal fun downloadedContent(
    client: ApiClient,
    transferId: String,
    file: FileMetadata,
    key: ByteArray,
    onProgress: (Long, Long?) -> Unit = { _, _ -> },
): FileContent = { write ->
    require(file.encoding == "chunked-v1") { "Unsupported file encryption" }
    require(file.chunkSize == 4 * 1024 * 1024)
    val encryptionId = requireNotNull(file.encryptionId)
    val wireSize = ChunkedFileCrypto.wireSize(file.size)
    val coroutine = currentCoroutineContext()
    var index = 0L
    var received = 0L
    client.transfers.downloadFileChunks(transferId, file.blobId, wireSize, 4 * 1024 * 1024 + 60) {
        frame ->
        coroutine.ensureActive()
        val plain = ChunkedFileCrypto.decrypt(key, encryptionId, file.size, index, frame)
        write(plain)
        received += frame.size
        index++
        onProgress(received, wireSize)
        true
    }
}

/**
 * Snapshot a document to private disk so length and bytes cannot change during streaming upload.
 */
internal suspend fun spoolUpload(context: Context, uri: Uri, maxBytes: Long): File {
    val file = File.createTempFile("psst-upload-", ".partial", context.cacheDir)
    try {
        context.contentResolver.openInputStream(uri)?.use { input ->
            file.outputStream().use { output ->
                var length = 0L
                val buffer = ByteArray(64 * 1024)
                while (true) {
                    currentCoroutineContext().ensureActive()
                    val count = input.read(buffer)
                    if (count < 0) break
                    length += count
                    require(length <= maxBytes) { "File exceeds this server’s per-file limit" }
                    output.write(buffer, 0, count)
                }
            }
        } ?: error("Cannot read selected file")
        return file
    } catch (error: Throwable) {
        file.delete()
        throw error
    }
}

internal suspend fun uploadChunkedFile(
    client: ApiClient,
    transferId: String,
    file: File,
    name: String,
    mimeType: String,
    key: ByteArray,
    onProgress: (Long) -> Unit = {},
): FileMetadata {
    val encryptionId = ChunkedFileCrypto.createId()
    val length = file.length()
    val wireSize = ChunkedFileCrypto.wireSize(length)
    val resource = client.createFileUpload(transferId, wireSize)
    var offset = 0L
    var index = 0L
    file.inputStream().use { input ->
        do {
            currentCoroutineContext().ensureActive()
            val plain =
                readChunk(input, minOf(4 * 1024 * 1024L, length - index * 4 * 1024 * 1024).toInt())
            val encrypted = ChunkedFileCrypto.encrypt(key, encryptionId, length, index, plain)
            client.tus.uploadChunk(resource, encrypted, offset)
            offset += encrypted.size
            onProgress(offset)
            index++
        } while (offset < wireSize)
        require(input.read() == -1) { "Selected file changed during upload" }
    }
    return FileMetadata(
        name,
        length,
        mimeType,
        resource.substringAfterLast('/'),
        "chunked-v1",
        4 * 1024 * 1024,
        encryptionId,
    )
}

private suspend fun readChunk(input: InputStream, count: Int): ByteArray {
    val bytes = ByteArray(count)
    var offset = 0
    while (offset < count) {
        currentCoroutineContext().ensureActive()
        val read = input.read(bytes, offset, count - offset)
        require(read >= 0) { "Selected file is incomplete" }
        offset += read
    }
    return bytes
}
