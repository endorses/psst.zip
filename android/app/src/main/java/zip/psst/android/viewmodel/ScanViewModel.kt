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
import zip.psst.android.data.GuestUploadSource
import zip.psst.android.data.InsufficientDownloadSpaceException
import zip.psst.android.data.LocalHistoryCursor
import zip.psst.android.data.LocalHistoryPager
import zip.psst.android.data.PreparedGuestUpload
import zip.psst.android.data.acknowledgeSavedDownload
import zip.psst.android.data.appendGuestSelection
import zip.psst.android.data.cleanupGuestUpload
import zip.psst.android.data.prepareGuestUpload
import zip.psst.android.data.receiveGuestFiles
import zip.psst.android.data.reconcileGuestOutput
import zip.psst.android.data.resolveGuestUpload
import zip.psst.android.data.uploadChunkedFile
import zip.psst.android.data.validatePreparedGuestUpload
import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.AndroidReceiveCrypto
import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.crypto.ReceiveEnvelope
import zip.psst.shared.model.*
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

data class ScanState(
    val origin: String = "",
    val reportReference: AbuseReportReference? = null,
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
    val maxUploadFiles: Int = 0,
    val remainingUploadFiles: Long? = null,
    val uploadCapacity: UploadCapacity? = null,
    val uploadCapacityMessage: String = "Checking receive capacity…",
    val fileAttempts: Map<String, Long?> = emptyMap(),
)

data class GuestHistoryState(
    val pager: LocalHistoryPager = LocalHistoryPager(),
    val next: LocalHistoryCursor? = null,
    val importing: Boolean = true,
    val importErrors: Boolean = false,
    val loading: Boolean = false,
    val error: String? = null,
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
    private var capacityExpiry: Job? = null

    private val _historyPage = MutableStateFlow(GuestHistoryState())
    val historyPage = _historyPage.asStateFlow()
    private var historyJob: Job? = null
    private var receiptCursor: String? = null
    private var downloadReceiptCursor: String? = null
    private var cleanupCursor: String? = null

    init {
        refreshHistory()
    }

    fun firstHistoryPage() = loadHistory(LocalHistoryPager())

    fun previousHistoryPage() {
        val current = _historyPage.value.pager
        if (current.previous.isNotEmpty()) loadHistory(current.back())
    }

    fun nextHistoryPage() {
        val current = _historyPage.value
        current.next?.let { loadHistory(current.pager.next(it)) }
    }

    fun refreshHistory() = loadHistory(_historyPage.value.pager)

    private fun loadHistory(target: LocalHistoryPager) {
        if (historyJob?.isActive == true) return
        _historyPage.update { it.copy(loading = true, error = null) }
        historyJob =
            viewModelScope.launch(Dispatchers.IO) {
                try {
                    store.importLegacyBatch()
                    val page = store.page(target.cursor)
                    val pendingCleanup = if (store.hasUploads()) 1 else 0
                    val pendingReceipts = if (store.hasReceipts()) 1 else 0
                    _state.update {
                        it.copy(
                            history = page.records,
                            pendingCleanup = pendingCleanup,
                            pendingReceipts = pendingReceipts,
                        )
                    }
                    _historyPage.value =
                        GuestHistoryState(target, page.next, page.importing, page.importErrors)
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                    _historyPage.update {
                        it.copy(
                            loading = false,
                            error =
                                "Local history could not be read. The current page and saved files have been retained. Retry.",
                        )
                    }
                }
            }
    }

    override fun onCleared() {
        viewModelScope.coroutineContext[Job]?.invokeOnCompletion { store.close() }
        super.onCleared()
    }

    fun classify(raw: String): Boolean {
        if (job?.isActive == true) return false
        val reportReference = AbuseReportReference.fromRawLink(raw)
        return try {
            val parsed = requireNotNull(ScanInputClassifier.classify(raw))
            input = parsed
            rawPairing = if (parsed.kind == ScanInputKind.PAIRING) raw else null
            _state.value =
                ScanState(
                    origin = parsed.link?.origin ?: parsed.pairing!!.serverUrl,
                    reportReference = parsed.link?.let(AbuseReportReference::fromLink),
                    kind = parsed.kind,
                    history = _state.value.history,
                    pendingCleanup = _state.value.pendingCleanup,
                    pendingReceipts = _state.value.pendingReceipts,
                )
            if (parsed.kind == ScanInputKind.UPLOAD) refreshUploadPolicy()
            true
        } catch (_: Exception) {
            input = null
            rawPairing = null
            _state.value =
                ScanState(
                    reportReference = reportReference,
                    origin = reportReference?.origin.orEmpty(),
                    history = _state.value.history,
                    pendingCleanup = _state.value.pendingCleanup,
                    pendingReceipts = _state.value.pendingReceipts,
                )
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
                    reportReference =
                        AbuseReportReference.create(
                            current.origin,
                            AbuseResourceKind.TRANSFER,
                            current.transferId,
                        ),
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
                    var exhaustedBlobIds = emptySet<String>()
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
                        exhaustedBlobIds =
                            transfer.files
                                .filter { it.remainingDownloads == 0L }
                                .map { it.id.lowercase() }
                                .toSet()
                        _state.update {
                            it.copy(
                                fileAttempts =
                                    if (transfer.maxDownloads > 0)
                                        transfer.files.associate { f ->
                                            f.id.lowercase() to f.remainingDownloads
                                        }
                                    else emptyMap()
                            )
                        }
                        require(record.saved.isEmpty() || record.files == manifest.files) {
                            "The file list changed after some files were saved"
                        }
                        record = record.copy(files = manifest.files)
                        store.save(record)
                    }
                    if (
                        record.files.all { file -> record.saved.any { it.blobId == file.blobId } }
                    ) {
                        record = record.copy(complete = true, receiptPending = true)
                        store.save(record)
                        record = sendReceipt(client, record)
                        return@launch
                    }
                    val preflight =
                        GuestDownloadPreflight.inspect(
                            record.origin,
                            record.transferId,
                            record.files,
                            record.saved.map { it.blobId }.toSet(),
                            redownloadMissing,
                            exhaustedBlobIds,
                        )
                    require(
                        record.files.any { f ->
                            f.blobId !in preflight.skippedBlobIds &&
                                record.saved.none { it.blobId == f.blobId }
                        }
                    ) {
                        "No download attempts remain for the missing files"
                    }
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
                        record.saved.map { it.blobId }.toSet() + preflight.skippedBlobIds,
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
                                        candidate.blobId in preflight.skippedBlobIds ||
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
                    val allSaved =
                        record.files.all { file -> record.saved.any { it.blobId == file.blobId } }
                    record = record.copy(complete = allSaved, receiptPending = allSaved)
                    store.save(record)
                    _state.update {
                        it.copy(
                            record = record,
                            stage =
                                if (allSaved) "Saved in Downloads/psst.zip"
                                else "Available files saved",
                            notice =
                                if (allSaved) null
                                else
                                    "${preflight.skippedBlobIds.size} files could not be downloaded because their attempt limits were reached.",
                        )
                    }
                    if (allSaved) record = sendReceipt(client, record)
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
                } catch (e: zip.psst.shared.api.TransferPolicyException) {
                    _state.update { it.copy(stage = e.title, error = e.message) }
                } catch (e: InsufficientDownloadSpaceException) {
                    _state.update { it.copy(stage = "More storage needed") }
                    error(requireNotNull(e.message))
                } catch (e: Exception) {
                    val trafficError =
                        client?.let { api ->
                            zip.psst.android.data.classifyTrafficFailure(e) {
                                api.transfers.trafficStatus(record.transferId)
                            }
                        }
                    _state.update {
                        it.copy(stage = trafficError?.title ?: "Receiving interrupted")
                    }
                    error(
                        trafficError?.message
                            ?: "Could not receive the files. Check your connection and available storage. The link may be expired, revoked, at its download limit, or contain invalid encrypted data. Saved files are kept."
                    )
                } finally {
                    withContext(NonCancellable) {
                        record.pending?.let { pending ->
                            try {
                                record = reconcile(record)
                            } catch (_: Exception) {}
                        }
                        client?.close()
                        if (record.files.isNotEmpty()) {
                            val refreshed =
                                zip.psst.android.data.refreshGuestDownloadAttempts(record)
                            _state.update { state ->
                                if (state.record?.identity == record.identity)
                                    state.copy(fileAttempts = refreshed)
                                else state
                            }
                        }
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
            val updated =
                try {
                    store.markReceiptSent(record.identity)
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
                    val records = store.pendingDownloads(downloadReceiptCursor)
                    if (records.isEmpty()) downloadReceiptCursor = null
                    for (record in records) {
                        downloadReceiptCursor = record.identity
                        val client = ApiClient.anonymous(record.origin)
                        try {
                            sendReceipt(client, record)
                        } finally {
                            client.close()
                        }
                    }
                    val receipts = store.receipts(receiptCursor)
                    if (receipts.isEmpty()) receiptCursor = null
                    for (receipt in receipts) {
                        receiptCursor = receipt.identity
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
                    val uploads = store.uploads(cleanupCursor)
                    if (uploads.isEmpty()) cleanupCursor = null
                    for (entry in uploads) {
                        cleanupCursor = entry.identity
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
        if (job?.isActive == true || _state.value.uploaded || uris.isEmpty()) return
        try {
            val selected = appendGuestSelection(_state.value.uploadFiles, uris)
            require(selected.all { it.scheme == "content" }) {
                "Choose files from the document picker"
            }
            _state.update { it.copy(uploadFiles = selected, error = null) }
            refreshUploadPolicy()
        } catch (e: IllegalArgumentException) {
            error(e.message ?: "Could not select these files")
        }
    }

    fun removeUpload(uri: Uri) {
        if (job?.isActive == true || _state.value.uploaded) return
        _state.update { it.copy(uploadFiles = it.uploadFiles - uri, error = null) }
        refreshUploadPolicy()
    }

    fun refreshUploadPolicy() {
        if (job?.isActive == true) return
        val link = input?.link ?: return
        job =
            viewModelScope.launch(Dispatchers.IO) {
                _state.update {
                    it.copy(
                        busy = true,
                        stage = "Checking receive link",
                        uploadCapacity = null,
                        uploadCapacityMessage = "Checking receive capacity…",
                    )
                }
                val client = ApiClient.anonymous(link.origin)
                try {
                    val policy = client.slots.availability(link.id)
                    val limit = client.limits.get().maxFileSize
                    policy.validateInvitation(link.id, link.key)
                    publishUploadPolicy(policy, limit)
                    val selected = _state.value.uploadFiles
                    // This is advisory only. Unknown/provider-changing lengths are sized by
                    // spooling
                    // every selected file before the authoritative pre-allocation refresh.
                    val sizes = selected.map { uploadSize(it) }
                    if (sizes.all { it != null } && selected.isNotEmpty()) {
                        require(sizes.filterNotNull().all { it <= limit }) {
                            "A selected file exceeds this server's per-file limit"
                        }
                        policy.validateForSubmission(
                            link.id,
                            link.key,
                            selected.size,
                            GuestUploadCapacity.totalWireBytes(sizes.filterNotNull()),
                        )
                    } else if (selected.isNotEmpty()) {
                        requireNotNull(policy.uploadCapacity) {
                                "Receive capacity could not be checked. Refresh and try again."
                            }
                            .validateSelection(
                                selected.size,
                                selected.size.toLong() * ChunkedFileCrypto.FRAME_OVERHEAD,
                            )
                        _state.update {
                            it.copy(
                                uploadCapacityMessage =
                                    it.uploadCapacityMessage +
                                        " Some file sizes will be checked before sending."
                            )
                        }
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    _state.update {
                        it.copy(
                            uploadCapacityMessage =
                                "Capacity is unavailable or out of date. Refresh before sending; your files are still selected."
                        )
                    }
                    error(
                        e.message ?: "Receive capacity could not be checked. Refresh and try again."
                    )
                } finally {
                    client.close()
                    _state.update { it.copy(busy = false) }
                }
            }
    }

    private fun uploadSize(uri: Uri): Long? =
        try {
            getApplication<Application>()
                .contentResolver
                .query(uri, arrayOf(OpenableColumns.SIZE), null, null, null)
                ?.use {
                    if (it.moveToFirst() && !it.isNull(0))
                        it.getLong(0).takeIf { size -> size >= 0 }
                    else null
                }
        } catch (_: Exception) {
            null
        }

    private fun publishUploadPolicy(policy: SlotAvailability, limit: Long) {
        val capacity = policy.uploadCapacity
        capacityExpiry?.cancel()
        if (capacity?.isFresh() == true) {
            capacityExpiry =
                viewModelScope.launch {
                    val age =
                        kotlin.time.Clock.System.now() -
                            kotlin.time.Instant.parse(capacity.checkedAt)
                    delay((120001 - age.inWholeMilliseconds).coerceIn(1, 240001))
                    _state.update {
                        if (it.uploadCapacity == capacity && !capacity.isFresh())
                            it.copy(
                                uploadCapacityMessage =
                                    "Capacity is out of date. Refresh before sending; your files are still selected."
                            )
                        else it
                    }
                }
        }
        val message =
            when {
                capacity == null || !capacity.isFresh() || capacity.state == "unknown" ->
                    "Receive capacity could not be checked. Refresh and try again."
                capacity.state == "blocked" ->
                    "This receive link cannot accept files right now. Remove files or refresh and try again."
                else ->
                    "Up to ${capacity.availableFiles} files and ${android.text.format.Formatter.formatFileSize(getApplication(), requireNotNull(capacity.availableWireBytes))} of encrypted data available for this submission. Capacity is checked again before sending."
            }
        _state.update {
            it.copy(
                maxUploadFiles = policy.maxFiles,
                remainingUploadFiles = policy.remainingFiles,
                maxFileBytes = limit,
                uploadCapacity = capacity,
                uploadCapacityMessage = message,
            )
        }
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
                var prepared: PreparedGuestUpload? = null
                var cleanup: zip.psst.android.data.GuestUploadCleanup? = null
                _state.update { it.copy(busy = true, error = null, stage = "Preparing upload") }
                try {
                    require(link.receiveVersion == 2) {
                        "This older receive link no longer accepts uploads"
                    }
                    val availability = guest.slots.availability(link.id)
                    availability.validateInvitation(link.id, link.key)
                    val maxBytes = guest.limits.get().maxFileSize
                    publishUploadPolicy(availability, maxBytes)
                    requireNotNull(availability.uploadCapacity) {
                            "Receive capacity could not be checked. Refresh and try again; your files are still selected."
                        }
                        .validateSelection(
                            uris.size,
                            uris.size.toLong() * ChunkedFileCrypto.FRAME_OVERHEAD,
                        )
                    require(uris.all { it.scheme == "content" }) {
                        "Choose files from the document picker"
                    }
                    val resolver = getApplication<Application>().contentResolver
                    val sources =
                        uris.map { uri ->
                            GuestUploadSource(
                                ManifestValidator.safeFilename(uploadName(uri)),
                                resolver.getType(uri) ?: "application/octet-stream",
                                {
                                    requireNotNull(resolver.openInputStream(uri)) {
                                        "Cannot read selected file"
                                    }
                                },
                            )
                        }
                    prepared =
                        prepareGuestUpload(
                            sources,
                            getApplication<Application>().cacheDir,
                            maxBytes,
                        )
                    val ready = requireNotNull(prepared)
                    validatePreparedGuestUpload(ready, link.id, link.key) {
                        val refreshed = guest.slots.availability(link.id)
                        val limit = guest.limits.get().maxFileSize
                        publishUploadPolicy(refreshed, limit)
                        refreshed to limit
                    }
                    child = guest.slots.createTransfer(link.id)
                    val submissionKey = CryptoProvider.generateKey()
                    val wrappedKey =
                        AndroidReceiveCrypto.sealSubmissionKey(
                            link.key,
                            link.id,
                            child.id,
                            submissionKey,
                        )
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
                    for ((index, file) in ready.files.withIndex()) {
                        ensureActive()
                        _state.update {
                            it.copy(
                                stage = "Uploading",
                                fileIndex = index + 1,
                                bytes = 0,
                                totalBytes = file.wireBytes,
                            )
                        }
                        files +=
                            uploadChunkedFile(
                                scoped,
                                child.id,
                                file.snapshot,
                                file.name,
                                file.mimeType,
                                submissionKey,
                            ) { uploaded ->
                                _state.update { it.copy(bytes = uploaded) }
                            }
                    }
                    val manifest = Json.encodeToString(Manifest(files)).encodeToByteArray()
                    val nonce = CryptoProvider.generateNonce()
                    scoped.transfers.uploadManifest(
                        child.id,
                        ReceiveEnvelope.encode(
                            wrappedKey,
                            nonce + CryptoProvider.encrypt(submissionKey, nonce, manifest),
                        ),
                    )
                    scoped.transfers.complete(child.id)
                    complete = true
                    _state.update { it.copy(uploaded = true, stage = "Files sent", error = null) }
                } catch (e: CancellationException) {
                    _state.update { it.copy(stage = "Upload cancelled") }
                    throw e
                } catch (e: zip.psst.shared.api.TransferPolicyException) {
                    _state.update { it.copy(stage = e.title, error = e.message) }
                } catch (e: IllegalArgumentException) {
                    _state.update {
                        it.copy(
                            stage = "Check selected files",
                            error = e.message,
                            uploadCapacityMessage =
                                "Capacity may have changed. Refresh and try again; your files are still selected.",
                        )
                    }
                } catch (e: Exception) {
                    val trafficError =
                        zip.psst.android.data.classifyTrafficFailure(e) {
                            guest.slots.trafficStatus(link.id)
                        }
                    trafficError?.let { policy -> _state.update { it.copy(stage = policy.title) } }
                    error(
                        trafficError?.message
                            ?: "Could not send files. Check your connection, file sizes and whether the receive link is still available."
                    )
                } finally {
                    withContext(NonCancellable) {
                        prepared?.close()
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
                                            _state.update {
                                                it.copy(
                                                    notice =
                                                        "Upload stopped. Cleanup can be retried later."
                                                )
                                            }
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
                                    _state.update {
                                        it.copy(
                                            notice =
                                                "Upload stopped, but its incomplete server files could not be removed. They will remain until server expiry."
                                        )
                                    }
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
