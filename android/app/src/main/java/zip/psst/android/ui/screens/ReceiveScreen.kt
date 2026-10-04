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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.R
import zip.psst.android.ui.components.AccountIndicator
import zip.psst.android.ui.components.LinkPanel
import zip.psst.android.viewmodel.ReceiveRetry
import zip.psst.android.viewmodel.ReceiveViewModel
import zip.psst.android.viewmodel.retryAction

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
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.receive_link)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.back))
                    }
                },
            )
        }
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
            if (state.slotId == null && existingId == null && !state.isCreatingSlot) {
                Text("Create a private inbox link for others to send files to you.")
                OutlinedTextField(
                    value = state.maxFilesInput,
                    onValueChange = viewModel::setMaxFiles,
                    label = { Text("Maximum files (optional)") },
                    supportingText = {
                        Text(
                            "Empty means unlimited. Unfinished uploads use an allowance too; deleting files does not restore it."
                        )
                    },
                    keyboardOptions =
                        androidx.compose.foundation.text.KeyboardOptions(
                            keyboardType = androidx.compose.ui.text.input.KeyboardType.Number
                        ),
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Button(onClick = viewModel::createSlot) { Text("Create receive link") }
            }
            if (state.legacyReadOnly)
                Text(
                    "This older inbox is read-only. Save its existing files and create a new receive link for further uploads."
                )
            if (state.slotId != null && state.maxFiles > 0)
                Text(
                    "${state.remainingFiles ?: "…"} of ${state.maxFiles} file allocations remaining · ${state.reservedFiles} used"
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
                        }
                    ),
                    Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                    style = MaterialTheme.typography.titleMedium,
                )
            if (state.isCreatingSlot) CircularProgressIndicator()
            state.uploadUrl?.let { LinkPanel(it) }
            if (state.slotId != null && existingId == null)
                OutlinedTextField(
                    value = state.localName,
                    onValueChange = viewModel::renameLocal,
                    label = { Text("Name (optional)") },
                    supportingText = { Text("Only on this device") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
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
                            when (state.retryAction(existingId)) {
                                ReceiveRetry.SIGN_IN -> onSignIn()
                                ReceiveRetry.SAVE -> viewModel.downloadReceivedFiles()
                                ReceiveRetry.REOPEN -> viewModel.openExisting(existingId!!)
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
            } else if (state.isDownloading) {
                LinearProgressIndicator(
                    progress = { state.downloadProgress },
                    modifier = Modifier.fillMaxWidth(),
                )
            } else if (state.slotStatus == "has_uploads") {
                Button(onClick = viewModel::downloadReceivedFiles) {
                    Text(stringResource(R.string.save_files))
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
        }
    }
}
