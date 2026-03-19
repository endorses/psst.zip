package zip.psst.android.viewmodel

import android.app.Application
import android.content.ContentValues
import android.os.Environment
import android.provider.MediaStore
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.SlotEvent
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.model.DropSlotStatus
import zip.psst.shared.model.EncryptedManifest
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.Manifest
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.UrlHelper
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import kotlin.io.encoding.Base64
import kotlin.io.encoding.ExperimentalEncodingApi

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
    val downloadComplete: Boolean = false,
)

class ReceiveViewModel(application: Application) : AndroidViewModel(application) {

    private val app = application as PsstApplication
    private val _uiState = MutableStateFlow(ReceiveUiState())
    val uiState: StateFlow<ReceiveUiState> = _uiState.asStateFlow()

    private var sseJob: Job? = null
    private var encryptionKeyBytes: ByteArray? = null

    @OptIn(ExperimentalEncodingApi::class)
    fun createSlot() {
        val serverUrl = app.prefs.getServerUrl()
        if (serverUrl.isBlank()) {
            _uiState.value = _uiState.value.copy(error = "Server URL not configured")
            return
        }

        _uiState.value = _uiState.value.copy(isCreatingSlot = true, error = null)

        viewModelScope.launch {
            try {
                val client = ApiClient(ServerConfig(serverUrl))
                val slot = client.slots.create()
                val key = CryptoProvider.generateKey()
                encryptionKeyBytes = key
                val base64Key = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).encode(key)
                val uploadUrl = UrlHelper.buildUploadUrl(serverUrl, slot.id, key)

                _uiState.value = _uiState.value.copy(
                    isCreatingSlot = false,
                    slotId = slot.id,
                    encryptionKey = base64Key,
                    uploadUrl = uploadUrl,
                )

                // Save to history
                app.database.transferHistoryDao().insert(
                    TransferHistoryEntity(
                        id = slot.id,
                        type = "received",
                        fileCount = 0,
                        totalSize = 0,
                        serverUrl = serverUrl,
                        encryptionKey = base64Key,
                        status = "waiting",
                        expiresAt = null,
                    ),
                )

                // Start listening for SSE events
                listenForEvents(client, slot.id)
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(
                    isCreatingSlot = false,
                    error = e.message ?: "Failed to create drop slot",
                )
            }
        }
    }

    private fun listenForEvents(client: ApiClient, slotId: String) {
        sseJob?.cancel()
        sseJob = viewModelScope.launch {
            try {
                client.slots.events(slotId).collect { event: SlotEvent ->
                    when (event.event) {
                        "upload_complete", "file_uploaded" -> {
                            _uiState.value = _uiState.value.copy(slotStatus = "has_uploads")
                            // Refresh slot status to get file list
                            refreshSlot(client, slotId)
                        }
                    }
                }
            } catch (_: Exception) {
                // SSE connection closed or error; fall back to polling
                pollSlotStatus(client, slotId)
            }
        }
    }

    private suspend fun refreshSlot(client: ApiClient, slotId: String) {
        try {
            val slot = client.slots.get(slotId)
            if (slot.status == DropSlotStatus.HAS_UPLOADS) {
                _uiState.value = _uiState.value.copy(
                    slotStatus = "has_uploads",
                )
                // Update history
                app.database.transferHistoryDao().updateStatus(slotId, "has_uploads")
            }
        } catch (_: Exception) {
            // Ignore refresh errors
        }
    }

    private suspend fun pollSlotStatus(client: ApiClient, slotId: String) {
        while (true) {
            try {
                val slot = client.slots.get(slotId)
                if (slot.status == DropSlotStatus.HAS_UPLOADS) {
                    _uiState.value = _uiState.value.copy(slotStatus = "has_uploads")
                    app.database.transferHistoryDao().updateStatus(slotId, "has_uploads")
                    break
                }
            } catch (_: Exception) {
                // Ignore and retry
            }
            kotlinx.coroutines.delay(3000L)
        }
    }

    fun downloadReceivedFiles() {
        val slotId = _uiState.value.slotId ?: return
        val key = encryptionKeyBytes ?: return
        val serverUrl = app.prefs.getServerUrl()

        _uiState.value = _uiState.value.copy(isDownloading = true, error = null)

        viewModelScope.launch {
            try {
                val client = ApiClient(ServerConfig(serverUrl))

                // The slot acts like a transfer on the server side for file downloads.
                // Download the manifest first.
                val manifestBytes = client.transfers.downloadManifest(slotId)
                val encManifest = EncryptedManifest.fromBytes(manifestBytes)
                val manifestPlaintext = CryptoProvider.decrypt(
                    key,
                    encManifest.nonce,
                    encManifest.ciphertext,
                )
                val manifest = Json.decodeFromString<Manifest>(
                    manifestPlaintext.decodeToString(),
                )

                _uiState.value = _uiState.value.copy(receivedFiles = manifest.files)

                val context = getApplication<PsstApplication>()
                val totalFiles = manifest.files.size

                // Download and decrypt each file
                for ((index, fileMeta) in manifest.files.withIndex()) {
                    _uiState.value = _uiState.value.copy(
                        downloadProgress = index.toFloat() / totalFiles.toFloat(),
                    )

                    val encryptedData = client.transfers.downloadFile(slotId, fileMeta.blobId)

                    // Decrypt: first 12 bytes are nonce, rest is ciphertext
                    val nonce = encryptedData.copyOfRange(0, 12)
                    val ciphertext = encryptedData.copyOfRange(12, encryptedData.size)
                    val plaintext = CryptoProvider.decrypt(key, nonce, ciphertext)

                    // Save to Downloads via MediaStore
                    val contentValues = ContentValues().apply {
                        put(MediaStore.Downloads.DISPLAY_NAME, fileMeta.name)
                        put(MediaStore.Downloads.MIME_TYPE, fileMeta.mimeType)
                        put(
                            MediaStore.Downloads.RELATIVE_PATH,
                            Environment.DIRECTORY_DOWNLOADS + "/Psst",
                        )
                    }

                    val uri = context.contentResolver.insert(
                        MediaStore.Downloads.EXTERNAL_CONTENT_URI,
                        contentValues,
                    ) ?: throw Exception("Failed to create file in Downloads")

                    context.contentResolver.openOutputStream(uri)?.use { outputStream ->
                        outputStream.write(plaintext)
                    } ?: throw Exception("Failed to write file")
                }

                client.close()

                _uiState.value = _uiState.value.copy(
                    isDownloading = false,
                    downloadProgress = 1f,
                    downloadComplete = true,
                )
            } catch (e: Exception) {
                if (e is kotlinx.coroutines.CancellationException) throw e
                _uiState.value = _uiState.value.copy(
                    isDownloading = false,
                    error = e.message ?: "Download failed",
                )
            }
        }
    }

    override fun onCleared() {
        super.onCleared()
        sseJob?.cancel()
    }
}
