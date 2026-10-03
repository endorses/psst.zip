package zip.psst.android.viewmodel

import android.app.Application
import android.content.ContentValues
import android.os.Environment
import android.provider.MediaStore
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.ReceivedSnapshot
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.parseHistoryExpiry
import zip.psst.android.data.receiveAndSaveChild
import zip.psst.android.data.receivedSnapshot
import zip.psst.android.data.retrySavedDownloadAcknowledgements
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
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
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
                            error = "Your account changed. Create a new receive link to continue.",
                            requiresLogin = access.accountId == null,
                        )
                }
            }
        }
    }

    @OptIn(ExperimentalEncodingApi::class)
    fun createSlot() {
        val serverUrl = app.prefs.getServerUrl()
        if (serverUrl.isBlank()) {
            _uiState.update { it.copy(error = "Server URL not configured") }
            return
        }

        val sessionToken = app.prefs.getSessionToken(serverUrl)
        val accountId = app.prefs.getAccountId()
        if (sessionToken == null || accountId == null) {
            _uiState.update {
                it.copy(
                    requiresLogin = true,
                    error = "Sign in under Server Configuration to create receive links.",
                )
            }
            return
        }

        sseJob?.cancel()
        pollJob?.cancel()
        slotClient?.close()
        _uiState.value = ReceiveUiState(isCreatingSlot = true)

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

                    _uiState.update {
                        it.copy(
                            isCreatingSlot = false,
                            slotId = slot.id,
                            encryptionKey = base64Key,
                            uploadUrl = uploadUrl,
                        )
                    }

                    // Save to history
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

                    // Start listening for SSE events
                    listenForEvents(client, slot.id)
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
                            error = e.message ?: "Failed to create drop slot",
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
                    delay(3000L)
                }
            }
    }

    private suspend fun refreshSlot(client: ApiClient, slotId: String) {
        try {
            val visibleRow = app.database.transferHistoryDao().getById(slotId) ?: return
            if (!app.prefs.historyAccess.value.permits(visibleRow)) return
            retrySavedDownloadAcknowledgements(visibleRow, client)
            val slot = client.slots.get(slotId)
            val snapshot = slot.receivedSnapshot()
            historyMutex.withLock {
                val row = app.database.transferHistoryDao().mergeReceived(slotId, snapshot)
                if (row != null) {
                    _uiState.update {
                        if (it.slotId == slotId)
                            it.copy(
                                slotStatus = row.status,
                                downloadComplete = row.status == "complete",
                            )
                        else it
                    }
                }
            }
        } catch (e: Exception) {
            if (e is CancellationException) throw e
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
                        it.copy(isDownloading = false, error = "Receive history entry is missing")
                    }
                    return@launch
                }
                val client = ApiClient(ServerConfig(row.serverUrl))
                try {
                    val slot = client.slots.get(slotId)
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

                        require(manifest.files.size == transfer.fileCount) {
                            "Manifest file count mismatch"
                        }
                        received += manifest.files.map { transfer.transferId to it }
                    }
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
                                val contentValues =
                                    ContentValues().apply {
                                        put(MediaStore.Downloads.DISPLAY_NAME, fileMeta.name)
                                        put(MediaStore.Downloads.MIME_TYPE, fileMeta.mimeType)
                                        put(
                                            MediaStore.Downloads.RELATIVE_PATH,
                                            Environment.DIRECTORY_DOWNLOADS,
                                        )
                                    }
                                val uri =
                                    context.contentResolver.insert(
                                        MediaStore.Downloads.EXTERNAL_CONTENT_URI,
                                        contentValues,
                                    ) ?: throw Exception("Failed to create file in Downloads")
                                context.contentResolver.openOutputStream(uri)?.use {
                                    it.write(plaintext)
                                } ?: throw Exception("Failed to write file")
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
                        it.copy(isDownloading = false, error = e.message ?: "Download failed")
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
