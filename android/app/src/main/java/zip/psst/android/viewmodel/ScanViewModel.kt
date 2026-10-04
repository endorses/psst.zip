package zip.psst.android.viewmodel

import android.app.Application
import android.net.Uri
import android.provider.OpenableColumns
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.data.GuestDownload
import zip.psst.android.data.GuestDownloadConsent
import zip.psst.android.data.GuestDownloadPreflight
import zip.psst.android.data.GuestDownloadStore
import zip.psst.android.data.GuestFileSaver
import zip.psst.android.data.InsufficientDownloadSpaceException
import zip.psst.android.data.acknowledgeSavedDownload
import zip.psst.android.data.cleanupGuestUpload
import zip.psst.android.data.receiveGuestFiles
import zip.psst.android.data.reconcileGuestOutput
import zip.psst.android.data.resolveGuestUpload
import zip.psst.android.data.spoolUpload
import zip.psst.android.data.uploadChunkedFile
import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.model.*
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

data class ScanState(
    val origin: String = "",
    val kind: ScanInputKind? = null,
    val busy: Boolean = false,
    val stage: String = "",
    val bytes: Long = 0,
    val totalBytes: Long? = null,
    val fileIndex: Int = 0,
    val error: String? = null,
    val notice: String? = null,
    val record: GuestDownload? = null,
    val history: List<GuestDownload> = emptyList(),
    val uploadFiles: List<Uri> = emptyList(),
    val uploaded: Boolean = false,
    val maxFileBytes: Long? = null,
    val pendingCleanup: Int = 0,
    val pendingReceipts: Int = 0,
    val downloadConsent: GuestDownloadConsent? = null,
)

/** ViewModel retains only in-memory input; secret keys never enter navigation/saved bundles. */
class ScanViewModel(application: Application) : AndroidViewModel(application) {
    private val store = GuestDownloadStore(application)
    private val saver = GuestFileSaver(application)
    private val _state = MutableStateFlow(ScanState())
    val state = _state.asStateFlow()
    private var input: ScanInput? = null
    private var rawPairing: String? = null
    private var job: Job? = null

    init {
        refreshHistory()
    }

    fun refreshHistory() {
        try {
            _state.update {
                it.copy(
                    history = store.all(),
                    pendingCleanup = store.uploads().size,
                    pendingReceipts =
                        store.receipts().size + store.all().count { row -> row.receiptPending },
                )
            }
        } catch (_: Exception) {
            error(
                "Local received history could not be read. Your saved files remain in Downloads/psst.zip."
            )
        }
    }

    fun classify(raw: String): Boolean {
        if (job?.isActive == true) return false
        return try {
            val parsed = requireNotNull(ScanInputClassifier.classify(raw))
            input = parsed
            rawPairing = if (parsed.kind == ScanInputKind.PAIRING) raw else null
            _state.value =
                ScanState(
                    origin = parsed.link?.origin ?: parsed.pairing!!.serverUrl,
                    kind = parsed.kind,
                    history = _state.value.history,
                    pendingCleanup = _state.value.pendingCleanup,
                    pendingReceipts = _state.value.pendingReceipts,
                )
            true
        } catch (_: Exception) {
            error(
                "This is not a supported psst.zip QR code or link. Scan again or paste a valid link."
            )
            false
        }
    }

    fun pairingPayload(): String? = rawPairing

    fun accountConnected() {
        clear()
        _state.update {
            it.copy(notice = "Account connected. You can now send files and create receive links.")
        }
    }

    fun error(message: String) {
        _state.update { it.copy(error = message) }
    }

    fun cancel() {
        job?.cancel()
        _state.update { it.copy(downloadConsent = null) }
    }

    fun clear() {
        if (job?.isActive == true) return
        input = null
        rawPairing = null
        _state.value =
            ScanState(
                history = _state.value.history,
                pendingCleanup = _state.value.pendingCleanup,
                pendingReceipts = _state.value.pendingReceipts,
            )
    }

    fun open(record: GuestDownload): Boolean {
        if (job?.isActive == true) return false
        input = null
        try {
            var current = store.read(record.identity)
            current = reconcile(current)
            _state.update {
                it.copy(
                    origin = current.origin,
                    kind = ScanInputKind.DOWNLOAD,
                    record = current,
                    error = null,
                    downloadConsent = null,
                    stage =
                        if (current.complete) "Saved in Downloads/psst.zip"
                        else "Interrupted — ready to resume",
                )
            }
            if (current.receiptPending) retryReceipt()
            return true
        } catch (_: Exception) {
            error("Could not reopen this local record. Saved files remain in Downloads/psst.zip.")
            return false
        }
    }

    fun remove(record: GuestDownload) {
        if (job?.isActive == true) return
        try {
            reconcile(store.read(record.identity))
            store.remove(record.identity)
            clear()
            refreshHistory()
        } catch (_: Exception) {
            error("Could not remove local history.")
        }
    }

    fun missingFiles(): Boolean = _state.value.record?.saved?.any { !saver.exists(it) } == true

    fun fileExists(file: zip.psst.android.data.SavedGuestFile) = saver.exists(file)

    fun confirmDownload() {
        val consent = _state.value.downloadConsent ?: return
        receive(consent.redownloadMissing, consent)
    }

    fun dismissDownloadConsent() {
        _state.update { it.copy(downloadConsent = null, stage = "Receiving cancelled") }
    }

    fun receive(redownloadMissing: Boolean = false) = receive(redownloadMissing, null)

    private fun receive(redownloadMissing: Boolean, approved: GuestDownloadConsent?) {
        if (job?.isActive == true) return
        job =
            viewModelScope.launch(Dispatchers.IO) {
                _state.update {
                    it.copy(
                        busy = true,
                        error = null,
                        stage = "Inspecting transfer",
                        bytes = 0,
                        totalBytes = null,
                        downloadConsent = null,
                    )
                }
                var client: ApiClient? = null
                var record = GuestDownload("", "", "")
                try {
                    val link = input?.link
                    record =
                        if (link != null) store.open(link.origin, link.id, link.key)
                        else store.read(requireNotNull(_state.value.record).identity)
                    record = reconcile(record)
                    val existing = record.saved.filter(saver::exists)
                    if (existing.size != record.saved.size && !redownloadMissing) {
                        _state.update {
                            it.copy(
                                record = record,
                                stage = "Some saved files are missing",
                                error =
                                    "Choose Redownload missing files to fetch removed copies. The link may have expired or reached its download limit.",
                            )
                        }
                        return@launch
                    }
                    record =
                        record.copy(
                            saved = existing,
                            complete = record.complete && existing.size == record.files.size,
                        )
                    store.save(record)
                    _state.update { it.copy(record = record, origin = record.origin) }
                    client = ApiClient.anonymous(record.origin)
                    if (record.complete) {
                        record = sendReceipt(client, record)
                        return@launch
                    }
                    val key = store.readKey(record.identity)
                    run {
                        val transfer = client.transfers.get(record.transferId)
                        require(
                            transfer.id == record.transferId &&
                                transfer.status == TransferStatus.COMPLETE
                        )
                        val bytes = client.transfers.downloadManifest(record.transferId)
                        val encrypted = EncryptedManifest.fromBytes(bytes)
                        val manifest =
                            Json.decodeFromString<Manifest>(
                                CryptoProvider.decrypt(key, encrypted.nonce, encrypted.ciphertext)
                                    .decodeToString()
                            )
                        ManifestValidator.validateForTransfer(manifest, transfer)
                        require(record.saved.isEmpty() || record.files == manifest.files) {
                            "The file list changed after some files were saved"
                        }
                        record = record.copy(files = manifest.files)
                        store.save(record)
                    }
                    val preflight =
                        GuestDownloadPreflight.inspect(
                            record.origin,
                            record.transferId,
                            record.files,
                            record.saved.map { it.blobId }.toSet(),
                            redownloadMissing,
                        )
                    saver.requireSpace(preflight.remainingBytes)
                    if (GuestDownloadPreflight.needsConsent(preflight, approved)) {
                        _state.update {
                            it.copy(
                                record = record,
                                downloadConsent = preflight,
                                stage = "Confirm download",
                            )
                        }
                        return@launch
                    }
                    receiveGuestFiles(
                        client,
                        record.transferId,
                        record.files,
                        key,
                        record.saved.map { it.blobId }.toSet(),
                        onStage = { stage, index, file ->
                            _state.update {
                                it.copy(
                                    stage = stage,
                                    fileIndex = index,
                                    bytes = if (stage == "Downloading") 0 else it.bytes,
                                    totalBytes = ChunkedFileCrypto.wireSize(file.size),
                                    record = record,
                                )
                            }
                        },
                        onProgress = { bytes, total ->
                            _state.update {
                                it.copy(bytes = bytes, totalBytes = total ?: it.totalBytes)
                            }
                        },
                        saveFile = { file, bytes ->
                            saver.requireSpace(
                                record.files
                                    .filterNot { candidate ->
                                        record.saved.any { it.blobId == candidate.blobId }
                                    }
                                    .sumOf { it.size }
                            )
                            saver.save(file, bytes) { pending ->
                                record = record.copy(pending = pending)
                                store.save(record)
                            }
                        },
                        checkpoint = { output ->
                            record = record.copy(saved = record.saved + output, pending = null)
                            store.save(record)
                            _state.update { it.copy(record = record) }
                        },
                    )
                    record = record.copy(complete = true, receiptPending = true)
                    store.save(record)
                    _state.update {
                        it.copy(record = record, stage = "Saved in Downloads/psst.zip")
                    }
                    record = sendReceipt(client, record)
                } catch (e: CancellationException) {
                    _state.update {
                        it.copy(
                            stage =
                                if (record.complete) "Saved in Downloads/psst.zip" else "Paused",
                            error =
                                if (record.complete) null
                                else
                                    "Receiving stopped. Saved files are kept; resume while the link is available.",
                        )
                    }
                    throw e
                } catch (e: InsufficientDownloadSpaceException) {
                    _state.update { it.copy(stage = "More storage needed") }
                    error(requireNotNull(e.message))
                } catch (_: Exception) {
                    _state.update { it.copy(stage = "Receiving interrupted") }
                    error(
                        "Could not receive the files. Check your connection and available storage. The link may be expired, revoked, at its download limit, or contain invalid encrypted data. Saved files are kept."
                    )
                } finally {
                    withContext(NonCancellable) {
                        record.pending?.let { pending ->
                            try {
                                record = reconcile(record)
                            } catch (_: Exception) {}
                        }
                        client?.close()
                        _state.update {
                            it.copy(
                                busy = false,
                                record =
                                    record.takeIf { row -> row.identity.isNotEmpty() } ?: it.record,
                            )
                        }
                        refreshHistory()
                    }
                }
            }
    }

    private suspend fun sendReceipt(client: ApiClient, record: GuestDownload): GuestDownload {
        if (!record.receiptPending) return record
        if (acknowledgeSavedDownload(client, record.transferId)) {
            val updated = record.copy(receiptPending = false)
            try {
                store.save(updated)
            } catch (_: Exception) {
                return record
            }
            _state.update {
                if (it.record?.identity == updated.identity) it.copy(record = updated) else it
            }
            return updated
        }
        return record
    }

    private fun reconcile(record: GuestDownload): GuestDownload =
        reconcileGuestOutput(record, saver::published, saver::discard, store::save)

    fun retryReceipt() {
        if (job?.isActive == true) return
        val record = _state.value.record ?: return
        job =
            viewModelScope.launch(Dispatchers.IO) {
                val client = ApiClient.anonymous(record.origin)
                try {
                    sendReceipt(client, record)
                    refreshHistory()
                } catch (_: Exception) {
                    /* Durable pending receipt remains independently retryable. */
                } finally {
                    client.close()
                }
            }
    }

    fun retryAllReceipts() {
        if (job?.isActive == true) return
        job =
            viewModelScope.launch(Dispatchers.IO) {
                _state.update { it.copy(busy = true, stage = "Sending delivery receipts") }
                try {
                    for (record in store.all().filter { it.complete && it.receiptPending }) {
                        val client = ApiClient.anonymous(record.origin)
                        try {
                            sendReceipt(client, record)
                        } finally {
                            client.close()
                        }
                    }
                    for (receipt in store.receipts()) {
                        val client = ApiClient.anonymous(receipt.origin)
                        try {
                            if (acknowledgeSavedDownload(client, receipt.transferId))
                                store.finishReceipt(receipt.identity)
                        } finally {
                            client.close()
                        }
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                    error(
                        "Saved files are safe. Delivery receipts will remain pending until the server is reachable."
                    )
                } finally {
                    _state.update { it.copy(busy = false) }
                    refreshHistory()
                }
            }
    }

    fun retryCleanup() {
        if (job?.isActive == true) return
        job =
            viewModelScope.launch(Dispatchers.IO) {
                _state.update {
                    it.copy(busy = true, stage = "Removing interrupted uploads", error = null)
                }
                try {
                    var failed = false
                    for (entry in store.uploads()) {
                        ensureActive()
                        try {
                            val token = store.readKey(entry.identity).decodeToString()
                            val client = ApiClient.slotUpload(entry.origin, token)
                            try {
                                withTimeout(5000) {
                                    cleanupGuestUpload(client, entry.transferId, token)
                                }
                                store.finishUpload(entry)
                            } finally {
                                client.close()
                            }
                        } catch (e: CancellationException) {
                            if (e !is TimeoutCancellationException) throw e
                            failed = true
                        } catch (_: Exception) {
                            failed = true
                        }
                    }
                    if (failed)
                        error(
                            "Some interrupted uploads could not be cleaned up. Retry when their server is reachable; server expiry still applies."
                        )
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                    error(
                        "Some interrupted uploads could not be cleaned up. Retry when their server is reachable; server expiry still applies."
                    )
                } finally {
                    _state.update { it.copy(busy = false) }
                    refreshHistory()
                }
            }
    }

    fun uploadName(uri: Uri): String =
        try {
            getApplication<Application>()
                .contentResolver
                .query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)
                ?.use { if (it.moveToFirst()) it.getString(0) else "File" } ?: "File"
        } catch (_: Exception) {
            "File"
        }

    fun selectUpload(uris: List<Uri>) {
        if (job?.isActive != true)
            _state.update { it.copy(uploadFiles = uris.distinct(), error = null) }
    }

    fun upload() {
        if (job?.isActive == true || _state.value.uploaded) return
        val link = input?.link ?: return
        val uris = _state.value.uploadFiles
        if (uris.isEmpty()) return
        job =
            viewModelScope.launch(Dispatchers.IO) {
                val guest = ApiClient.anonymous(link.origin)
                var scoped: ApiClient? = null
                var child: Transfer? = null
                var complete = false
                var cleanup: zip.psst.android.data.GuestUploadCleanup? = null
                _state.update { it.copy(busy = true, error = null, stage = "Preparing upload") }
                try {
                    val maxBytes = guest.limits.get().maxFileSize
                    _state.update { it.copy(maxFileBytes = maxBytes) }
                    require(uris.size <= TransferLimits.MAX_FILES)
                    child = guest.slots.createTransfer(link.id)
                    withContext(NonCancellable) {
                        cleanup =
                            store.queueUpload(
                                link.origin,
                                child.id,
                                requireNotNull(child.deleteToken),
                            )
                    }
                    scoped = ApiClient.slotUpload(link.origin, requireNotNull(child.deleteToken))
                    val files = mutableListOf<FileMetadata>()
                    for ((index, uri) in uris.withIndex()) {
                        ensureActive()
                        val resolver = getApplication<Application>().contentResolver
                        val name =
                            resolver
                                .query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)
                                ?.use { if (it.moveToFirst()) it.getString(0) else "file" }
                                ?: "file"
                        val safeName = ManifestValidator.safeFilename(name)
                        val snapshot = spoolUpload(getApplication(), uri, maxBytes)
                        try {
                            _state.update {
                                it.copy(
                                    stage = "Uploading",
                                    fileIndex = index + 1,
                                    bytes = 0,
                                    totalBytes = ChunkedFileCrypto.wireSize(snapshot.length()),
                                )
                            }
                            files +=
                                uploadChunkedFile(
                                    scoped,
                                    child.id,
                                    snapshot,
                                    safeName,
                                    resolver.getType(uri) ?: "application/octet-stream",
                                    link.key,
                                ) { uploaded ->
                                    _state.update { it.copy(bytes = uploaded) }
                                }
                        } finally {
                            snapshot.delete()
                        }
                    }
                    val manifest = Json.encodeToString(Manifest(files)).encodeToByteArray()
                    val nonce = CryptoProvider.generateNonce()
                    scoped.transfers.uploadManifest(
                        child.id,
                        nonce + CryptoProvider.encrypt(link.key, nonce, manifest),
                    )
                    scoped.transfers.complete(child.id)
                    complete = true
                    _state.update { it.copy(uploaded = true, stage = "Files sent", error = null) }
                } catch (e: CancellationException) {
                    _state.update { it.copy(stage = "Upload cancelled") }
                    throw e
                } catch (_: Exception) {
                    error(
                        "Could not send files. Check your connection, file sizes and whether the receive link is still available."
                    )
                } finally {
                    withContext(NonCancellable) {
                        if (child != null) {
                            try {
                                withTimeout(5000) {
                                    val checkClient =
                                        scoped
                                            ?: ApiClient.slotUpload(
                                                link.origin,
                                                requireNotNull(child.deleteToken),
                                            )
                                    try {
                                        val resolution =
                                            resolveGuestUpload(
                                                checkClient,
                                                child.id,
                                                requireNotNull(child.deleteToken),
                                                knownCompleted = complete,
                                                finishJournal = {
                                                    cleanup?.let(store::finishUpload)
                                                },
                                            )
                                        complete = resolution.completed
                                        if (complete)
                                            _state.update {
                                                it.copy(
                                                    uploaded = true,
                                                    stage = "Files sent",
                                                    error = null,
                                                    notice =
                                                        if (resolution.journalCleared) null
                                                        else
                                                            "Files were sent. Cleanup can be retried later.",
                                                )
                                            }
                                        else if (!resolution.journalCleared)
                                            error("Upload stopped. Cleanup can be retried later.")
                                    } finally {
                                        if (scoped == null) checkClient.close()
                                    }
                                }
                            } catch (_: Exception) {
                                if (complete)
                                    _state.update {
                                        it.copy(
                                            uploaded = true,
                                            stage = "Files sent",
                                            error = null,
                                            notice =
                                                "Files were sent. Cleanup can be retried later.",
                                        )
                                    }
                                else
                                    error(
                                        "Upload stopped, but its incomplete server files could not be removed. They will remain until server expiry."
                                    )
                            }
                        }
                        scoped?.close()
                        guest.close()
                        _state.update { it.copy(busy = false) }
                        refreshHistory()
                    }
                }
            }
    }
}
