package zip.psst.android.viewmodel

import android.app.Application
import android.net.Uri
import android.provider.OpenableColumns
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.R
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
import zip.psst.android.i18n.*
import zip.psst.android.i18n.ScanStage
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
    val inputDraft: String = "",
    val origin: String = "",
    val sharedTitle: String? = null,
    val reportReference: AbuseReportReference? = null,
    val kind: ScanInputKind? = null,
    val busy: Boolean = false,
    val stage: ScanStage = ScanStage.NONE,
    val bytes: Long = 0,
    val totalBytes: Long? = null,
    val fileIndex: Int = 0,
    val error: UiText? = null,
    val notice: UiText? = null,
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
    val uploadCapacityMessage: UiText = message(R.string.l_checking_receive_capacity_50448a),
    val fileAttempts: Map<String, Long?> = emptyMap(),
    val refreshingAvailability: Boolean = false,
)

data class GuestHistoryState(
    val pager: LocalHistoryPager = LocalHistoryPager(),
    val next: LocalHistoryCursor? = null,
    val importing: Boolean = true,
    val importErrors: Boolean = false,
    val loading: Boolean = false,
    val error: UiText? = null,
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
    private var availabilityJob: Job? = null
    @Volatile private var availabilityRevision = 0L

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
                                message(
                                    R.string
                                        .l_local_history_could_not_be_read_the_current_page_and_saved_files__654bc9
                                ),
                        )
                    }
                }
            }
    }

    override fun onCleared() {
        viewModelScope.coroutineContext[Job]?.invokeOnCompletion { store.close() }
        super.onCleared()
    }

    fun setInputDraft(value: String) {
        _state.update { it.copy(inputDraft = value) }
    }

    fun classify(raw: String): Boolean {
        if (job?.isActive == true) return false
        stopAvailabilityRefresh()
        val reportReference = AbuseReportReference.fromRawLink(raw)
        return try {
            val parsed = requireNotNull(ScanInputClassifier.classify(raw))
            input = parsed
            rawPairing = if (parsed.kind == ScanInputKind.PAIRING) raw else null
            _state.value =
                ScanState(
                    inputDraft = _state.value.inputDraft,
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
                    inputDraft = _state.value.inputDraft,
                    reportReference = reportReference,
                    origin = reportReference?.origin.orEmpty(),
                    history = _state.value.history,
                    pendingCleanup = _state.value.pendingCleanup,
                    pendingReceipts = _state.value.pendingReceipts,
                )
            error(
                message(
                    R.string
                        .l_this_is_not_a_supported_psst_zip_qr_code_or_link_scan_again_or_pa_f5aedb
                )
            )
            false
        }
    }

    fun pairingPayload(): String? = rawPairing

    fun accountConnected() {
        clear()
        _state.update {
            it.copy(
                notice =
                    message(
                        R.string
                            .l_account_connected_you_can_now_send_files_and_create_receive_links_3d6c3b
                    )
            )
        }
    }

    fun error(message: UiText) {
        _state.update { it.copy(error = message) }
    }

    fun cancel() {
        stopAvailabilityRefresh()
        capacityExpiry?.cancel()
        job?.cancel()
        _state.update { it.copy(downloadConsent = null) }
    }

    fun clear() {
        stopAvailabilityRefresh()
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
        stopAvailabilityRefresh()
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
                    fileAttempts =
                        current.files.associate { file -> file.blobId.lowercase() to null },
                    refreshingAvailability = false,
                    record = current,
                    error = null,
                    downloadConsent = null,
                    stage = if (current.complete) ScanStage.SAVED else ScanStage.RESUMABLE,
                )
            }
            refreshDownloadAvailability()
            if (current.receiptPending) retryReceipt()
            return true
        } catch (_: Exception) {
            error(
                message(
                    R.string
                        .l_could_not_reopen_this_local_record_saved_files_remain_in_download_23ca76
                )
            )
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
            error(message(R.string.l_could_not_remove_local_history_de6a5e))
        }
    }

    fun missingFiles(): Boolean = _state.value.record?.saved?.any { !saver.exists(it) } == true

    fun fileExists(file: zip.psst.android.data.SavedGuestFile) = saver.exists(file)

    internal fun downloadAvailability(): zip.psst.android.data.GuestDownloadAvailability? =
        _state.value.record?.let {
            zip.psst.android.data.guestDownloadAvailability(
                it,
                _state.value.fileAttempts,
                saver::exists,
            )
        }

    private fun stopAvailabilityRefresh() {
        availabilityRevision++
        availabilityJob?.cancel()
        availabilityJob = null
        _state.update { it.copy(refreshingAvailability = false) }
    }

    /** Advisory metadata refresh never opens payload URLs or repeats a download. */
    fun refreshDownloadAvailability() {
        if (_state.value.busy || availabilityJob?.isActive == true) return
        val record = _state.value.record ?: return
        if (record.files.isEmpty()) return
        val revision = ++availabilityRevision
        _state.update { it.copy(refreshingAvailability = true) }
        availabilityJob =
            viewModelScope.launch(Dispatchers.IO) {
                try {
                    val attempts = zip.psst.android.data.refreshGuestDownloadAttempts(record)
                    _state.update {
                        if (
                            availabilityRevision == revision &&
                                it.record?.identity == record.identity &&
                                it.record.files == record.files
                        )
                            it.copy(fileAttempts = attempts, refreshingAvailability = false)
                        else it
                    }
                } finally {
                    _state.update {
                        if (
                            availabilityRevision == revision &&
                                it.record?.identity == record.identity
                        )
                            it.copy(refreshingAvailability = false)
                        else it
                    }
                }
            }
    }

    fun confirmDownload() {
        val consent = _state.value.downloadConsent ?: return
        receive(consent.redownloadMissing, consent)
    }

    fun dismissDownloadConsent() {
        _state.update { it.copy(downloadConsent = null, stage = ScanStage.CANCELLED) }
    }

    fun receive(redownloadMissing: Boolean = false) = receive(redownloadMissing, null)

    private fun receive(redownloadMissing: Boolean, approved: GuestDownloadConsent?) {
        if (job?.isActive == true) return
        stopAvailabilityRefresh()
        job =
            viewModelScope.launch(Dispatchers.IO) {
                _state.update {
                    it.copy(
                        busy = true,
                        error = null,
                        stage = ScanStage.INSPECTING,
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
                                stage = ScanStage.MISSING,
                                error =
                                    message(
                                        R.string
                                            .l_choose_redownload_missing_files_to_fetch_removed_copies_the_link__3d6573
                                    ),
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
                        if (transfer.status == TransferStatus.EXHAUSTED)
                            throw UiFailureException(
                                message(
                                    R.string
                                        .l_download_limit_reached_saved_local_copies_remain_available_ef7380
                                )
                            )
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
                        uiRequire(record.saved.isEmpty() || record.files == manifest.files) {
                            message(
                                R.string.l_the_file_list_changed_after_some_files_were_saved_e08cb6
                            )
                        }
                        record = record.copy(files = manifest.files, sharedTitle = transfer.title)
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
                    uiRequire(
                        record.files.any { f ->
                            f.blobId !in preflight.skippedBlobIds &&
                                record.saved.none { it.blobId == f.blobId }
                        }
                    ) {
                        message(R.string.l_no_download_attempts_remain_for_the_missing_files_1e5c2c)
                    }
                    saver.requireSpace(preflight.remainingBytes)
                    if (GuestDownloadPreflight.needsConsent(preflight, approved)) {
                        _state.update {
                            it.copy(
                                record = record,
                                downloadConsent = preflight,
                                stage = ScanStage.CONFIRM,
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
                                    bytes = if (stage == ScanStage.DOWNLOADING) 0 else it.bytes,
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
                            stage = if (allSaved) ScanStage.SAVED else ScanStage.AVAILABLE_SAVED,
                            notice =
                                if (allSaved) null
                                else
                                    pluralMessage(
                                        R.plurals.files_unavailable,
                                        preflight.skippedBlobIds.size.toLong(),
                                        preflight.skippedBlobIds.size,
                                    ),
                        )
                    }
                    if (allSaved) record = sendReceipt(client, record)
                } catch (e: CancellationException) {
                    _state.update {
                        it.copy(
                            stage = if (record.complete) ScanStage.SAVED else ScanStage.PAUSED,
                            error =
                                if (record.complete) null
                                else
                                    message(
                                        R.string
                                            .l_receiving_stopped_saved_files_are_kept_resume_while_the_link_is_a_78143e
                                    ),
                        )
                    }
                    throw e
                } catch (e: zip.psst.shared.api.TransferPolicyException) {
                    _state.update {
                        it.copy(stage = ScanStage.forFailure(e), error = failureText(e))
                    }
                } catch (e: InsufficientDownloadSpaceException) {
                    _state.update { it.copy(stage = ScanStage.STORAGE) }
                    error(failureText(e))
                } catch (e: Exception) {
                    val trafficError =
                        client?.let { api ->
                            zip.psst.android.data.classifyTrafficFailure(e) {
                                api.transfers.trafficStatus(record.transferId)
                            }
                        }
                    _state.update {
                        it.copy(
                            stage =
                                trafficError?.let(ScanStage::forFailure) ?: ScanStage.INTERRUPTED
                        )
                    }
                    error(
                        trafficError?.let(::failureText)
                            ?: message(
                                R.string
                                    .l_could_not_receive_the_files_check_your_connection_and_available_s_bf341e
                            )
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
                _state.update { it.copy(busy = true, stage = ScanStage.RECEIPTS) }
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
                        message(
                            R.string
                                .l_saved_files_are_safe_delivery_receipts_will_remain_pending_until__ceaf93
                        )
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
                _state.update { it.copy(busy = true, stage = ScanStage.CLEANUP, error = null) }
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
                            message(
                                R.string
                                    .l_some_interrupted_uploads_could_not_be_cleaned_up_retry_when_their_897009
                            )
                        )
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                    error(
                        message(
                            R.string
                                .l_some_interrupted_uploads_could_not_be_cleaned_up_retry_when_their_897009
                        )
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
                ?.use { if (it.moveToFirst()) it.getString(0) else tr(R.string.l_file_2c3caf) }
                ?: tr(R.string.l_file_2c3caf)
        } catch (_: Exception) {
            tr(R.string.l_file_2c3caf)
        }

    fun selectUpload(uris: List<Uri>) {
        if (job?.isActive == true || _state.value.uploaded || uris.isEmpty()) return
        try {
            val selected = appendGuestSelection(_state.value.uploadFiles, uris)
            uiRequire(selected.all { it.scheme == "content" }) {
                message(R.string.l_choose_files_from_the_document_picker_d11a92)
            }
            _state.update { it.copy(uploadFiles = selected, error = null) }
            refreshUploadPolicy()
        } catch (e: IllegalArgumentException) {
            error(failureText(e) ?: message(R.string.l_could_not_select_these_files_ba479d))
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
                        error = null,
                        stage = ScanStage.CHECKING,
                        uploadCapacity = null,
                        uploadCapacityMessage = message(R.string.l_checking_receive_capacity_50448a),
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
                        uiRequire(sizes.filterNotNull().all { it <= limit }) {
                            message(
                                R.string
                                    .l_a_selected_file_exceeds_this_server_s_per_file_limit_7568e8
                            )
                        }
                        policy.validateForSubmission(
                            link.id,
                            link.key,
                            selected.size,
                            GuestUploadCapacity.totalWireBytes(sizes.filterNotNull()),
                        )
                    } else if (selected.isNotEmpty()) {
                        requireNotNull(policy.uploadCapacity) {
                                message(
                                    R.string
                                        .l_receive_capacity_could_not_be_checked_refresh_and_try_again_722ee1
                                )
                            }
                            .validateSelection(
                                selected.size,
                                selected.size.toLong() * ChunkedFileCrypto.FRAME_OVERHEAD,
                            )
                        _state.update {
                            it.copy(
                                uploadCapacityMessage =
                                    combinedMessages(
                                        listOf(
                                            it.uploadCapacityMessage,
                                            message(
                                                R.string
                                                    .l_some_file_sizes_will_be_checked_before_sending_69808b
                                            ),
                                        )
                                    )
                            )
                        }
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    _state.update {
                        it.copy(
                            uploadCapacityMessage =
                                message(
                                    R.string
                                        .l_capacity_is_unavailable_or_out_of_date_refresh_before_sending_you_d5fd19
                                )
                        )
                    }
                    error(zip.psst.android.data.receiveCapacityError(e))
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
                                    message(
                                        R.string
                                            .l_capacity_is_out_of_date_refresh_before_sending_your_files_are_sti_c21ef7
                                    )
                            )
                        else it
                    }
                }
        }
        val message =
            when {
                capacity == null || !capacity.isFresh() || capacity.state == "unknown" ->
                    message(
                        R.string
                            .l_receive_capacity_could_not_be_checked_refresh_and_try_again_722ee1
                    )
                capacity.state == "blocked" ->
                    message(
                        R.string
                            .l_this_receive_link_cannot_accept_files_right_now_remove_files_or_r_3a5187
                    )
                else ->
                    message(
                        R.string
                            .l_up_to_1_s_files_and_2_s_of_encrypted_data_available_for_this_subm_165c0f,
                        (capacity.availableFiles),
                        (UiByteCount(requireNotNull(capacity.availableWireBytes))),
                    )
            }
        _state.update {
            it.copy(
                sharedTitle = policy.title,
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
                _state.update { it.copy(busy = true, error = null, stage = ScanStage.PREPARING) }
                try {
                    uiRequire(link.receiveVersion == 2) {
                        message(R.string.l_this_older_receive_link_no_longer_accepts_uploads_f47bfb)
                    }
                    val availability = guest.slots.availability(link.id)
                    availability.validateInvitation(link.id, link.key)
                    val maxBytes = guest.limits.get().maxFileSize
                    publishUploadPolicy(availability, maxBytes)
                    requireNotNull(availability.uploadCapacity) {
                            message(
                                R.string
                                    .l_receive_capacity_could_not_be_checked_refresh_and_try_again_your__93d95b
                            )
                        }
                        .validateSelection(
                            uris.size,
                            uris.size.toLong() * ChunkedFileCrypto.FRAME_OVERHEAD,
                        )
                    uiRequire(uris.all { it.scheme == "content" }) {
                        message(R.string.l_choose_files_from_the_document_picker_d11a92)
                    }
                    val resolver = getApplication<Application>().contentResolver
                    val sources =
                        uris.map { uri ->
                            GuestUploadSource(
                                ManifestValidator.safeFilename(uploadName(uri)),
                                resolver.getType(uri) ?: "application/octet-stream",
                                {
                                    requireNotNull(resolver.openInputStream(uri)) {
                                        message(R.string.l_cannot_read_selected_file_dd9248)
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
                                stage = ScanStage.UPLOADING,
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
                    zip.psst.android.data.HistoryNotifications.changed(link.origin)
                    complete = true
                    _state.update { it.copy(uploaded = true, stage = ScanStage.SENT, error = null) }
                } catch (e: CancellationException) {
                    _state.update { it.copy(stage = ScanStage.UPLOAD_CANCELLED) }
                    throw e
                } catch (e: zip.psst.shared.api.TransferPolicyException) {
                    _state.update {
                        it.copy(stage = ScanStage.forFailure(e), error = failureText(e))
                    }
                } catch (e: IllegalArgumentException) {
                    _state.update {
                        it.copy(
                            stage = ScanStage.SELECTION,
                            error = failureText(e),
                            uploadCapacityMessage =
                                message(
                                    R.string
                                        .l_capacity_may_have_changed_refresh_and_try_again_your_files_are_st_8b5635
                                ),
                        )
                    }
                } catch (e: Exception) {
                    val trafficError =
                        zip.psst.android.data.classifyTrafficFailure(e) {
                            guest.slots.trafficStatus(link.id)
                        }
                    trafficError?.let { policy ->
                        _state.update { it.copy(stage = ScanStage.forFailure(policy)) }
                    }
                    error(
                        trafficError?.let(::failureText)
                            ?: message(
                                R.string
                                    .l_could_not_send_files_check_your_connection_file_sizes_and_whether_2d1f4b
                            )
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
                                                    stage = ScanStage.SENT,
                                                    error = null,
                                                    notice =
                                                        if (resolution.journalCleared) null
                                                        else
                                                            message(
                                                                R.string
                                                                    .l_files_were_sent_cleanup_can_be_retried_later_2e3639
                                                            ),
                                                )
                                            }
                                        else if (!resolution.journalCleared)
                                            _state.update {
                                                it.copy(
                                                    notice =
                                                        message(
                                                            R.string
                                                                .l_upload_stopped_cleanup_can_be_retried_later_0f81d2
                                                        )
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
                                            stage = ScanStage.SENT,
                                            error = null,
                                            notice =
                                                message(
                                                    R.string
                                                        .l_files_were_sent_cleanup_can_be_retried_later_2e3639
                                                ),
                                        )
                                    }
                                else
                                    _state.update {
                                        it.copy(
                                            notice =
                                                message(
                                                    R.string
                                                        .l_upload_stopped_but_its_incomplete_server_files_could_not_be_remov_33f9ec
                                                )
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
