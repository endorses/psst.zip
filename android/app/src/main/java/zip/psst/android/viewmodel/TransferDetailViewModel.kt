package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import zip.psst.android.PsstApplication
import zip.psst.android.data.refreshHistoryEntry
import zip.psst.shared.model.UrlHelper
import java.time.Instant
import kotlin.io.encoding.ExperimentalEncodingApi
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

data class TransferDetailUiState(
    val transferId: String = "",
    val type: String = "send",
    val status: String = "pending",
    val fileCount: Int = 0,
    val totalSize: Long = 0,
    val expiresAt: String? = null,
    val shareUrl: String = "",
    val isLoading: Boolean = false,
    val error: String? = null,
    val offline: Boolean = false,
    val maxDownloads: Int = 0,
    val exhaustedFiles: Int = 0,
)

class TransferDetailViewModel(application: Application) : AndroidViewModel(application) {
    private val app = application as PsstApplication
    private val _uiState = MutableStateFlow(TransferDetailUiState())
    val uiState: StateFlow<TransferDetailUiState> = _uiState.asStateFlow()
    private var loadJob: Job? = null
    private var loadedAccess = app.prefs.historyAccess.value

    init {
        viewModelScope.launch {
            app.prefs.historyAccess.collect { access ->
                if (access != loadedAccess) {
                    loadJob?.cancel()
                    loadedAccess = access
                    _uiState.value =
                        TransferDetailUiState(
                            error =
                                app.getString(
                                    zip.psst.android.R.string
                                        .ui_sign_in_to_the_account_that_created_this_transfer_to_view_its_det
                                )
                        )
                }
            }
        }
    }

    fun stopRefreshing() {
        loadJob?.cancel()
    }

    @OptIn(ExperimentalEncodingApi::class)
    fun load(transferId: String, encryptionKey: String, type: String) {
        loadJob?.cancel()
        val access = app.prefs.historyAccess.value
        loadedAccess = access
        _uiState.value = TransferDetailUiState(isLoading = true)
        loadJob =
            viewModelScope.launch {
                val dao = app.database.transferHistoryDao()
                val row = dao.getById(transferId)
                if (
                    row == null || !access.permits(row) || app.prefs.historyAccess.value != access
                ) {
                    _uiState.value =
                        TransferDetailUiState(
                            error =
                                app.getString(
                                    zip.psst.android.R.string
                                        .ui_this_transfer_is_not_available_to_the_signed_in_account
                                )
                        )
                    return@launch
                }
                val keyBytes =
                    try {
                        zip.psst.android.data.decodeInboxKeyMarker(row.encryptionKey).also {
                            require(it.size == 32)
                        }
                    } catch (_: Exception) {
                        _uiState.value =
                            TransferDetailUiState(
                                transferId = row.id,
                                type = row.type,
                                status = row.status,
                                fileCount = row.fileCount,
                                totalSize = row.totalSize,
                                expiresAt =
                                    row.expiresAt?.let { Instant.ofEpochMilli(it).toString() },
                                error = app.getString(zip.psst.android.R.string.unavailable_key),
                            )
                        return@launch
                    }
                val shareUrl =
                    if (row.type in listOf("receive", "received")) {
                        if (row.encryptionKey.startsWith("v2."))
                            UrlHelper.buildReceiveUrl(row.serverUrl, row.id, keyBytes)
                        else ""
                    } else {
                        UrlHelper.buildDownloadUrl(row.serverUrl, row.id, keyBytes)
                    }
                _uiState.value =
                    TransferDetailUiState(
                        transferId = row.id,
                        type = row.type,
                        status = row.status,
                        fileCount = row.fileCount,
                        totalSize = row.totalSize,
                        expiresAt = row.expiresAt?.let { Instant.ofEpochMilli(it).toString() },
                        shareUrl = shareUrl,
                    )
                while (isActive && app.prefs.historyAccess.value == access) {
                    try {
                        val refreshed =
                            refreshHistoryEntry(
                                dao,
                                row.id,
                                reportFailure = true,
                                createClient = { config ->
                                    zip.psst.shared.api.ApiClient(
                                        config,
                                        sessionToken =
                                            app.prefs.getSessionToken(config.normalizedBaseUrl),
                                    )
                                },
                                onTransfer = { metadata ->
                                    if (app.prefs.historyAccess.value == access)
                                        _uiState.value =
                                            _uiState.value.copy(
                                                maxDownloads = metadata.maxDownloads,
                                                exhaustedFiles =
                                                    metadata.files.count {
                                                        it.remainingDownloads == 0L
                                                    },
                                            )
                                },
                                privateReceiveKey = zip.psst.android.data.InboxKeyStore(app)::read,
                            ) ?: return@launch
                        if (app.prefs.historyAccess.value != access || !access.permits(refreshed))
                            return@launch
                        _uiState.value =
                            _uiState.value.copy(
                                status = refreshed.status,
                                fileCount = refreshed.fileCount,
                                totalSize = refreshed.totalSize,
                                expiresAt =
                                    refreshed.expiresAt?.let {
                                        Instant.ofEpochMilli(it).toString()
                                    },
                                offline = false,
                            )
                    } catch (e: CancellationException) {
                        throw e
                    } catch (_: Exception) {
                        _uiState.value = _uiState.value.copy(offline = true)
                    }
                    delay(if (_uiState.value.offline) 15000 else 5000)
                }
            }
    }
}
