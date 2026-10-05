package zip.psst.android.ui.screens

import android.app.DownloadManager
import android.content.Intent
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.R
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
    var freshCreation by remember(existingId) { mutableStateOf(false) }
    val activeExistingId = existingId.takeUnless { freshCreation }
    var showQR by remember(state.slotId) { mutableStateOf(false) }
    var renaming by remember(state.slotId) { mutableStateOf(false) }
    var renameDraft by remember(state.slotId) { mutableStateOf("") }
    if (renaming)
        AlertDialog(
            onDismissRequest = { renaming = false },
            title = { Text("Rename link") },
            text = {
                OutlinedTextField(
                    value = renameDraft,
                    onValueChange = { renameDraft = it },
                    label = { Text("Shared title") },
                    supportingText = { Text("Shown to people using this link") },
                    singleLine = true,
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        viewModel.renameShared(renameDraft)
                        renaming = false
                    }
                ) {
                    Text("Save")
                }
            },
            dismissButton = { TextButton(onClick = { renaming = false }) { Text("Cancel") } },
        )
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(existingId) { if (existingId != null) viewModel.openExisting(existingId) }
    DisposableEffect(lifecycle, viewModel) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) viewModel.setVisible(true)
            if (event == Lifecycle.Event.ON_STOP) viewModel.setVisible(false)
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
            viewModel.setVisible(false)
        }
    }
    state.downloadConsent?.let { consent ->
        AlertDialog(
            onDismissRequest = viewModel::cancelDownload,
            title = { Text("Download these files?") },
            text = {
                Text(
                    "${consent.fileCount} files · ${android.text.format.Formatter.formatFileSize(context, consent.remainingBytes)}. This download is larger than 100 MiB. Keep the app open while saving."
                )
            },
            confirmButton = {
                TextButton(onClick = viewModel::confirmDownload) { Text("Download and save") }
            },
            dismissButton = { TextButton(onClick = viewModel::cancelDownload) { Text("Cancel") } },
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
                                Text("Cancel saving")
                            }
                        } else if (canSave)
                            Button(
                                onClick = viewModel::downloadReceivedFiles,
                                enabled = !state.isPaging && state.downloadConsent == null,
                                modifier = Modifier.fillMaxWidth(),
                            ) {
                                Text("Save shown uploads")
                            }
                        else
                            OutlinedButton(
                                onClick = {
                                    runCatching {
                                        context.startActivity(
                                            Intent(DownloadManager.ACTION_VIEW_DOWNLOADS)
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
                Text("Create a private inbox link for others to send files to you.")
                OutlinedTextField(
                    value = state.localName,
                    onValueChange = viewModel::renameLocal,
                    label = { Text("Name (optional)") },
                    supportingText = { Text("Shown to people using this link") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                LinkLimits(
                    enabled = state.fileLimitEnabled,
                    value = state.maxFilesInput,
                    editable = !state.linkPolicyLocked,
                    label = "Maximum files accepted",
                    help =
                        "Incomplete uploads use an allowance too; deleting files does not restore it. Existing server restrictions still apply.",
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
                    Text("Create receive link")
                }
            }
            if (state.slotId != null) {
                Text(
                    state.localName.ifBlank { "Receive link" },
                    style = MaterialTheme.typography.headlineSmall,
                )
                Row {
                    TextButton(
                        onClick = {
                            renameDraft = state.localName
                            renaming = true
                        }
                    ) {
                        Text("Rename")
                    }
                    TextButton(
                        onClick = {
                            freshCreation = true
                            viewModel.createAnother()
                        },
                        enabled = !state.isDownloading && !state.isPaging,
                    ) {
                        Text("Create another link")
                    }
                }
            }
            if (state.legacyReadOnly)
                Text(
                    "This older inbox is read-only. Save its existing files and create a new receive link for further uploads."
                )
            if (state.slotId != null && state.maxFiles > 0)
                Text("${state.remainingFiles ?: "…"} files remaining")
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
                        }
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
                        Text(if (showQR) "Hide QR" else "Show QR / Share link")
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
                    else "Received files",
                    style = MaterialTheme.typography.titleMedium,
                    modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                )
                TextButton(
                    onClick = viewModel::reconnect,
                    enabled = !state.isPaging && !state.isDownloading,
                ) {
                    Text("Refresh")
                }
                if (page.transfers.isEmpty()) Text("No available uploads on this page.")
                page.transfers.forEach { child ->
                    val status =
                        when (child.status) {
                            TransferStatus.COMPLETE -> "Ready to save"
                            TransferStatus.PENDING -> "Upload in progress"
                            TransferStatus.EXPIRED -> "Expired"
                            TransferStatus.EXHAUSTED -> "Download limit reached"
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
                if (state.shownSaved) Text("Shown uploads saved on this device")
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
                    state.error.orEmpty(),
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                )
                if (!state.keyUnavailable)
                    Button(
                        onClick = {
                            when (state.retryAction(activeExistingId)) {
                                ReceiveRetry.SIGN_IN -> onSignIn()
                                ReceiveRetry.SAVE -> viewModel.downloadReceivedFiles()
                                ReceiveRetry.REOPEN -> viewModel.openExisting(activeExistingId!!)
                                ReceiveRetry.CREATE -> viewModel.createSlot()
                            }
                        }
                    ) {
                        Text(
                            stringResource(
                                if (state.requiresLogin) R.string.sign_in
                                else if (state.slotId != null) R.string.retry_saving
                                else R.string.retry
                            )
                        )
                    }
            }
            if (state.checkpointState != "ready") {
                Text(
                    if (state.checkpointState == "recovery")
                        "An older saved checkpoint contains invalid or unsupported data. Original records and saved files are retained; saving is paused to avoid duplicate downloads. Retrying after an app update may resolve it."
                    else
                        "Importing saved-file checkpoints. Saved files remain on this device; saving resumes when the import finishes."
                )
                TextButton(
                    onClick = viewModel::continueCheckpointImport,
                    enabled = !state.isPaging,
                ) {
                    Text(
                        if (state.checkpointState == "recovery") "Retry checkpoint import"
                        else "Continue checkpoint import"
                    )
                }
                TextButton(
                    onClick = {
                        runCatching {
                            context.startActivity(Intent(DownloadManager.ACTION_VIEW_DOWNLOADS))
                        }
                    }
                ) {
                    Text(stringResource(R.string.open_downloads))
                }
            }
            if (state.downloadComplete || state.savedFileCount > 0) {
                Text(stringResource(R.string.saved_count, state.savedFileCount))
                OutlinedButton(
                    onClick = {
                        runCatching {
                            context.startActivity(Intent(DownloadManager.ACTION_VIEW_DOWNLOADS))
                        }
                    }
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
                            Text("First")
                        }
                        TextButton(
                            onClick = viewModel::previousPage,
                            enabled = canNavigate && state.pager.previous.isNotEmpty(),
                        ) {
                            Text("Previous")
                        }
                        TextButton(
                            onClick = viewModel::nextPage,
                            enabled = canNavigate && page.nextCursor != null,
                        ) {
                            Text("Next")
                        }
                    }
            }
        }
    }
}
