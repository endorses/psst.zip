package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.InboxKeyStore
import zip.psst.android.data.ReceivedSnapshot
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.decodeInboxKeyMarker
import zip.psst.android.data.decryptInboxManifest
import zip.psst.android.data.optionalLinkLimit
import zip.psst.android.data.parseHistoryExpiry
import zip.psst.android.data.receiveAndSaveChild
import zip.psst.android.data.receivedSnapshot
import zip.psst.android.data.retrySavedDownloadAcknowledgements
import zip.psst.android.data.savedFileCount
import zip.psst.android.data.savedTransferIds
import zip.psst.shared.api.AdminTransferForbiddenException
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.AuthenticationRequiredException
import zip.psst.shared.api.PasswordChangeRequiredException
import zip.psst.shared.api.SlotEvent
import zip.psst.shared.crypto.AndroidReceiveCrypto
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.UrlHelper
import kotlin.io.encoding.Base64
import kotlin.io.encoding.ExperimentalEncodingApi
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json

data class ReceiveUiState(
    val localName: String = "",
    val maxFilesInput: String = "",
    val maxFiles: Int = 0,
    val remainingFiles: Long? = null,
    val reservedFiles: Long = 0,
    val legacyReadOnly: Boolean = false,
    val isCreatingSlot: Boolean = false,
    val slotId: String? = null,
    val encryptionKey: String? = null,
    val uploadUrl: String? = null,
    val slotStatus: String = "waiting",
    val receivedFiles: List<FileMetadata> = emptyList(),
    val isDownloading: Boolean = false,
    val downloadProgress: Float = 0f,
    val error: String? = null,
    val requiresLogin: Boolean = false,
    val downloadComplete: Boolean = false,
    val connectionError: Boolean = false,
    val savedFileCount: Int = 0,
    val keyUnavailable: Boolean = false,
    val downloadConsent: zip.psst.android.data.InboxDownloadConsent? = null,
)

class ReceiveViewModel(application: Application) : AndroidViewModel(application) {

    private val app = application as PsstApplication
    private val inboxKeys = InboxKeyStore(application)
    private val _uiState = MutableStateFlow(ReceiveUiState())
    val uiState: StateFlow<ReceiveUiState> = _uiState.asStateFlow()

    private var sseJob: Job? = null
    private var encryptionKeyBytes: ByteArray? = null
    private var slotClient: ApiClient? = null
    private var pollJob: Job? = null
    private val historyMutex = Mutex()
    private val refreshMutex = Mutex()
    private var visible = true
    private var downloadJob: Job? = null
    private var createJob: Job? = null
    private var activeAccess = app.prefs.historyAccess.value

    init {
        viewModelScope.launch {
            app.prefs.historyAccess.collect { access ->
                if (activeAccess != access) {
                    activeAccess = access
                    sseJob?.cancel()
                    pollJob?.cancel()
                    downloadJob?.cancel()
                    createJob?.cancel()
                    slotClient?.close()
                    encryptionKeyBytes = null
                    _uiState.value =
                        ReceiveUiState(
                            error =
                                app.getString(
                                    zip.psst.android.R.string
                                        .ui_your_account_changed_create_a_new_receive_link_to_continue
                                ),
                            requiresLogin = access.accountId == null,
                        )
                }
            }
        }
    }

    @OptIn(ExperimentalEncodingApi::class)
    fun openExisting(id: String) {
        if (_uiState.value.slotId == id) return
        createJob =
            viewModelScope.launch(Dispatchers.IO) {
                val access = app.prefs.historyAccess.value
                val row = app.database.transferHistoryDao().getById(id)
                if (row == null || !access.permits(row)) {
                    _uiState.value =
                        ReceiveUiState(
                            error =
                                app.getString(
                                    zip.psst.android.R.string
                                        .ui_this_receive_link_is_unavailable_to_this_account
                                )
                        )
                    return@launch
                }
                try {
                    val key = runCatching { decodeInboxKeyMarker(row.encryptionKey) }.getOrNull()
                    val privateAvailable =
                        !row.encryptionKey.startsWith("v2.") || inboxKeys.read(row) != null
                    check(app.prefs.historyAccess.value == access)
                    encryptionKeyBytes = key.takeIf { privateAvailable }
                    val client =
                        ApiClient(
                            ServerConfig(row.serverUrl),
                            sessionToken = requireNotNull(app.prefs.getSessionToken(row.serverUrl)),
                        )
                    slotClient = client
                    val restored = restoreReceiveEntry(row, access, privateAvailable)
                    _uiState.value =
                        restored.copy(
                            error =
                                if (restored.keyUnavailable)
                                    app.getString(zip.psst.android.R.string.unavailable_key)
                                else null
                        )
                    if (visible) listenForEvents(client, id)
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                    _uiState.value =
                        ReceiveUiState(
                            slotId = row.id,
                            slotStatus = row.status,
                            error = app.getString(zip.psst.android.R.string.unavailable_key),
                            keyUnavailable = true,
                        )
                }
            }
    }

    fun setVisible(value: Boolean) {
        visible = value
        if (!value) {
            sseJob?.cancel()
            pollJob?.cancel()
            cancelDownload()
        } else {
            val id = _uiState.value.slotId
            val client = slotClient
            if (id != null && client != null) listenForEvents(client, id)
        }
    }

    fun reconnect() = setVisible(true)

    @OptIn(ExperimentalEncodingApi::class)
    fun renameLocal(value: String) {
        val name = value.take(200)
        _uiState.update { it.copy(localName = name) }
        val access = app.prefs.historyAccess.value
        val id = _uiState.value.slotId ?: return
        viewModelScope.launch {
            val dao = app.database.transferHistoryDao()
            val row = dao.getById(id) ?: return@launch
            if (
                access == app.prefs.historyAccess.value &&
                    access.permits(row) &&
                    row.accountId != null
            )
                dao.rename(
                    row.id,
                    row.serverUrl,
                    row.accountId,
                    row.type,
                    name.trim().ifEmpty { null },
                )
        }
    }

    fun setMaxFiles(value: String) {
        if (_uiState.value.slotId == null && !_uiState.value.isCreatingSlot)
            _uiState.update { it.copy(maxFilesInput = value.take(10), error = null) }
    }

    @OptIn(ExperimentalEncodingApi::class)
    fun createSlot() {
        if (createJob?.isActive == true || _uiState.value.slotId != null) return
        val limit =
            try {
                optionalLinkLimit(_uiState.value.maxFilesInput)
            } catch (e: IllegalArgumentException) {
                _uiState.update { it.copy(error = e.message) }
                return
            }
        val serverUrl = app.prefs.getServerUrl()
        if (serverUrl.isBlank()) {
            _uiState.update {
                it.copy(
                    error = app.getString(zip.psst.android.R.string.ui_server_url_not_configured)
                )
            }
            return
        }

        val sessionToken = app.prefs.getSessionToken(serverUrl)
        val accountId = app.prefs.getAccountId()
        if (sessionToken == null || accountId == null) {
            _uiState.update {
                it.copy(
                    requiresLogin = true,
                    error =
                        app.getString(
                            zip.psst.android.R.string
                                .ui_sign_in_under_server_configuration_to_create_receive_links
                        ),
                )
            }
            return
        }

        sseJob?.cancel()
        pollJob?.cancel()
        slotClient?.close()
        _uiState.update { it.copy(isCreatingSlot = true, error = null, maxFiles = limit) }

        val access = app.prefs.historyAccess.value
        createJob =
            viewModelScope.launch(Dispatchers.IO) {
                var allocation: TransferHistoryEntity? = null
                var retained = false
                try {
                    val client = ApiClient(ServerConfig(serverUrl), sessionToken = sessionToken)
                    slotClient = client
                    val pair = AndroidReceiveCrypto.generateKeyPair()
                    val key = pair.publicKey
                    val publicKey =
                        Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).encode(key)
                    val slot = client.slots.create(publicKey, limit)
                    allocation =
                        TransferHistoryEntity(
                            slot.id,
                            "received",
                            0,
                            0,
                            serverUrl,
                            "",
                            "failed",
                            deletionToken = slot.deleteToken,
                            accountId = accountId,
                            expiresAt = parseHistoryExpiry(slot.expiresAt),
                        )
                    withContext(NonCancellable) {
                        app.database.transferHistoryDao().insert(requireNotNull(allocation))
                    }
                    zip.psst.android.data.verifyReceivePolicy(
                        client.slots.get(slot.id),
                        publicKey,
                        limit,
                    )
                    encryptionKeyBytes = key
                    val base64Key = "v2.$publicKey"
                    val uploadUrl = UrlHelper.buildReceiveUrl(serverUrl, slot.id, key)

                    // Persist the private key under its original account/server/slot before
                    // history.
                    withContext(NonCancellable) {
                        inboxKeys.save(serverUrl, accountId, slot.id, pair.privateKey)
                        pair.privateKey.fill(0)
                        app.database
                            .transferHistoryDao()
                            .update(
                                TransferHistoryEntity(
                                    id = slot.id,
                                    type = "received",
                                    fileCount = 0,
                                    totalSize = 0,
                                    serverUrl = serverUrl,
                                    encryptionKey = base64Key,
                                    status = "waiting",
                                    expiresAt = parseHistoryExpiry(slot.expiresAt),
                                    deletionToken = slot.deleteToken,
                                    accountId = accountId,
                                )
                            )
                    }
                    retained = true
                    ensureActive()
                    check(app.prefs.historyAccess.value == access)
                    _uiState.update {
                        it.copy(
                            isCreatingSlot = false,
                            slotId = slot.id,
                            encryptionKey = base64Key,
                            uploadUrl = uploadUrl,
                        )
                    }

                    // Start listening for SSE events
                    if (visible) listenForEvents(client, slot.id)
                } catch (e: Exception) {
                    if (!retained && allocation != null)
                        withContext(NonCancellable) {
                            val failed = requireNotNull(allocation)
                            val cleanup =
                                ApiClient(ServerConfig(serverUrl), sessionToken = sessionToken)
                            try {
                                kotlinx.coroutines.withTimeout(5000) {
                                    cleanup.slots.delete(failed.id, failed.deletionToken)
                                }
                                app.database.transferHistoryDao().delete(failed.id)
                                inboxKeys.delete(failed)
                            } catch (_: Exception) {
                                /* Capability remains in non-shareable History for owner cleanup. */
                            } finally {
                                cleanup.close()
                            }
                        }
                    if (e is CancellationException) throw e
                    if (
                        e is AuthenticationRequiredException &&
                            app.prefs.getSessionToken(serverUrl) == sessionToken
                    )
                        app.prefs.clearSession()
                    slotClient?.close()
                    _uiState.update {
                        it.copy(
                            isCreatingSlot = false,
                            requiresLogin =
                                e is AuthenticationRequiredException ||
                                    e is PasswordChangeRequiredException ||
                                    e is AdminTransferForbiddenException,
                            error =
                                if (
                                    e is zip.psst.shared.api.TransferPolicyException ||
                                        e is zip.psst.android.data.UnsupportedLinkPolicyException ||
                                        e is PasswordChangeRequiredException ||
                                        e is AdminTransferForbiddenException
                                )
                                    e.message
                                else
                                    app.getString(
                                        zip.psst.android.R.string
                                            .ui_could_not_create_a_receive_link_check_your_connection_and_retry
                                    ),
                        )
                    }
                }
            }
    }

    private fun listenForEvents(client: ApiClient, slotId: String) {
        sseJob?.cancel()
        pollJob?.cancel()
        sseJob =
            viewModelScope.launch(Dispatchers.IO) {
                try {
                    // Backend sends event names inside JSON data. Refresh on every message,
                    // including connected, so only completed transfer summaries enable downloads.
                    client.slots.events(slotId).collect { _: SlotEvent ->
                        refreshSlot(client, slotId)
                    }
                } catch (e: Exception) {
                    if (e is CancellationException) throw e
                    // Polling below covers disconnects and missed SSE notifications.
                }
            }
        pollJob =
            viewModelScope.launch(Dispatchers.IO) {
                while (isActive) {
                    refreshSlot(client, slotId)
                    delay(if (_uiState.value.connectionError) 15000L else 5000L)
                }
            }
    }

    private suspend fun refreshSlot(client: ApiClient, slotId: String) {
        if (!refreshMutex.tryLock()) return
        try {
            val visibleRow = app.database.transferHistoryDao().getById(slotId) ?: return
            if (!app.prefs.historyAccess.value.permits(visibleRow)) return
            retrySavedDownloadAcknowledgements(visibleRow, client)
            val slot = client.slots.get(slotId)
            if (!app.prefs.historyAccess.value.permits(visibleRow)) return
            val snapshot = slot.receivedSnapshot()
            historyMutex.withLock {
                val row = app.database.transferHistoryDao().mergeReceived(slotId, snapshot)
                if (row != null) {
                    _uiState.update {
                        if (it.slotId == slotId)
                            it.copy(
                                connectionError = false,
                                slotStatus = row.status,
                                downloadComplete = row.status == "complete",
                                maxFiles = slot.maxFiles,
                                remainingFiles = slot.remainingFiles,
                                reservedFiles = slot.reservedFiles,
                            )
                        else it
                    }
                }
            }
        } catch (e: Exception) {
            if (e is CancellationException) throw e
            if (e is AuthenticationRequiredException) {
                sseJob?.cancel()
                pollJob?.cancel()
                _uiState.update {
                    it.copy(requiresLogin = true, error = e.message, connectionError = false)
                }
                return
            }
            if (
                e is zip.psst.shared.api.ResourceRevokedException ||
                    (e is io.ktor.client.plugins.ClientRequestException &&
                        e.response.status.value in listOf(404, 410))
            ) {
                app.database.transferHistoryDao().updateStatus(slotId, "unavailable")
                sseJob?.cancel()
                pollJob?.cancel()
                _uiState.update {
                    if (it.slotId == slotId)
                        it.copy(slotStatus = "unavailable", connectionError = false)
                    else it
                }
            } else _uiState.update { it.copy(connectionError = true) }
        } finally {
            refreshMutex.unlock()
        }
    }

    fun downloadReceivedFiles() = downloadReceivedFiles(null)

    fun confirmDownload() {
        val approved = _uiState.value.downloadConsent ?: return
        downloadReceivedFiles(approved)
    }

    fun cancelDownload() {
        downloadJob?.cancel()
        _uiState.update { it.copy(downloadConsent = null) }
    }

    private fun downloadReceivedFiles(approved: zip.psst.android.data.InboxDownloadConsent?) {
        val slotId = _uiState.value.slotId ?: return
        if (encryptionKeyBytes == null || _uiState.value.keyUnavailable) return
        if (_uiState.value.isDownloading) return

        _uiState.update {
            it.copy(
                isDownloading = true,
                error = null,
                downloadComplete = false,
                downloadConsent = null,
            )
        }

        downloadJob =
            viewModelScope.launch(Dispatchers.IO) {
                val access = app.prefs.historyAccess.value
                val dao = app.database.transferHistoryDao()
                val row = dao.getById(slotId)
                if (row == null || !access.permits(row)) {
                    _uiState.update {
                        it.copy(
                            isDownloading = false,
                            error =
                                app.getString(
                                    zip.psst.android.R.string.ui_receive_history_entry_is_missing
                                ),
                        )
                    }
                    return@launch
                }
                val client =
                    ApiClient(
                        ServerConfig(row.serverUrl),
                        sessionToken = requireNotNull(app.prefs.getSessionToken(row.serverUrl)),
                    )
                try {
                    val slot = client.slots.get(slotId)
                    check(app.prefs.historyAccess.value == access)
                    val latest = dao.mergeReceived(slotId, slot.receivedSnapshot()) ?: row
                    val transfers =
                        slot.completedTransfers.filter {
                            it.transferId !in latest.savedTransferIds()
                        }
                    require(transfers.isNotEmpty()) { "No new completed uploads to save" }
                    val received = mutableListOf<Pair<String, FileMetadata>>()
                    val childKeys = mutableMapOf<String, ByteArray>()
                    val fingerprints = mutableMapOf<String, String>()
                    val privateKey = inboxKeys.read(row)
                    for (transfer in transfers) {
                        val manifestBytes = client.transfers.downloadManifest(transfer.transferId)
                        val decoded =
                            decryptInboxManifest(
                                row,
                                transfer.transferId,
                                manifestBytes,
                                privateKey,
                            )
                        val manifest = decoded.manifest
                        val metadata = client.transfers.get(transfer.transferId)
                        zip.psst.shared.model.ManifestValidator.validateForTransfer(
                            manifest,
                            metadata,
                        )
                        require(manifest.files.size == transfer.fileCount) {
                            "Manifest file count mismatch"
                        }
                        fingerprints[transfer.transferId] =
                            java.security.MessageDigest.getInstance("SHA-256")
                                .digest(manifestBytes)
                                .joinToString("") { "%02x".format(it) }
                        childKeys[transfer.transferId] = decoded.key
                        received += manifest.files.map { transfer.transferId to it }
                    }
                    ensureActive()
                    check(app.prefs.historyAccess.value == access)
                    _uiState.update { it.copy(receivedFiles = received.map { it.second }) }

                    val context = getApplication<PsstApplication>()
                    val totalFiles = received.size
                    require(totalFiles == transfers.sumOf { it.fileCount }) {
                        "Manifest file count mismatch"
                    }
                    val savedIds =
                        Json.decodeFromString<Set<String>>(
                            dao.getById(slotId)?.savedFileIdsJson ?: "[]"
                        )
                    val preflight =
                        zip.psst.android.data.InboxDownloadPreflight.inspect(
                            row.serverUrl,
                            requireNotNull(row.accountId),
                            slotId,
                            received,
                            savedIds,
                            fingerprints,
                        )
                    val saver = zip.psst.android.data.GuestFileSaver(context)
                    saver.requireSpace(preflight.remainingBytes)
                    if (
                        zip.psst.android.data.InboxDownloadPreflight.needsConsent(
                            preflight,
                            approved,
                        )
                    ) {
                        _uiState.update { it.copy(downloadConsent = preflight) }
                        return@launch
                    }
                    var remainingBytes = preflight.remainingBytes
                    var savedFiles = 0
                    for (transfer in transfers) {
                        check(app.prefs.historyAccess.value == access) { "Your account changed" }
                        receiveAndSaveChild(
                            client = client,
                            transferId = transfer.transferId,
                            files =
                                received
                                    .filter { it.first == transfer.transferId }
                                    .map { it.second },
                            key = requireNotNull(childKeys[transfer.transferId]),
                            saveFile = { fileMeta, plaintext ->
                                check(app.prefs.historyAccess.value == access) {
                                    "Your account changed"
                                }
                                saver.requireSpace(remainingBytes)
                                saver.save(fileMeta, plaintext) {}
                            },
                            recordSaved = { child ->
                                historyMutex.withLock {
                                    dao.mergeReceived(
                                        slotId,
                                        ReceivedSnapshot(mapOf(transfer.transferId to child)),
                                        saved = true,
                                    )
                                }
                            },
                            alreadySaved =
                                Json.decodeFromString<Set<String>>(
                                        dao.getById(slotId)?.savedFileIdsJson ?: "[]"
                                    )
                                    .filter { it.startsWith("${transfer.transferId}/") }
                                    .map { it.substringAfter('/') }
                                    .toSet(),
                            recordFileSaved = { blobId ->
                                dao.recordSavedFile(slotId, "${transfer.transferId}/$blobId")
                                remainingBytes -=
                                    received
                                        .first {
                                            it.first == transfer.transferId &&
                                                it.second.blobId == blobId
                                        }
                                        .second
                                        .size
                                val savedCount =
                                    Json.decodeFromString<Set<String>>(
                                            dao.getById(slotId)?.savedFileIdsJson ?: "[]"
                                        )
                                        .size
                                if (app.prefs.historyAccess.value == access)
                                    _uiState.update { it.copy(savedFileCount = savedCount) }
                            },
                            onFileSaved = {
                                savedFiles++
                                _uiState.update {
                                    it.copy(
                                        downloadProgress =
                                            savedFiles.toFloat() / totalFiles.toFloat()
                                    )
                                }
                            },
                        )
                    }

                    historyMutex.withLock {
                        val updated = dao.getById(slotId)
                        _uiState.update {
                            if (it.slotId == slotId)
                                it.copy(
                                    isDownloading = false,
                                    downloadProgress = 1f,
                                    slotStatus = updated?.status ?: "has_uploads",
                                    downloadComplete = updated?.status == "complete",
                                )
                            else it
                        }
                    }
                } catch (e: Exception) {
                    if (e is kotlinx.coroutines.CancellationException) throw e
                    val trafficError =
                        zip.psst.android.data.classifyTrafficFailure(e) {
                            client.slots.trafficStatus(slotId, "download")
                        }
                    _uiState.update {
                        it.copy(
                            isDownloading = false,
                            requiresLogin = e is AuthenticationRequiredException,
                            error =
                                trafficError?.message
                                    ?: if (
                                        e is
                                            zip.psst.android.data.InsufficientDownloadSpaceException ||
                                            e is zip.psst.shared.api.TransferPolicyException ||
                                            e is AuthenticationRequiredException
                                    )
                                        e.message
                                    else
                                        app.getString(
                                            zip.psst.android.R.string
                                                .ui_could_not_save_every_file_retry_saving_files_already_saved_will_b
                                        ),
                        )
                    }
                } finally {
                    client.close()
                    _uiState.update {
                        if (it.slotId == slotId) it.copy(isDownloading = false) else it
                    }
                }
            }
    }

    override fun onCleared() {
        super.onCleared()
        sseJob?.cancel()
        pollJob?.cancel()
        slotClient?.close()
        downloadJob?.cancel()
        createJob?.cancel()
    }
}
