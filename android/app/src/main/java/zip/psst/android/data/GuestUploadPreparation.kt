package zip.psst.android.data

import zip.psst.android.R
import zip.psst.android.i18n.*
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
    uiRequire(selection.size <= TransferLimits.MAX_FILES) {
        message(R.string.l_select_at_most_100_files_remove_files_before_adding_more_ac3446)
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
    uiRequire(sources.size in 1..TransferLimits.MAX_FILES) {
        message(R.string.l_select_between_1_and_100_files_808241)
    }
    uiRequire(maxFileBytes in 1..ChunkedFileCrypto.MAX_FILE_SIZE) {
        message(R.string.l_this_server_cannot_accept_the_selected_files_e648fe)
    }
    val initialSpace = availableSpace()
    uiRequire(initialSpace >= UPLOAD_DISK_RESERVE) {
        message(R.string.l_more_local_storage_is_needed_to_prepare_these_files_free_space_an_baec9b)
    }
    val budget = minOf(MAX_SPOOL_BYTES, initialSpace - UPLOAD_DISK_RESERVE)
    var totalPlain = 0L
    val files = mutableListOf<PreparedGuestFile>()
    try {
        for (source in sources) {
            uiRequire(source.mimeType.length <= 255 && source.name.length <= 1024) {
                message(R.string.ui_local_selected_invalid)
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
                        uiRequire(count > 0) {
                            message(
                                R.string
                                    .l_the_selected_document_provider_stopped_responding_try_selecting_t_7d427a
                            )
                        }
                        uiRequire(
                            count.toLong() <= budget - totalPlain &&
                                availableSpace() >= UPLOAD_DISK_RESERVE + count
                        ) {
                            message(
                                R.string
                                    .l_more_local_storage_is_needed_to_prepare_these_files_free_space_or_650a7e
                            )
                        }
                        uiRequire(length <= ChunkedFileCrypto.MAX_FILE_SIZE - count) {
                            message(R.string.l_selected_file_is_too_large_561cb5)
                        }
                        length += count
                        uiRequire(length <= maxFileBytes) {
                            message(R.string.ui_selected_limit_exceeded)
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
    uiRequire(prepared.files.all { it.snapshot.length() <= maxFileBytes }) {
        message(R.string.l_the_server_s_file_limit_changed_remove_oversized_files_and_try_ag_19f9f5)
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
    uiRequire(manifestBytes <= uiRequireNotNull(policy.uploadCapacity).manifestReserveBytes) {
        message(R.string.l_the_selected_files_details_exceed_this_server_s_manifest_limit_se_05f591)
    }
    return policy
}
