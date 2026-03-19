package zip.psst.android.viewmodel

import android.app.Application
import android.net.Uri
import android.provider.OpenableColumns
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.shared.api.ApiClient
import zip.psst.shared.crypto.CryptoProvider
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
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlin.io.encoding.Base64
import kotlin.io.encoding.ExperimentalEncodingApi

data class FileInfo(
    val uri: Uri,
    val name: String,
    val size: Long,
    val mimeType: String,
)

data class SendUiState(
    val files: List<FileInfo> = emptyList(),
    val isUploading: Boolean = false,
    val uploadProgress: Float = 0f,
    val currentFileIndex: Int = 0,
    val error: String? = null,
    val transferId: String? = null,
    val encryptionKey: String? = null,
    val downloadUrl: String? = null,
)

class SendViewModel(application: Application) : AndroidViewModel(application) {

    private val app = application as PsstApplication
    private val _uiState = MutableStateFlow(SendUiState())
    val uiState: StateFlow<SendUiState> = _uiState.asStateFlow()

    private var uploadJob: Job? = null

    fun addFiles(uris: List<Uri>) {
        val context = getApplication<PsstApplication>()
        val newFiles = uris.mapNotNull { uri ->
            resolveFileInfo(context, uri)
        }
        _uiState.value = _uiState.value.copy(
            files = _uiState.value.files + newFiles,
            error = null,
        )
    }

    fun removeFile(index: Int) {
        val files = _uiState.value.files.toMutableList()
        if (index in files.indices) {
            files.removeAt(index)
            _uiState.value = _uiState.value.copy(files = files)
        }
    }

    @OptIn(ExperimentalEncodingApi::class)
    fun startUpload() {
        val files = _uiState.value.files
        if (files.isEmpty()) return

        val serverUrl = app.prefs.getServerUrl()
        if (serverUrl.isBlank()) {
            _uiState.value = _uiState.value.copy(error = "Server URL not configured")
            return
        }

        _uiState.value = _uiState.value.copy(isUploading = true, error = null, uploadProgress = 0f)

        uploadJob = viewModelScope.launch {
            try {
                val client = ApiClient(ServerConfig(serverUrl))
                val key = CryptoProvider.generateKey()
                val base64Key = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).encode(key)

                // Create transfer
                val transfer = client.transfers.create()
                _uiState.value = _uiState.value.copy(transferId = transfer.id)

                val context = getApplication<PsstApplication>()
                val fileMetadataList = mutableListOf<FileMetadata>()
                val totalBytes = files.sumOf { it.size }
                var uploadedBytes = 0L

                // Encrypt and upload each file
                for ((index, fileInfo) in files.withIndex()) {
                    _uiState.value = _uiState.value.copy(currentFileIndex = index)

                    val inputStream = context.contentResolver.openInputStream(fileInfo.uri)
                        ?: throw Exception("Cannot read file: ${fileInfo.name}")

                    val plaintext = inputStream.use { it.readBytes() }

                    // Encrypt the file
                    val nonce = CryptoProvider.generateNonce()
                    val ciphertext = CryptoProvider.encrypt(key, nonce, plaintext)
                    val encryptedData = nonce + ciphertext

                    // Upload via tus
                    val resourceUrl = client.uploadFile(
                        transferId = transfer.id,
                        data = encryptedData,
                        metadata = mapOf("filename" to fileInfo.name),
                    ) { uploaded ->
                        val progress = (uploadedBytes + uploaded).toFloat() / totalBytes.toFloat()
                        _uiState.value = _uiState.value.copy(
                            uploadProgress = progress.coerceIn(0f, 1f),
                        )
                    }

                    uploadedBytes += fileInfo.size

                    // Extract blob ID from resource URL
                    val blobId = resourceUrl.substringAfterLast("/")
                    fileMetadataList.add(
                        FileMetadata(
                            name = fileInfo.name,
                            size = fileInfo.size,
                            mimeType = fileInfo.mimeType,
                            blobId = blobId,
                        ),
                    )
                }

                // Create and upload encrypted manifest
                val manifest = Manifest(files = fileMetadataList)
                val manifestJson = Json.encodeToString(manifest)
                val manifestNonce = CryptoProvider.generateNonce()
                val manifestCiphertext = CryptoProvider.encrypt(
                    key,
                    manifestNonce,
                    manifestJson.encodeToByteArray(),
                )
                val encryptedManifest = EncryptedManifest(
                    ciphertext = manifestCiphertext,
                    nonce = manifestNonce,
                )
                client.transfers.uploadManifest(transfer.id, encryptedManifest.toBytes())

                // Complete the transfer
                client.transfers.complete(transfer.id)
                client.close()

                val downloadUrl = UrlHelper.buildDownloadUrl(serverUrl, transfer.id, key)

                _uiState.value = _uiState.value.copy(
                    isUploading = false,
                    uploadProgress = 1f,
                    encryptionKey = base64Key,
                    downloadUrl = downloadUrl,
                )

                // Save to history
                app.database.transferHistoryDao().insert(
                    TransferHistoryEntity(
                        id = transfer.id,
                        type = "sent",
                        fileCount = files.size,
                        totalSize = totalBytes,
                        serverUrl = serverUrl,
                        encryptionKey = base64Key,
                        status = "complete",
                        expiresAt = null,
                    ),
                )
            } catch (e: Exception) {
                if (e is kotlinx.coroutines.CancellationException) throw e
                _uiState.value = _uiState.value.copy(
                    isUploading = false,
                    error = e.message ?: "Upload failed",
                )
            }
        }
    }

    fun cancelUpload() {
        uploadJob?.cancel()
        uploadJob = null
        _uiState.value = _uiState.value.copy(isUploading = false, uploadProgress = 0f)
    }

    private fun resolveFileInfo(context: android.content.Context, uri: Uri): FileInfo? {
        val cursor = context.contentResolver.query(uri, null, null, null, null) ?: return null
        return cursor.use {
            if (!it.moveToFirst()) return null
            val nameIndex = it.getColumnIndex(OpenableColumns.DISPLAY_NAME)
            val sizeIndex = it.getColumnIndex(OpenableColumns.SIZE)
            val name = if (nameIndex >= 0) it.getString(nameIndex) else "unknown"
            val size = if (sizeIndex >= 0) it.getLong(sizeIndex) else 0L
            val mimeType = context.contentResolver.getType(uri) ?: "application/octet-stream"
            FileInfo(uri = uri, name = name, size = size, mimeType = mimeType)
        }
    }
}
