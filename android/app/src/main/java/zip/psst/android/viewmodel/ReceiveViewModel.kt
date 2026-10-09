package zip.psst.android.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
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
import zip.psst.android.PsstApplication
import zip.psst.android.R
import zip.psst.android.data.InboxKeyStore
import zip.psst.android.data.InboxPager
import zip.psst.android.data.InboxReadIdentity
import zip.psst.android.data.ReceivedSnapshot
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.checkpointScope
import zip.psst.android.data.decodeInboxKeyMarker
import zip.psst.android.data.decryptInboxManifest
import zip.psst.android.data.parseHistoryExpiry
import zip.psst.android.data.receiveAndSaveChild
import zip.psst.android.data.receivedSnapshot
import zip.psst.android.data.retrySavedDownloadAcknowledgements
import zip.psst.android.data.selectedLinkLimit
import zip.psst.android.i18n.*
import zip.psst.shared.api.AdminTransferForbiddenException
import zip.psst.shared.api.ApiClient
import zip.psst.shared.api.AuthenticationRequiredException
import zip.psst.shared.api.PasswordChangeRequiredException
import zip.psst.shared.api.SlotEvent
import zip.psst.shared.crypto.AndroidReceiveCrypto
import zip.psst.shared.model.DropSlot
import zip.psst.shared.model.FileMetadata
import zip.psst.shared.model.ServerConfig
import zip.psst.shared.model.UrlHelper

data class ReceiveUiState(
    val page: DropSlot? = null,
    val pager: InboxPager = InboxPager(),
    val isPaging: Boolean = false,
    val shownSaved: Boolean = false,
    val localName: String = "",
    val maxFilesInput: String = "",
    val fileLimitEnabled: Boolean = false,
    val linkPolicyLocked: Boolean = false,
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
    val error: UiText? = null,
    val requiresLogin: Boolean = false,
    val downloadComplete: Boolean = false,
    val connectionError: Boolean = false,
    val savedFileCount: Int = 0,
    val checkpointState: String = "ready",
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
    private var slotAccess: zip.psst.android.data.HistoryAccess? = null
    private var pollJob: Job? = null
    private val historyMutex = Mutex()
    private val refreshMutex = Mutex()
    private var visible = true
    private var downloadJob: Job? = null
    private var createJob: Job? = null
    private var pageJob: Job? = null
    private var frozenPage: DropSlot? = null
    private var frozenPager: InboxPager? = null
    @Volatile private var pageRevision = 0L
    private var activeAccess = app.prefs.historyAccess.value
    private var pendingAccess = activeAccess

    init {
        viewModelScope.launch {
            app.prefs.historyAccess.collect { access ->
                if (activeAccess != access) {
                    activeAccess = access
                    sseJob?.cancel()
                    pollJob?.cancel()
                    downloadJob?.cancel()
                    createJob?.cancel()
                    pageJob?.cancel()
                    pageRevision++
                    frozenPage = null
                    frozenPager = null
                    slotClient?.close()
                    encryptionKeyBytes = null
                    val previous = _uiState.value
                    val recovery = selectionRecovery(previous.requiresLogin, pendingAccess, access)
                    _uiState.value =
                        if (previous.slotId == null && recovery != SelectionRecovery.DISCARD)
                            restoreReceiveDraft(previous, recovery)
                        else
                            ReceiveUiState(
                                error =
                                    message(
                                        zip.psst.android.R.string
                                            .ui_your_account_changed_create_a_new_receive_link_to_continue,
                                    ),
                                requiresLogin = access.accountId == null,
                            )
                }
            }
        }
    }

    @OptIn(ExperimentalEncodingApi::class)
    fun openExisting(id: String) {
        if (_uiState.value.slotId == id && !_uiState.value.requiresLogin) return
        createJob?.cancel()
        sseJob?.cancel()
        pollJob?.cancel()
        downloadJob?.cancel()
        pageJob?.cancel()
        slotClient?.close()
        pageRevision++
        frozenPage = null
        frozenPager = null
        _uiState.value = ReceiveUiState()
        createJob =
            viewModelScope.launch(Dispatchers.IO) {
                val access = app.prefs.historyAccess.value
                val row = app.database.transferHistoryDao().getById(id)
                if (row == null || !access.permits(row)) {
                    _uiState.value =
                        ReceiveUiState(
                            error =
                                message(
                                    zip.psst.android.R.string
                                        .ui_this_receive_link_is_unavailable_to_this_account,
                                ),
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
                    slotAccess = access
                    ensureActive()
                    val restored = restoreReceiveEntry(row, access, privateAvailable)
                    _uiState.value =
                        restored.copy(
                            error =
                                if (restored.keyUnavailable)
                                    message(zip.psst.android.R.string.unavailable_key)
                                else null,
                        )
                    if (visible) listenForEvents(client, id)
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                    _uiState.value =
                        ReceiveUiState(
                            slotId = row.id,
                            slotStatus = row.status,
                            error = message(zip.psst.android.R.string.unavailable_key),
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

    fun continueCheckpointImport() {
        val id = _uiState.value.slotId ?: return
        val client = slotClient ?: return
        val access = app.prefs.historyAccess.value
        val revision = pageRevision
        fun current() =
            app.prefs.historyAccess.value == access &&
                _uiState.value.slotId == id &&
                slotClient === client &&
                pageRevision == revision
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val dao = app.database.transferHistoryDao()
                val row = dao.getById(id) ?: return@launch
                if (!current() || access != slotAccess || !access.permits(row)) return@launch
                if (_uiState.value.checkpointState == "recovery")
                    dao.retryCheckpointMigration(id, row.checkpointScope())
                if (current()) refreshSlot(client, id)
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                if (current())
                    _uiState.update {
                        it.copy(
                            error =
                                message(
                                    R.string
                                        .l_saved_checkpoints_could_not_be_imported_original_records_and_save_a50cc8,
                                ),
                        )
                    }
            }
        }
    }

    fun reconnect() = setVisible(true)

    fun renameLocal(value: String) {
        _uiState.update { it.copy(localName = value) }
    }

    fun renameShared(draft: String) {
        val access = app.prefs.historyAccess.value
        val id = _uiState.value.slotId ?: return
        viewModelScope.launch(Dispatchers.IO) {
            val row = app.database.transferHistoryDao().getById(id) ?: return@launch
            if (!access.permits(row) || row.accountId == null) return@launch
            val client =
                ApiClient(
                    ServerConfig(row.serverUrl),
                    sessionToken = app.prefs.getSessionToken(row.serverUrl),
                )
            try {
                val result =
                    client.slots.renameTitle(id, zip.psst.shared.model.LinkTitle.normalize(draft))
                if (app.prefs.historyAccess.value != access || _uiState.value.slotId != id)
                    return@launch
                app.database
                    .transferHistoryDao()
                    .saveSharedTitle(id, row.accountId, row.originScope, result.title)
                _uiState.update { it.copy(localName = result.title.orEmpty()) }
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                _uiState.update {
                    it.copy(
                        error =
                            failureText(error)
                                ?: message(R.string.l_could_not_rename_this_link_retry_229f97),
                    )
                }
            } finally {
                client.close()
            }
        }
    }

    fun createAnother() {
        if (_uiState.value.isDownloading || _uiState.value.isCreatingSlot) return
        createJob?.cancel()
        sseJob?.cancel()
        pollJob?.cancel()
        pageJob?.cancel()
        slotClient?.close()
        slotClient = null
        slotAccess = null
        pageRevision++
        frozenPage = null
        frozenPager = null
        encryptionKeyBytes = null
        _uiState.value = ReceiveUiState()
    }

    fun setMaxFiles(value: String) {
        if (
            _uiState.value.slotId == null &&
                !_uiState.value.isCreatingSlot &&
                !_uiState.value.linkPolicyLocked
        )
            _uiState.update { it.copy(maxFilesInput = value.take(11), error = null) }
    }

    fun setFileLimitEnabled(enabled: Boolean) {
        if (
            _uiState.value.slotId == null &&
                !_uiState.value.isCreatingSlot &&
                !_uiState.value.linkPolicyLocked
        )
            _uiState.update { it.copy(fileLimitEnabled = enabled, error = null) }
    }

    @OptIn(ExperimentalEncodingApi::class)
    fun createSlot() {
        if (
            createJob?.isActive == true ||
                _uiState.value.isCreatingSlot ||
                _uiState.value.slotId != null
        )
            return
        val limit =
            try {
                selectedLinkLimit(_uiState.value.fileLimitEnabled, _uiState.value.maxFilesInput)
            } catch (e: IllegalArgumentException) {
                _uiState.update { it.copy(error = failureText(e)) }
                return
            }
        val serverUrl = app.prefs.getServerUrl()
        if (serverUrl.isBlank()) {
            _uiState.update {
                it.copy(error = message(zip.psst.android.R.string.ui_server_url_not_configured))
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
                        message(
                            zip.psst.android.R.string
                                .ui_sign_in_under_server_configuration_to_create_receive_links,
                        ),
                )
            }
            return
        }

        val localName = _uiState.value.localName.trim().ifEmpty { null }
        sseJob?.cancel()
        pollJob?.cancel()
        slotClient?.close()
        _uiState.update { it.copy(isCreatingSlot = true, error = null, maxFiles = limit) }

        val access = app.prefs.historyAccess.value
        pendingAccess = access
        createJob =
            viewModelScope.launch(Dispatchers.IO) {
                var allocation: TransferHistoryEntity? = null
                var retained = false
                try {
                    val client = ApiClient(ServerConfig(serverUrl), sessionToken = sessionToken)
                    slotClient = client
                    slotAccess = access
                    val pair = AndroidReceiveCrypto.generateKeyPair()
                    val key = pair.publicKey
                    val publicKey =
                        Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT).encode(key)
                    val slot =
                        client.slots.create(
                            publicKey,
                            limit,
                            zip.psst.shared.model.LinkTitle.normalize(localName),
                        )
                    if (app.prefs.historyAccess.value == access)
                        _uiState.update { it.copy(linkPolicyLocked = true) }
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
                            sharedTitle = slot.title,
                            maxFiles = limit,
                            reservedFiles = 0,
                        )
                    withContext(NonCancellable) {
                        app.database.transferHistoryDao().insert(requireNotNull(allocation))
                    }
                    zip.psst.android.data.HistoryNotifications.changed(serverUrl, accountId)
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
                                    sharedTitle = slot.title,
                                    maxFiles = limit,
                                    reservedFiles = 0,
                                ),
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
                                    failureText(e)
                                else
                                    message(
                                        zip.psst.android.R.string
                                            .ui_could_not_create_a_receive_link_check_your_connection_and_retry,
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

    fun firstPage() = navigatePage(InboxPager())

    fun previousPage() {
        val pager = _uiState.value.pager
        if (pager.previous.isNotEmpty()) navigatePage(pager.back())
    }

    fun nextPage() {
        val state = _uiState.value
        val next = state.page?.nextCursor ?: return
        try {
            navigatePage(state.pager.next(next))
        } catch (e: IllegalArgumentException) {
            _uiState.update { it.copy(error = failureText(e)) }
        }
    }

    private fun navigatePage(target: InboxPager) {
        val state = _uiState.value
        val id = state.slotId ?: return
        val client = slotClient ?: return
        if (state.isDownloading || state.downloadConsent != null || state.isPaging) return
        pageRevision++
        _uiState.update { it.copy(isPaging = true, error = null) }
        pageJob = viewModelScope.launch(Dispatchers.IO) { refreshSlot(client, id, target, true) }
    }

    private suspend fun refreshSlot(
        client: ApiClient,
        slotId: String,
        target: InboxPager = _uiState.value.pager,
        navigation: Boolean = false,
    ) {
        val initial = _uiState.value
        if (
            initial.isDownloading ||
                initial.downloadConsent != null ||
                (!navigation && initial.isPaging)
        )
            return
        val request = InboxReadIdentity(app.prefs.historyAccess.value, slotId, initial.pager)
        if (slotAccess != request.access || client !== slotClient) return
        val revision = pageRevision
        fun current(): Boolean =
            pageRevision == revision &&
                request.accepts(
                    app.prefs.historyAccess.value,
                    _uiState.value.slotId,
                    _uiState.value.pager,
                )
        if (navigation) refreshMutex.lock() else if (!refreshMutex.tryLock()) return
        try {
            if (!current()) return
            val visibleRow = app.database.transferHistoryDao().getById(slotId) ?: return
            if (!request.access.permits(visibleRow)) return
            val slot = client.slots.getPage(slotId, target.cursor, 50)
            if (
                !current() || _uiState.value.isDownloading || _uiState.value.downloadConsent != null
            )
                return
            uiRequire(slot.nextCursor == null || slot.nextCursor !in target.previous) {
                message(
                    R.string.l_the_server_repeated_an_inbox_page_return_to_the_first_page_6ce5f7,
                )
            }
            val snapshot = slot.receivedSnapshot()
            historyMutex.withLock {
                if (!current()) return@withLock
                val dao = app.database.transferHistoryDao()
                val row =
                    dao.mergeReceived(
                        slotId,
                        snapshot,
                        expectedScope = visibleRow.checkpointScope(),
                    )
                if (row != null) dao.update(row.copy(sharedTitle = slot.title))
                val savedChildren =
                    row?.let {
                            dao.savedChildren(
                                it,
                                slot.completedTransfers.map { child -> child.transferId },
                            )
                        }
                        .orEmpty()
                if (row != null && current()) {
                    _uiState.update {
                        it.copy(
                            page = slot,
                            localName = slot.title ?: row.title.orEmpty(),
                            pager = target,
                            isPaging = false,
                            connectionError = false,
                            error = it.storagePermissionErrorAfterRefresh(),
                            slotStatus = row.status,
                            downloadComplete = false,
                            shownSaved =
                                slot.completedTransfers.isNotEmpty() &&
                                    slot.completedTransfers.all { child ->
                                        child.transferId in savedChildren
                                    },
                            savedFileCount =
                                row.checkpointSavedFiles
                                    .coerceAtMost(Int.MAX_VALUE.toLong())
                                    .toInt(),
                            checkpointState = row.checkpointState,
                            maxFiles = slot.maxFiles,
                            remainingFiles = slot.remainingFiles,
                            reservedFiles = slot.reservedFiles,
                        )
                    }
                }
            }
            if (pageRevision == revision && app.prefs.historyAccess.value == request.access)
                retrySavedDownloadAcknowledgements(
                    app.database
                        .transferHistoryDao()
                        .savedChildren(
                            requireNotNull(app.database.transferHistoryDao().getById(slotId)),
                            slot.completedTransfers.map { it.transferId },
                        ),
                    client,
                )
        } catch (e: Exception) {
            if (e is CancellationException) throw e
            if (!current()) return
            if (e is AuthenticationRequiredException) {
                sseJob?.cancel()
                pollJob?.cancel()
                _uiState.update {
                    it.copy(requiresLogin = true, error = failureText(e), connectionError = false)
                }
            } else if (
                e is zip.psst.shared.api.ResourceRevokedException ||
                    (e is io.ktor.client.plugins.ClientRequestException &&
                        e.response.status.value in listOf(404, 410))
            ) {
                app.database.transferHistoryDao().updateStatus(slotId, "unavailable")
                sseJob?.cancel()
                pollJob?.cancel()
                _uiState.update { it.copy(slotStatus = "unavailable", connectionError = false) }
            } else _uiState.update { it.copy(connectionError = true) }
        } finally {
            refreshMutex.unlock()
            if (current()) _uiState.update { it.copy(isPaging = false) }
        }
    }

    fun downloadReceivedFiles() = downloadReceivedFiles(null)

    fun storagePermissionDenied() {
        _uiState.update { it.storagePermissionDenied() }
    }

    fun confirmDownload() {
        val approved = _uiState.value.downloadConsent ?: return
        downloadReceivedFiles(approved)
    }

    fun cancelDownload() {
        downloadJob?.cancel()
        frozenPage = null
        frozenPager = null
        _uiState.update { it.copy(downloadConsent = null) }
    }

    private fun downloadReceivedFiles(approved: zip.psst.android.data.InboxDownloadConsent?) {
        val slotId = _uiState.value.slotId ?: return
        if (encryptionKeyBytes == null || _uiState.value.keyUnavailable) return
        if (_uiState.value.isDownloading || _uiState.value.isPaging) return
        val shown = if (approved != null) frozenPage else _uiState.value.page
        val selectedPage = shown ?: return
        val selectedPager = if (approved != null) frozenPager ?: return else _uiState.value.pager
        if (selectedPager != _uiState.value.pager) return
        frozenPage = selectedPage
        frozenPager = selectedPager
        val request = InboxReadIdentity(app.prefs.historyAccess.value, slotId, selectedPager)
        val token = app.prefs.getSessionToken(request.access.serverUrl) ?: return
        fun checkCurrent() {
            if (
                !request.accepts(
                    app.prefs.historyAccess.value,
                    _uiState.value.slotId,
                    _uiState.value.pager,
                ) || app.prefs.getSessionToken(request.access.serverUrl) != token
            )
                throw CancellationException("Your account or inbox page changed")
        }

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
                val access = request.access
                val dao = app.database.transferHistoryDao()
                val row = dao.getById(slotId)
                if (row == null || !access.permits(row)) {
                    _uiState.update {
                        it.copy(
                            isDownloading = false,
                            error =
                                message(
                                    zip.psst.android.R.string.ui_receive_history_entry_is_missing,
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
                    checkCurrent()
                    val slot = client.slots.getPage(slotId, selectedPager.cursor, 50)
                    checkCurrent()
                    val selected = zip.psst.android.data.shownInboxTransfers(selectedPage, slot)
                    uiRequire(
                        if (row.encryptionKey.startsWith("v2."))
                            slot.receiveProtocol == 2 &&
                                slot.recipientPublicKey == row.encryptionKey.removePrefix("v2.")
                        else slot.receiveProtocol == 1,
                    ) {
                        message(R.string.l_the_inbox_receive_key_does_not_match_this_device_0a2771)
                    }
                    val latest =
                        dao.mergeReceived(
                            slotId,
                            slot.receivedSnapshot(),
                            expectedScope = row.checkpointScope(),
                        ) ?: row
                    uiRequire(latest.checkpointState == "ready") {
                        message(
                            R.string
                                .l_saved_checkpoints_are_still_being_imported_continue_local_checkpo_921dc6,
                        )
                    }
                    val savedChildren = dao.savedChildren(latest, selected.map { it.transferId })
                    checkCurrent()
                    val transfers = selected.filter { it.transferId !in savedChildren }
                    uiRequire(transfers.isNotEmpty()) { message(R.string.ui_no_completed_uploads) }
                    val received = mutableListOf<Pair<String, FileMetadata>>()
                    val childKeys = mutableMapOf<String, ByteArray>()
                    val fingerprints = mutableMapOf<String, String>()
                    val privateKey = inboxKeys.read(row)
                    for (transfer in transfers) {
                        checkCurrent()
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
                        uiRequire(manifest.files.size == transfer.fileCount) {
                            message(R.string.l_manifest_file_count_mismatch_634a32)
                        }
                        fingerprints[transfer.transferId] =
                            java.security.MessageDigest.getInstance("SHA-256")
                                .digest(manifestBytes)
                                .joinToString("") { "%02x".format(it) }
                        childKeys[transfer.transferId] = decoded.key
                        received += manifest.files.map { transfer.transferId to it }
                    }
                    ensureActive()
                    checkCurrent()
                    _uiState.update { it.copy(receivedFiles = received.map { it.second }) }

                    val context = getApplication<PsstApplication>()
                    val totalFiles = received.size
                    uiRequire(totalFiles == transfers.sumOf { it.fileCount }) {
                        message(R.string.l_manifest_file_count_mismatch_634a32)
                    }
                    val savedIds = dao.savedFiles(latest, transfers.map { it.transferId })
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
                    checkCurrent()
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
                        checkCurrent()
                        var savedOutput: zip.psst.android.data.SavedGuestFile? = null
                        receiveAndSaveChild(
                            client = client,
                            transferId = transfer.transferId,
                            files =
                                received
                                    .filter { it.first == transfer.transferId }
                                    .map { it.second },
                            key = requireNotNull(childKeys[transfer.transferId]),
                            saveFile = { fileMeta, plaintext ->
                                checkCurrent()
                                saver.requireSpace(remainingBytes)
                                savedOutput = saver.save(fileMeta, plaintext) {}
                            },
                            recordSaved = { child ->
                                checkCurrent()
                                historyMutex.withLock {
                                    dao.mergeReceived(
                                        slotId,
                                        ReceivedSnapshot(
                                            mapOf(transfer.transferId to child),
                                            partial = true,
                                        ),
                                        saved = true,
                                        expectedScope = row.checkpointScope(),
                                    )
                                }
                            },
                            alreadySaved =
                                dao.savedFiles(latest, listOf(transfer.transferId))
                                    .map { it.substringAfter('/') }
                                    .toSet(),
                            recordFileSaved = { blobId ->
                                dao.recordSavedFile(
                                    slotId,
                                    "${transfer.transferId}/$blobId",
                                    row.checkpointScope(),
                                    savedOutput?.uri,
                                )
                                savedOutput = null
                                remainingBytes -=
                                    received
                                        .first {
                                            it.first == transfer.transferId &&
                                                it.second.blobId == blobId
                                        }
                                        .second
                                        .size
                                val savedCount = dao.getById(slotId)?.checkpointSavedFiles ?: 0
                                if (app.prefs.historyAccess.value == access)
                                    _uiState.update {
                                        it.copy(
                                            savedFileCount =
                                                savedCount
                                                    .coerceAtMost(Int.MAX_VALUE.toLong())
                                                    .toInt(),
                                        )
                                    }
                            },
                            onFileSaved = {
                                checkCurrent()
                                savedFiles++
                                _uiState.update {
                                    it.copy(
                                        downloadProgress =
                                            savedFiles.toFloat() / totalFiles.toFloat(),
                                    )
                                }
                            },
                        )
                    }

                    historyMutex.withLock {
                        checkCurrent()
                        val updated = dao.getById(slotId)
                        val savedChildren =
                            updated
                                ?.let {
                                    dao.savedChildren(
                                        it,
                                        selectedPage.completedTransfers.map { child ->
                                            child.transferId
                                        },
                                    )
                                }
                                .orEmpty()
                        checkCurrent()
                        _uiState.update {
                            if (it.slotId == slotId)
                                it.copy(
                                    isDownloading = false,
                                    downloadProgress = 1f,
                                    slotStatus = updated?.status ?: "has_uploads",
                                    downloadComplete = false,
                                    shownSaved =
                                        selectedPage.completedTransfers.all { child ->
                                            child.transferId in savedChildren
                                        },
                                )
                            else it
                        }
                    }
                } catch (e: Exception) {
                    if (e is kotlinx.coroutines.CancellationException) throw e
                    checkCurrent()
                    val trafficError =
                        zip.psst.android.data.classifyTrafficFailure(e) {
                            client.slots.trafficStatus(slotId, "download")
                        }
                    checkCurrent()
                    _uiState.update {
                        it.copy(
                            isDownloading = false,
                            requiresLogin = e is AuthenticationRequiredException,
                            error =
                                trafficError?.let(::failureText)
                                    ?: if (
                                        e is
                                            zip.psst.android.data.InsufficientDownloadSpaceException ||
                                            e is zip.psst.shared.api.TransferPolicyException ||
                                            e is AuthenticationRequiredException
                                    )
                                        failureText(e)
                                    else
                                        message(
                                            zip.psst.android.R.string
                                                .ui_could_not_save_every_file_retry_saving_files_already_saved_will_b,
                                        ),
                        )
                    }
                } finally {
                    client.close()
                    _uiState.update {
                        if (request.accepts(app.prefs.historyAccess.value, it.slotId, it.pager))
                            it.copy(isDownloading = false)
                        else it
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
        pageJob?.cancel()
    }
}

/** New-inbox setup follows the same account-bound recovery rules as the send draft. */
internal fun restoreReceiveDraft(
    previous: ReceiveUiState,
    recovery: SelectionRecovery,
): ReceiveUiState =
    if (recovery == SelectionRecovery.DISCARD) ReceiveUiState()
    else
        ReceiveUiState(
            localName = previous.localName,
            maxFilesInput = previous.maxFilesInput,
            fileLimitEnabled = previous.fileLimitEnabled,
            linkPolicyLocked = previous.linkPolicyLocked,
            requiresLogin = recovery == SelectionRecovery.RESTRICTED,
        )
