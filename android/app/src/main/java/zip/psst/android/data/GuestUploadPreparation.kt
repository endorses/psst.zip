package zip.psst.android.data

import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.crypto.ReceiveEnvelope
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.GuestUploadCapacity
import zip.psst.shared.model.Manifest
import zip.psst.shared.model.SlotAvailability
import zip.psst.shared.model.TransferLimits
import java.io.Closeable
import java.io.File
import java.io.InputStream
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

internal const val UPLOAD_DISK_RESERVE = 256L * 1024 * 1024
private const val MAX_SPOOL_BYTES = TransferLimits.MAX_FILES * ChunkedFileCrypto.MAX_FILE_SIZE

internal data class GuestUploadSource(
    val name: String,
    val mimeType: String,
    val open: () -> InputStream,
)

internal data class PreparedGuestFile(val snapshot: File, val name: String, val mimeType: String) {
    val wireBytes: Long
        get() = ChunkedFileCrypto.wireSize(snapshot.length())
}

internal class PreparedGuestUpload(val files: List<PreparedGuestFile>) : Closeable {
    val totalWireBytes: Long
        get() = GuestUploadCapacity.totalWireBytes(files.map { it.snapshot.length() })

    override fun close() {
        files.forEach { it.snapshot.delete() }
    }
}

internal fun <T> appendGuestSelection(previous: List<T>, added: List<T>): List<T> {
    val selection = (previous + added).distinct()
    require(selection.size <= TransferLimits.MAX_FILES) {
        "Select at most 100 files. Remove files before adding more."
    }
    return selection
}

/** Provider metadata is never trusted for admission: every byte is read once into private disk. */
internal suspend fun prepareGuestUpload(
    sources: List<GuestUploadSource>,
    directory: File,
    maxFileBytes: Long,
    availableSpace: () -> Long = { directory.usableSpace },
): PreparedGuestUpload {
    require(sources.size in 1..TransferLimits.MAX_FILES) { "Select between 1 and 100 files" }
    require(maxFileBytes in 1..ChunkedFileCrypto.MAX_FILE_SIZE) {
        "This server cannot accept the selected files"
    }
    val initialSpace = availableSpace()
    require(initialSpace >= UPLOAD_DISK_RESERVE) {
        "More local storage is needed to prepare these files. Free space and try again."
    }
    val budget = minOf(MAX_SPOOL_BYTES, initialSpace - UPLOAD_DISK_RESERVE)
    var totalPlain = 0L
    val files = mutableListOf<PreparedGuestFile>()
    try {
        for (source in sources) {
            require(source.mimeType.length <= 255 && source.name.length <= 1024) {
                "A selected document has invalid file details"
            }
            currentCoroutineContext().ensureActive()
            val snapshot = File.createTempFile("psst-guest-upload-", ".partial", directory)
            files += PreparedGuestFile(snapshot, source.name, source.mimeType)
            source.open().use { input ->
                snapshot.outputStream().use { output ->
                    val buffer = ByteArray(64 * 1024)
                    var length = 0L
                    while (true) {
                        currentCoroutineContext().ensureActive()
                        val count = input.read(buffer)
                        if (count < 0) break
                        require(count > 0) {
                            "The selected document provider stopped responding. Try selecting the file again."
                        }
                        require(
                            count.toLong() <= budget - totalPlain &&
                                availableSpace() >= UPLOAD_DISK_RESERVE + count
                        ) {
                            "More local storage is needed to prepare these files. Free space or remove files and try again."
                        }
                        require(length <= ChunkedFileCrypto.MAX_FILE_SIZE - count) {
                            "Selected file is too large"
                        }
                        length += count
                        require(length <= maxFileBytes) {
                            "A selected file exceeds this server's per-file limit"
                        }
                        output.write(buffer, 0, count)
                        totalPlain += count
                    }
                }
            }
        }
        return PreparedGuestUpload(files).also { it.totalWireBytes }
    } catch (error: Throwable) {
        files.forEach { it.snapshot.delete() }
        throw error
    }
}

/** Final refresh is separate from opening/picker advice and precedes any child allocation. */
internal suspend fun validatePreparedGuestUpload(
    prepared: PreparedGuestUpload,
    slotId: String,
    publicKey: ByteArray,
    refresh: suspend () -> Pair<SlotAvailability, Long>,
): SlotAvailability {
    val (policy, maxFileBytes) = refresh()
    require(prepared.files.all { it.snapshot.length() <= maxFileBytes }) {
        "The server's file limit changed. Remove oversized files and try again; your files are still selected."
    }
    policy.validateForSubmission(slotId, publicKey, prepared.files.size, prepared.totalWireBytes)
    // Blob IDs and encryption contexts have fixed lengths, so this matches the final wire size.
    val manifest =
        Manifest(
            prepared.files.map { file ->
                FileMetadata(
                    file.name,
                    file.snapshot.length(),
                    file.mimeType,
                    "00000000-0000-0000-0000-000000000000",
                    ChunkedFileCrypto.ENCODING,
                    ChunkedFileCrypto.CHUNK_SIZE,
                    "0".repeat(32),
                )
            }
        )
    val manifestBytes =
        Json.encodeToString(manifest).encodeToByteArray().size.toLong() + ReceiveEnvelope.MIN_BYTES
    require(manifestBytes <= requireNotNull(policy.uploadCapacity).manifestReserveBytes) {
        "The selected files' details exceed this server's manifest limit. Select fewer files and try again."
    }
    return policy
}
