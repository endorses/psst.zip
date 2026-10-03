package zip.psst.android.viewmodel

import android.app.Application
import android.net.Uri
import android.provider.OpenableColumns
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.AuthenticationRequiredException
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.model.EncryptedManifest
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.Manifest
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.TransferLimits
import zip.psst.shared.model.UrlHelper
import java.io.ByteArrayOutputStream
import kotlin.io.encoding.Base64
import kotlin.io.encoding.ExperimentalEncodingApi
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

data class FileInfo(val uri: Uri, val name: String, val size: Long, val mimeType: String)

data class SendUiState(
    val files: List<FileInfo> = emptyList(),
    val isUploading: Boolean = false,
    val uploadProgress: Float = 0f,
    val isPreparing: Boolean = false,
    val uploadedBytes: Long = 0,
    val totalUploadBytes: Long = 0,
    val currentFileIndex: Int = 0,
    val error: String? = null,
    val requiresLogin: Boolean = false,
    val transferId: String? = null,
    val encryptionKey: String? = null,
    val downloadUrl: String? = null,
)

class SendViewModel(application: Application) : AndroidViewModel(application) {

    private val app = application as PsstApplication
    private val _uiState = MutableStateFlow(SendUiState())
    val uiState: StateFlow<SendUiState> = _uiState.asStateFlow()

    private var uploadJob: Job? = null
    private var activeAccess = app.prefs.historyAccess.value
    private var pendingAccess = activeAccess

    init {
        viewModelScope.launch {
            app.prefs.historyAccess.collect { access ->
                if (activeAccess != access) {
                    activeAccess = access
                    uploadJob?.cancel()
                    val previous = _uiState.value
                    val canKeepSelection =
                        previous.requiresLogin &&
                            (access.accountId == null || canResumeSelection(pendingAccess, access))
                    _uiState.value =
                        if (canKeepSelection)
                            SendUiState(
                                files = previous.files,
                                requiresLogin = access.accountId == null,
                            )
                        else SendUiState()
                }
            }
        }
    }

    fun addFiles(uris: List<Uri>) {
        val context = getApplication<PsstApplication>()
        val newFiles = uris.mapNotNull { uri -> resolveFileInfo(context, uri) }
        val rejected = newFiles.any { it.size > TransferLimits.MAX_FILE_BYTES }
        val accepted = newFiles.filter { it.size <= TransferLimits.MAX_FILE_BYTES }
        _uiState.update {
            it.copy(
                files = (it.files + accepted).distinctBy { file -> file.uri },
                error = if (rejected) app.getString(zip.psst.android.R.string.file_limit) else null,
            )
        }
    }

    fun removeFile(index: Int) {
        if (_uiState.value.isUploading) return
        val files = _uiState.value.files.toMutableList()
        if (index in files.indices) {
            files.removeAt(index)
            _uiState.update { it.copy(files = files) }
        }
    }

    @OptIn(ExperimentalEncodingApi::class)
    fun startUpload() {
        if (_uiState.value.isUploading) return
        val files = _uiState.value.files
        if (files.isEmpty()) return
        if (files.any { it.size > TransferLimits.MAX_FILE_BYTES }) {
            _uiState.update {
                it.copy(
                    error =
                        app.getString(zip.psst.android.R.string.ui_files_up_to_25_mib_are_supported)
                )
            }
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
                                .ui_sign_in_under_server_configuration_to_upload_files
                        ),
                )
            }
            return
        }

        val access = app.prefs.historyAccess.value
        pendingAccess = access
        _uiState.update {
            it.copy(
                isUploading = true,
                isPreparing = true,
                error = null,
                uploadProgress = 0f,
                uploadedBytes = 0,
                totalUploadBytes = files.sumOf { f -> f.size + 28 },
            )
        }

        uploadJob =
            viewModelScope.launch(Dispatchers.IO) {
                val client = ApiClient(ServerConfig(serverUrl), sessionToken = sessionToken)
                var createdTransferId: String? = null
                var deletionToken: String? = null
                var completed = false
                try {
                    val key = CryptoProvider.generateKey()
                    val base64Key =
                        Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).encode(key)

                    val totalBytes = files.sumOf { it.size }
                    var totalEncryptedBytes = totalBytes + files.size * 28L

                    // Create transfer
                    val transfer = client.transfers.create()
                    createdTransferId = transfer.id
                    deletionToken = transfer.deleteToken
                    ensureActive()
                    check(app.prefs.historyAccess.value == access)
                    // Retain the owner capability even if uploading is interrupted.
                    app.database
                        .transferHistoryDao()
                        .insert(
                            TransferHistoryEntity(
                                id = transfer.id,
                                type = "sent",
                                fileCount = files.size,
                                totalSize = totalBytes,
                                serverUrl = serverUrl,
                                encryptionKey = base64Key,
                                status = "pending",
                                deletionToken = transfer.deleteToken,
                                accountId = accountId,
                                title = files.firstOrNull()?.name,
                            )
                        )
                    _uiState.update { it.copy(transferId = transfer.id) }

                    val context = getApplication<PsstApplication>()
                    val fileMetadataList = mutableListOf<FileMetadata>()
                    var uploadedBytes = 0L

                    // Encrypt and upload each file
                    for ((index, fileInfo) in files.withIndex()) {
                        _uiState.update { it.copy(currentFileIndex = index, isPreparing = true) }

                        val inputStream =
                            context.contentResolver.openInputStream(fileInfo.uri)
                                ?: throw Exception("Cannot read file: ${fileInfo.name}")

                        val plaintext =
                            inputStream.use { input ->
                                val output = ByteArrayOutputStream()
                                val buffer = ByteArray(8192)
                                while (true) {
                                    ensureActive()
                                    val count = input.read(buffer)
                                    if (count == -1) break
                                    require(
                                        output.size() + count <= TransferLimits.MAX_FILE_BYTES
                                    ) {
                                        "Files up to 25 MiB are supported"
                                    }
                                    output.write(buffer, 0, count)
                                }
                                output.toByteArray()
                            }

                        // Encrypt the file
                        val nonce = CryptoProvider.generateNonce()
                        val ciphertext = CryptoProvider.encrypt(key, nonce, plaintext)
                        val encryptedData = nonce + ciphertext
                        totalEncryptedBytes += plaintext.size.toLong() - fileInfo.size
                        _uiState.update { it.copy(totalUploadBytes = totalEncryptedBytes) }

                        // Upload via tus
                        ensureActive()
                        check(app.prefs.historyAccess.value == access)
                        _uiState.update { it.copy(isPreparing = false) }
                        val resourceUrl =
                            client.uploadFile(transferId = transfer.id, data = encryptedData) {
                                uploaded ->
                                val progress =
                                    (uploadedBytes + uploaded).toFloat() /
                                        totalEncryptedBytes.toFloat()
                                _uiState.update {
                                    it.copy(
                                        uploadProgress = progress.coerceIn(0f, 1f),
                                        uploadedBytes = uploadedBytes + uploaded,
                                    )
                                }
                            }

                        uploadedBytes += encryptedData.size

                        // Extract blob ID from resource URL
                        val blobId = resourceUrl.substringAfterLast("/")
                        fileMetadataList.add(
                            FileMetadata(
                                name = fileInfo.name,
                                size = plaintext.size.toLong(),
                                mimeType = fileInfo.mimeType,
                                blobId = blobId,
                            )
                        )
                    }

                    // Create and upload encrypted manifest
                    val manifest = Manifest(files = fileMetadataList)
                    val manifestJson = Json.encodeToString(manifest)
                    val manifestNonce = CryptoProvider.generateNonce()
                    val manifestCiphertext =
                        CryptoProvider.encrypt(key, manifestNonce, manifestJson.encodeToByteArray())
                    val encryptedManifest =
                        EncryptedManifest(ciphertext = manifestCiphertext, nonce = manifestNonce)
                    client.transfers.uploadManifest(transfer.id, encryptedManifest.toBytes())

                    // Complete the transfer
                    client.transfers.complete(transfer.id)
                    ensureActive()
                    check(app.prefs.historyAccess.value == access)
                    completed = true

                    val downloadUrl = UrlHelper.buildDownloadUrl(serverUrl, transfer.id, key)

                    _uiState.update {
                        it.copy(
                            isUploading = false,
                            uploadProgress = 1f,
                            encryptionKey = base64Key,
                            downloadUrl = downloadUrl,
                        )
                    }

                    app.database.transferHistoryDao().updateStatus(transfer.id, "complete")
                } catch (e: Exception) {
                    if (e is CancellationException) throw e
                    if (e is AuthenticationRequiredException)
                        _uiState.update { it.copy(requiresLogin = true) }
                    if (
                        e is AuthenticationRequiredException &&
                            app.prefs.getSessionToken(serverUrl) == sessionToken
                    )
                        app.prefs.clearSession()
                    createdTransferId?.let {
                        app.database.transferHistoryDao().updateStatus(it, "failed")
                    }
                    _uiState.update {
                        it.copy(
                            isUploading = false,
                            requiresLogin = e is AuthenticationRequiredException,
                            error = app.getString(zip.psst.android.R.string.upload_failed),
                        )
                    }
                } finally {
                    if (!completed && createdTransferId != null) {
                        withContext(NonCancellable) {
                            val id = createdTransferId
                            try {
                                client.transfers.delete(id, deletionToken)
                                app.database.transferHistoryDao().delete(id)
                            } catch (_: Exception) {
                                app.database.transferHistoryDao().updateStatus(id, "failed")
                                if (app.prefs.historyAccess.value == access)
                                    _uiState.update {
                                        it.copy(
                                            error =
                                                app.getString(
                                                    zip.psst.android.R.string.cleanup_failed
                                                )
                                        )
                                    }
                            }
                        }
                    }
                    client.close()
                    if (app.prefs.historyAccess.value == access)
                        _uiState.update { it.copy(isUploading = false, isPreparing = false) }
                }
            }
    }

    fun cancelUpload() {
        uploadJob?.cancel()
        uploadJob = null
        _uiState.update { it.copy(isPreparing = true) }
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

internal fun canResumeSelection(
    origin: zip.psst.android.data.HistoryAccess,
    current: zip.psst.android.data.HistoryAccess,
): Boolean =
    origin.accountId == null ||
        (origin.accountId == current.accountId &&
            origin.serverUrl.trimEnd('/') == current.serverUrl.trimEnd('/'))
