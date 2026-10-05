package zip.psst.android.viewmodel

import android.app.Application
import android.net.Uri
import android.provider.OpenableColumns
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.spoolUpload
import zip.psst.android.data.uploadChunkedFile
import zip.psst.shared.api.AdminTransferForbiddenException
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.AuthenticationRequiredException
import zip.psst.shared.api.PasswordChangeRequiredException
import zip.psst.shared.crypto.ChunkedFileCrypto
import zip.psst.shared.crypto.CryptoProvider
import zip.psst.shared.model.EncryptedManifest
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.Manifest
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.TransferLimits
import zip.psst.shared.model.UrlHelper
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
    val sharedTitle: String = "",
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
    val maxFileBytes: Long? = null,
    val completionConsumed: Boolean = false,
    val maxDownloadsInput: String = "",
    val downloadLimitEnabled: Boolean = false,
    val linkPolicyLocked: Boolean = false,
) {
    fun pendingCompletion(): Pair<String, String>? =
        if (isUploading || completionConsumed) null
        else transferId?.let { id -> encryptionKey?.let { id to it } }
}

class SendViewModel(application: Application) : AndroidViewModel(application) {
    fun setSharedTitle(value: String) {
        if (!_uiState.value.isUploading) _uiState.update { it.copy(sharedTitle = value) }
    }

    fun setMaxDownloads(value: String) {
        if (!_uiState.value.isUploading && !_uiState.value.linkPolicyLocked)
            _uiState.update { it.copy(maxDownloadsInput = value.take(11), error = null) }
    }

    fun setDownloadLimitEnabled(enabled: Boolean) {
        if (!_uiState.value.isUploading && !_uiState.value.linkPolicyLocked)
            _uiState.update { it.copy(downloadLimitEnabled = enabled, error = null) }
    }

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
                    val recovery = selectionRecovery(previous.requiresLogin, pendingAccess, access)
                    _uiState.value = restoreSendDraft(previous, recovery)
                }
            }
        }
    }

    fun consumeCompletion(): Pair<String, String>? {
        val state = _uiState.value
        val completion = state.pendingCompletion() ?: return null
        _uiState.value = state.copy(completionConsumed = true)
        return completion
    }

    fun refreshLimit() {
        val origin = app.prefs.getServerUrl()
        if (origin.isBlank()) return
        viewModelScope.launch(Dispatchers.IO) {
            val client = ApiClient.anonymous(origin)
            try {
                val limit = client.limits.get().maxFileSize
                if (app.prefs.getServerUrl() == origin)
                    _uiState.update { it.copy(maxFileBytes = limit) }
            } catch (_: Exception) {
                /* Upload retries policy retrieval and surfaces failure. */
            } finally {
                client.close()
            }
        }
    }

    fun addFiles(uris: List<Uri>) {
        val context = getApplication<PsstApplication>()
        val newFiles = uris.mapNotNull { uri -> resolveFileInfo(context, uri) }
        val limit = _uiState.value.maxFileBytes
        val rejected = limit != null && newFiles.any { it.size > limit }
        val accepted = newFiles.filter { limit == null || it.size <= limit }
        _uiState.update {
            it.copy(
                files = (it.files + accepted).distinctBy { file -> file.uri },
                error =
                    if (rejected) "A selected file exceeds this server’s per-file limit" else null,
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
        val selectedLimit =
            try {
                zip.psst.android.data.selectedLinkLimit(
                    _uiState.value.downloadLimitEnabled,
                    _uiState.value.maxDownloadsInput,
                )
            } catch (e: IllegalArgumentException) {
                _uiState.update { it.copy(error = e.message) }
                return
            }
        val files = _uiState.value.files
        if (files.isEmpty()) return

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
                    val maxBytes = client.limits.get().maxFileSize
                    _uiState.update { it.copy(maxFileBytes = maxBytes) }
                    require(files.size <= TransferLimits.MAX_FILES) { "Too many files" }
                    require(files.all { it.size <= maxBytes }) {
                        "A file exceeds this server’s per-file limit"
                    }
                    val key = CryptoProvider.generateKey()
                    val base64Key =
                        Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).encode(key)

                    val totalBytes = files.sumOf { it.size }
                    var totalEncryptedBytes = files.sumOf { ChunkedFileCrypto.wireSize(it.size) }

                    // Create transfer
                    val maxDownloads = selectedLimit
                    val transfer =
                        client.transfers.create(
                            maxDownloads,
                            zip.psst.shared.model.LinkTitle.normalize(_uiState.value.sharedTitle),
                        )
                    createdTransferId = transfer.id
                    deletionToken = transfer.deleteToken
                    if (app.prefs.historyAccess.value == access)
                        _uiState.update { it.copy(linkPolicyLocked = true) }
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
                                automaticTitle = files.firstOrNull()?.name,
                                sharedTitle = transfer.title,
                                maxDownloads = maxDownloads,
                            )
                        )
                    _uiState.update { it.copy(transferId = transfer.id) }
                    if (maxDownloads > 0)
                        zip.psst.android.data.verifyDownloadPolicy(
                            client.transfers.get(transfer.id),
                            maxDownloads,
                        )

                    val context = getApplication<PsstApplication>()
                    val fileMetadataList = mutableListOf<FileMetadata>()
                    var uploadedBytes = 0L

                    // Encrypt and upload each file
                    for ((index, fileInfo) in files.withIndex()) {
                        _uiState.update { it.copy(currentFileIndex = index, isPreparing = true) }

                        val snapshot = spoolUpload(context, fileInfo.uri, maxBytes)
                        try {
                            totalEncryptedBytes +=
                                ChunkedFileCrypto.wireSize(snapshot.length()) -
                                    ChunkedFileCrypto.wireSize(fileInfo.size)
                            _uiState.update {
                                it.copy(totalUploadBytes = totalEncryptedBytes, isPreparing = false)
                            }
                            ensureActive()
                            check(app.prefs.historyAccess.value == access)
                            val metadata =
                                uploadChunkedFile(
                                    client,
                                    transfer.id,
                                    snapshot,
                                    fileInfo.name,
                                    fileInfo.mimeType,
                                    key,
                                ) { uploaded ->
                                    _uiState.update {
                                        it.copy(
                                            uploadProgress =
                                                ((uploadedBytes + uploaded).toFloat() /
                                                        totalEncryptedBytes)
                                                    .coerceIn(0f, 1f),
                                            uploadedBytes = uploadedBytes + uploaded,
                                        )
                                    }
                                }
                            uploadedBytes += ChunkedFileCrypto.wireSize(metadata.size)
                            fileMetadataList.add(metadata)
                        } finally {
                            snapshot.delete()
                        }
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
                    val trafficError =
                        createdTransferId?.let { id ->
                            zip.psst.android.data.classifyTrafficFailure(e) {
                                client.transfers.trafficStatus(id, "upload")
                            }
                        }
                    if (
                        e is AuthenticationRequiredException ||
                            e is PasswordChangeRequiredException ||
                            e is AdminTransferForbiddenException
                    )
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
                            requiresLogin =
                                e is AuthenticationRequiredException ||
                                    e is PasswordChangeRequiredException ||
                                    e is AdminTransferForbiddenException,
                            error =
                                trafficError?.message
                                    ?: if (
                                        e is zip.psst.shared.api.TransferPolicyException ||
                                            e is
                                                zip.psst.android.data.UnsupportedLinkPolicyException ||
                                            e is PasswordChangeRequiredException ||
                                            e is AdminTransferForbiddenException
                                    )
                                        e.message
                                    else app.getString(zip.psst.android.R.string.upload_failed),
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
                                                listOfNotNull(
                                                        it.error,
                                                        app.getString(
                                                            zip.psst.android.R.string.cleanup_failed
                                                        ),
                                                    )
                                                    .distinct()
                                                    .joinToString("\n")
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
    (origin.serverUrl.isBlank() ||
        origin.serverUrl.trimEnd('/') == current.serverUrl.trimEnd('/')) &&
        (origin.accountId == null || origin.accountId == current.accountId)

/** A restricted temporary session is one step of reauthentication, not its completion. */
internal enum class SelectionRecovery {
    DISCARD,
    RESTRICTED,
    READY,
}

internal fun selectionRecovery(
    pendingLogin: Boolean,
    origin: zip.psst.android.data.HistoryAccess,
    current: zip.psst.android.data.HistoryAccess,
): SelectionRecovery =
    when {
        !pendingLogin -> SelectionRecovery.DISCARD
        current.isAdmin -> SelectionRecovery.DISCARD
        current.accountId == null -> SelectionRecovery.RESTRICTED
        !canResumeSelection(origin, current) -> SelectionRecovery.DISCARD
        current.mustChangePassword -> SelectionRecovery.RESTRICTED
        else -> SelectionRecovery.READY
    }

/** Retain the selected protection together with files, never old completion/navigation state. */
internal fun restoreSendDraft(previous: SendUiState, recovery: SelectionRecovery): SendUiState =
    if (recovery == SelectionRecovery.DISCARD) SendUiState()
    else
        SendUiState(
            files = previous.files,
            sharedTitle = previous.sharedTitle,
            maxDownloadsInput = previous.maxDownloadsInput,
            downloadLimitEnabled = previous.downloadLimitEnabled,
            linkPolicyLocked = previous.linkPolicyLocked,
            requiresLogin = recovery == SelectionRecovery.RESTRICTED,
        )
