package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.ReceivedSnapshot
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.parseHistoryExpiry
import zip.psst.android.data.receiveAndSaveChild
import zip.psst.android.data.receivedSnapshot
import zip.psst.android.data.retrySavedDownloadAcknowledgements
import zip.psst.android.data.savedFileCount
import zip.psst.android.data.savedTransferIds
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.AuthenticationRequiredException
import zip.psst.shared.api.SlotEvent
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.model.EncryptedManifest
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.Manifest
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
)

class ReceiveViewModel(application: Application) : AndroidViewModel(application) {

    private val app = application as PsstApplication
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
                if (row.encryptionKey.isBlank()) {
                    _uiState.value =
                        ReceiveUiState(
                            slotId = row.id,
                            slotStatus = row.status,
                            error = app.getString(zip.psst.android.R.string.unavailable_key),
                            keyUnavailable = true,
                        )
                    return@launch
                }
                try {
                    val key =
                        Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)
                            .decode(row.encryptionKey)
                    require(key.size == 32)
                    check(app.prefs.historyAccess.value == access)
                    encryptionKeyBytes = key
                    val client = ApiClient(ServerConfig(row.serverUrl))
                    slotClient = client
                    _uiState.value = restoreReceiveEntry(row, access)
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
        } else {
            val id = _uiState.value.slotId
            val client = slotClient
            if (id != null && client != null) listenForEvents(client, id)
        }
    }

    fun reconnect() = setVisible(true)

    @OptIn(ExperimentalEncodingApi::class)
    fun createSlot() {
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
        _uiState.value = ReceiveUiState(isCreatingSlot = true)

        val access = app.prefs.historyAccess.value
        createJob =
            viewModelScope.launch(Dispatchers.IO) {
                try {
                    val client = ApiClient(ServerConfig(serverUrl), sessionToken = sessionToken)
                    slotClient = client
                    val slot = client.slots.create()
                    val key = CryptoProvider.generateKey()
                    encryptionKeyBytes = key
                    val base64Key =
                        Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).encode(key)
                    val uploadUrl = UrlHelper.buildUploadUrl(serverUrl, slot.id, key)

                    // Save to history
                    withContext(NonCancellable) {
                        app.database
                            .transferHistoryDao()
                            .insert(
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
                            requiresLogin = e is AuthenticationRequiredException,
                            error =
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
                            )
                        else it
                    }
                }
            }
        } catch (e: Exception) {
            if (e is CancellationException) throw e
            if (
                e is io.ktor.client.plugins.ClientRequestException &&
                    e.response.status.value in listOf(404, 410)
            ) {
                app.database.transferHistoryDao().updateStatus(slotId, "unavailable")
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

    fun downloadReceivedFiles() {
        val slotId = _uiState.value.slotId ?: return
        val key = encryptionKeyBytes ?: return
        if (_uiState.value.isDownloading) return

        _uiState.update { it.copy(isDownloading = true, error = null, downloadComplete = false) }

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
                val client = ApiClient(ServerConfig(row.serverUrl))
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
                    for (transfer in transfers) {
                        val manifestBytes = client.transfers.downloadManifest(transfer.transferId)
                        val encManifest = EncryptedManifest.fromBytes(manifestBytes)
                        val manifestPlaintext =
                            CryptoProvider.decrypt(key, encManifest.nonce, encManifest.ciphertext)
                        val manifest =
                            Json.decodeFromString<Manifest>(manifestPlaintext.decodeToString())

                        zip.psst.shared.model.ManifestValidator.validate(manifest)
                        require(manifest.files.size == transfer.fileCount) {
                            "Manifest file count mismatch"
                        }
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
                            key = key,
                            saveFile = { fileMeta, plaintext ->
                                check(app.prefs.historyAccess.value == access) {
                                    "Your account changed"
                                }
                                zip.psst.android.data.GuestFileSaver(context).save(
                                    fileMeta,
                                    plaintext,
                                ) {}
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
                    _uiState.update {
                        it.copy(
                            isDownloading = false,
                            error =
                                app.getString(
                                    zip.psst.android.R.string
                                        .ui_could_not_save_every_file_retry_saving_files_already_saved_will_b
                                ),
                        )
                    }
                } finally {
                    client.close()
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
