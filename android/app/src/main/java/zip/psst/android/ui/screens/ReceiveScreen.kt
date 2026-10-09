package zip.psst.android.ui.screens

import android.Manifest
import android.app.DownloadManager
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.android.ui.components.AccountIndicator
import zip.psst.android.ui.components.LinkLimits
import zip.psst.android.ui.components.LinkPanel
import zip.psst.android.viewmodel.ReceiveRetry
import zip.psst.android.viewmodel.ReceiveViewModel
import zip.psst.android.viewmodel.retryAction
import zip.psst.shared.model.TransferStatus

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ReceiveScreen(
    onSlotCreated: (String, String) -> Unit,
    onBack: () -> Unit,
    onSignIn: () -> Unit,
    existingId: String? = null,
    viewModel: ReceiveViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current
    var pendingStorageSlot by rememberSaveable { mutableStateOf<String?>(null) }
    var pendingStorageConfirmation by rememberSaveable { mutableStateOf(false) }
    val storage =
        rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
            val sameSlot = pendingStorageSlot != null && state.slotId == pendingStorageSlot
            pendingStorageSlot = null
            if (sameSlot) {
                if (!granted) viewModel.storagePermissionDenied()
                else if (pendingStorageConfirmation) viewModel.confirmDownload()
                else viewModel.downloadReceivedFiles()
            }
        }
    fun saveAllowed(confirm: Boolean = false) {
        if (
            Build.VERSION.SDK_INT <= 28 &&
                ContextCompat.checkSelfPermission(
                    context,
                    Manifest.permission.WRITE_EXTERNAL_STORAGE,
                ) != PackageManager.PERMISSION_GRANTED
        ) {
            pendingStorageSlot = state.slotId
            pendingStorageConfirmation = confirm
            storage.launch(Manifest.permission.WRITE_EXTERNAL_STORAGE)
        } else if (confirm) viewModel.confirmDownload() else viewModel.downloadReceivedFiles()
    }
    var freshCreation by rememberSaveable(existingId) { mutableStateOf(false) }
    val activeExistingId = existingId.takeUnless { freshCreation }
    var showQR by rememberSaveable(state.slotId) { mutableStateOf(false) }
    var renaming by rememberSaveable(state.slotId) { mutableStateOf(false) }
    var renameDraft by rememberSaveable(state.slotId) { mutableStateOf("") }
    if (renaming)
        AlertDialog(
            onDismissRequest = { renaming = false },
            title = { Text(tr(R.string.l_rename_link_8e2e05)) },
            text = {
                OutlinedTextField(
                    value = renameDraft,
                    onValueChange = { renameDraft = it },
                    label = { Text(tr(R.string.l_shared_title_6bac93)) },
                    supportingText = {
                        Text(tr(R.string.l_shown_to_people_using_this_link_1c969c))
                    },
                    singleLine = true,
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        viewModel.renameShared(renameDraft)
                        renaming = false
                    },
                ) {
                    Text(tr(R.string.l_save_efc007))
                }
            },
            dismissButton = {
                TextButton(onClick = { renaming = false }) { Text(tr(R.string.l_cancel_77dfd2)) }
            },
        )
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(existingId) { if (existingId != null) viewModel.openExisting(existingId) }
    DisposableEffect(lifecycle, viewModel) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) viewModel.setVisible(true)
            if (
                event == Lifecycle.Event.ON_STOP &&
                    (context as? android.app.Activity)?.isChangingConfigurations != true
            )
                viewModel.setVisible(false)
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
            if ((context as? android.app.Activity)?.isChangingConfigurations != true)
                viewModel.setVisible(false)
        }
    }
    state.downloadConsent?.let { consent ->
        AlertDialog(
            onDismissRequest = viewModel::cancelDownload,
            title = { Text(tr(R.string.l_download_these_files_f0f150)) },
            text = {
                Text(
                    plural(
                        R.plurals.download_large_consent,
                        consent.fileCount.toLong(),
                        consent.fileCount,
                        UiFormatting.bytes(
                            consent.remainingBytes,
                            UiStrings.context().resources.configuration.locales[0],
                        ),
                    ),
                )
            },
            confirmButton = {
                TextButton(onClick = { saveAllowed(confirm = true) }) {
                    Text(tr(R.string.l_download_and_save_392089))
                }
            },
            dismissButton = {
                TextButton(onClick = viewModel::cancelDownload) {
                    Text(tr(R.string.l_cancel_77dfd2))
                }
            },
        )
    }
    val hasUploads = state.page?.completedTransfers?.isNotEmpty() == true
    val canSave =
        hasUploads &&
            !state.shownSaved &&
            state.checkpointState == "ready" &&
            !state.keyUnavailable &&
            state.slotStatus != "unavailable" &&
            state.error == null
    Scaffold(
        bottomBar = {
            if (state.isDownloading || canSave || (hasUploads && state.shownSaved))
                Surface(tonalElevation = 3.dp) {
                    Column(
                        Modifier.fillMaxWidth().navigationBarsPadding().padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        if (state.isDownloading) {
                            LinearProgressIndicator(
                                progress = { state.downloadProgress },
                                modifier = Modifier.fillMaxWidth(),
                            )
                            OutlinedButton(
                                onClick = viewModel::cancelDownload,
                                modifier = Modifier.fillMaxWidth(),
                            ) {
                                Text(tr(R.string.l_cancel_saving_2f4c09))
                            }
                        } else if (canSave)
                            Button(
                                onClick = { saveAllowed() },
                                enabled = !state.isPaging && state.downloadConsent == null,
                                modifier = Modifier.fillMaxWidth(),
                            ) {
                                Text(tr(R.string.l_save_shown_uploads_62e075))
                            }
                        else
                            OutlinedButton(
                                onClick = {
                                    runCatching {
                                        context.startActivity(
                                            Intent(DownloadManager.ACTION_VIEW_DOWNLOADS),
                                        )
                                    }
                                },
                                modifier = Modifier.fillMaxWidth(),
                            ) {
                                Text(stringResource(R.string.open_downloads))
                            }
                    }
                }
        },
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.receive_link)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.back))
                    }
                },
            )
        },
    ) { padding ->
        Column(
            Modifier.fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            AccountIndicator()
            if (state.slotId == null && activeExistingId == null && !state.isCreatingSlot) {
                Text(
                    tr(
                        R.string
                            .l_create_a_private_inbox_link_for_others_to_send_files_to_you_3a7e52,
                    ),
                )
                OutlinedTextField(
                    value = state.localName,
                    onValueChange = viewModel::renameLocal,
                    label = { Text(tr(R.string.l_name_optional_9c9f03)) },
                    supportingText = {
                        Text(tr(R.string.l_shown_to_people_using_this_link_1c969c))
                    },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                LinkLimits(
                    enabled = state.fileLimitEnabled,
                    value = state.maxFilesInput,
                    editable = !state.linkPolicyLocked,
                    label = tr(R.string.l_maximum_files_accepted_54d449),
                    help =
                        tr(
                            R.string
                                .l_incomplete_uploads_use_an_allowance_too_deleting_files_does_not_r_cdd215,
                        ),
                    onEnabledChange = viewModel::setFileLimitEnabled,
                    onValueChange = viewModel::setMaxFiles,
                )
                Button(
                    onClick = viewModel::createSlot,
                    enabled =
                        zip.psst.android.data.linkLimitError(
                            state.fileLimitEnabled,
                            state.maxFilesInput,
                        ) == null,
                ) {
                    Text(tr(R.string.l_create_receive_link_b664da))
                }
            }
            if (state.slotId != null) {
                Text(
                    state.localName.ifBlank { tr(R.string.l_receive_link_ef4dc0) },
                    style = MaterialTheme.typography.headlineSmall,
                )
                Row {
                    TextButton(
                        onClick = {
                            renameDraft = state.localName
                            renaming = true
                        },
                    ) {
                        Text(tr(R.string.l_rename_d3f4cb))
                    }
                    TextButton(
                        onClick = {
                            freshCreation = true
                            viewModel.createAnother()
                        },
                        enabled = !state.isDownloading && !state.isPaging,
                    ) {
                        Text(tr(R.string.l_create_another_link_ba0703))
                    }
                }
            }
            if (state.legacyReadOnly)
                Text(
                    tr(
                        R.string
                            .l_this_older_inbox_is_read_only_save_its_existing_files_and_create__b83329,
                    ),
                )
            if (state.slotId != null && state.maxFiles > 0)
                Text(
                    state.remainingFiles?.let { plural(R.plurals.files_remaining, it, it) }
                        ?: tr(R.string.ui_unknown_files_remaining),
                )
            if (state.slotId != null || state.isCreatingSlot)
                Text(
                    stringResource(
                        when {
                            state.slotStatus == "unavailable" -> R.string.link_unavailable
                            state.isCreatingSlot -> R.string.creating_link
                            state.isDownloading -> R.string.saving_files
                            state.downloadComplete -> R.string.saved_downloads
                            state.slotStatus == "has_uploads" -> R.string.files_received
                            else -> R.string.waiting_files
                        },
                    ),
                    Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                    style = MaterialTheme.typography.titleMedium,
                )
            if (state.isCreatingSlot) CircularProgressIndicator()
            val arrivals =
                state.page?.summary?.completedFiles?.let { it > 0 } == true ||
                    state.page?.transfers?.isNotEmpty() == true
            if (state.uploadUrl != null && state.slotStatus != "unavailable") {
                if (!arrivals || showQR) LinkPanel(state.uploadUrl!!)
                if (arrivals)
                    TextButton(onClick = { showQR = !showQR }) {
                        Text(
                            if (showQR) tr(R.string.l_hide_qr_b7a2f4)
                            else tr(R.string.l_show_qr_share_link_e63fb8),
                        )
                    }
            }
            state.page?.let { page ->
                val summary = page.summary
                Text(
                    if (summary?.ready == true)
                        pluralStringResource(
                            R.plurals.received_file_count,
                            if (summary.completedFiles == 1L) 1 else 2,
                            summary.completedFiles ?: 0L,
                        )
                    else tr(R.string.l_received_files_c558af),
                    style = MaterialTheme.typography.titleMedium,
                    modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                )
                TextButton(
                    onClick = viewModel::reconnect,
                    enabled = !state.isPaging && !state.isDownloading,
                ) {
                    Text(tr(R.string.l_refresh_56e3ba))
                }
                if (page.transfers.isEmpty())
                    Text(tr(R.string.l_no_available_uploads_on_this_page_92661b))
                page.transfers.forEach { child ->
                    val status =
                        when (child.status) {
                            TransferStatus.COMPLETE -> tr(R.string.l_ready_to_save_275fae)
                            TransferStatus.PENDING -> tr(R.string.l_upload_in_progress_dbc05c)
                            TransferStatus.EXPIRED -> tr(R.string.l_expired_a689a9)
                            TransferStatus.EXHAUSTED -> tr(R.string.l_download_limit_reached_8745b6)
                        }
                    Text(
                        pluralStringResource(
                            R.plurals.file_count_label,
                            child.fileCount,
                            child.fileCount,
                        ) + " · " + status,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                if (state.isPaging) LinearProgressIndicator(Modifier.fillMaxWidth())
                if (state.shownSaved) Text(tr(R.string.l_shown_uploads_saved_on_this_device_375637))
            }
            if (state.connectionError) {
                Text(
                    stringResource(R.string.offline_retained),
                    style = MaterialTheme.typography.bodySmall,
                )
                TextButton(onClick = viewModel::reconnect) {
                    Text(stringResource(R.string.reconnect))
                }
            }
            if (state.error != null) {
                Text(
                    state.error!!.text(),
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                )
                if (!state.keyUnavailable)
                    Button(
                        onClick = {
                            when (state.retryAction(activeExistingId)) {
                                ReceiveRetry.SIGN_IN -> onSignIn()
                                ReceiveRetry.SAVE -> saveAllowed()
                                ReceiveRetry.REOPEN -> viewModel.openExisting(activeExistingId!!)
                                ReceiveRetry.CREATE -> viewModel.createSlot()
                            }
                        },
                    ) {
                        Text(
                            stringResource(
                                if (state.requiresLogin) R.string.sign_in
                                else if (state.slotId != null) R.string.retry_saving
                                else R.string.retry,
                            ),
                        )
                    }
            }
            if (state.checkpointState != "ready") {
                Text(
                    if (state.checkpointState == "recovery")
                        tr(
                            R.string
                                .l_an_older_saved_checkpoint_contains_invalid_or_unsupported_data_or_084a28,
                        )
                    else
                        tr(
                            R.string
                                .l_importing_saved_file_checkpoints_saved_files_remain_on_this_devic_3acb84,
                        ),
                )
                TextButton(
                    onClick = viewModel::continueCheckpointImport,
                    enabled = !state.isPaging,
                ) {
                    Text(
                        if (state.checkpointState == "recovery")
                            tr(R.string.l_retry_checkpoint_import_78098d)
                        else tr(R.string.l_continue_checkpoint_import_291d65),
                    )
                }
                TextButton(
                    onClick = {
                        runCatching {
                            context.startActivity(Intent(DownloadManager.ACTION_VIEW_DOWNLOADS))
                        }
                    },
                ) {
                    Text(stringResource(R.string.open_downloads))
                }
            }
            if (state.downloadComplete || state.savedFileCount > 0) {
                Text(
                    plural(
                        R.plurals.files_saved_local,
                        state.savedFileCount.toLong(),
                        state.savedFileCount,
                    ),
                )
                OutlinedButton(
                    onClick = {
                        runCatching {
                            context.startActivity(Intent(DownloadManager.ACTION_VIEW_DOWNLOADS))
                        }
                    },
                ) {
                    Text(stringResource(R.string.open_downloads))
                }
            }
            state.page?.let { page ->
                val canNavigate =
                    !state.isPaging && !state.isDownloading && state.downloadConsent == null
                if (state.pager.previous.isNotEmpty() || page.nextCursor != null)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        TextButton(
                            onClick = viewModel::firstPage,
                            enabled = canNavigate && state.pager.cursor != null,
                        ) {
                            Text(tr(R.string.l_first_916a78))
                        }
                        TextButton(
                            onClick = viewModel::previousPage,
                            enabled = canNavigate && state.pager.previous.isNotEmpty(),
                        ) {
                            Text(tr(R.string.l_previous_50f942))
                        }
                        TextButton(
                            onClick = viewModel::nextPage,
                            enabled = canNavigate && page.nextCursor != null,
                        ) {
                            Text(tr(R.string.l_next_bc9819))
                        }
                    }
            }
        }
    }
}
