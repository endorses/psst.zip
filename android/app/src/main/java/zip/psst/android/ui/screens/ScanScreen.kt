package zip.psst.android.ui.screens

import android.Manifest
import android.app.Activity
import android.content.ClipData
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
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
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.R
import zip.psst.android.data.SavedGuestFile
import zip.psst.android.data.compactHistoryTitle
import zip.psst.android.data.receivedFilenameLabel
import zip.psst.android.ui.components.AbuseReportButton
import zip.psst.android.ui.components.EmbeddedScanner
import zip.psst.android.viewmodel.ScanViewModel
import zip.psst.android.viewmodel.ServerConfigViewModel
import zip.psst.android.viewmodel.TestResult
import zip.psst.shared.model.ScanInputKind

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ScanScreen(
    onBack: () -> Unit,
    onHistory: () -> Unit = {},
    historical: Boolean = false,
    viewModel: ScanViewModel = viewModel(),
    account: ServerConfigViewModel = viewModel(),
) {
    val context = LocalContext.current
    val state by viewModel.state.collectAsState()
    val accountState by account.uiState.collectAsState()

    var secondary by remember { mutableStateOf(false) }
    var paste by remember { mutableStateOf("") }
    var pairingConfirmation by remember { mutableStateOf(false) }

    var redownload by remember { mutableStateOf(false) }
    var stopConfirmation by remember { mutableStateOf(false) }
    val storage =
        rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
            if (granted) viewModel.receive(redownload)
            else
                viewModel.error(
                    "Storage access is required to save in Downloads/psst.zip on this Android version. Allow it and retry."
                )
        }
    fun receive(missing: Boolean = false) {
        redownload = missing
        if (
            Build.VERSION.SDK_INT <= 28 &&
                ContextCompat.checkSelfPermission(
                    context,
                    Manifest.permission.WRITE_EXTERNAL_STORAGE,
                ) != PackageManager.PERMISSION_GRANTED
        )
            storage.launch(Manifest.permission.WRITE_EXTERNAL_STORAGE)
        else viewModel.receive(missing)
    }
    fun accept(raw: String) {
        if (!accountState.isTesting && viewModel.classify(raw)) {
            paste = ""
            when (viewModel.state.value.kind) {
                ScanInputKind.DOWNLOAD -> receive()
                ScanInputKind.PAIRING -> pairingConfirmation = true
                else -> Unit
            }
        }
    }
    val picker =
        rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) {
            viewModel.selectUpload(it)
        }
    val lifecycle = LocalLifecycleOwner.current
    DisposableEffect(lifecycle) {
        val observer = LifecycleEventObserver { _, event ->
            if (
                event == Lifecycle.Event.ON_STOP &&
                    (context as? Activity)?.isChangingConfigurations != true
            )
                viewModel.cancel()
        }
        lifecycle.lifecycle.addObserver(observer)
        onDispose { lifecycle.lifecycle.removeObserver(observer) }
    }
    fun back() {
        if (!accountState.isTesting) {
            if (
                state.busy &&
                    state.stage in
                        listOf(
                            "Inspecting transfer",
                            "Downloading",
                            "Decrypting",
                            "Saving",
                            "Preparing upload",
                            "Encrypting",
                            "Uploading",
                        )
            )
                stopConfirmation = true
            else {
                viewModel.cancel()
                onBack()
            }
        }
    }
    BackHandler { back() }
    fun openFile(file: SavedGuestFile, share: Boolean) {
        if (!viewModel.fileExists(file)) {
            viewModel.error(
                "This saved file is missing. You can explicitly redownload it while the link remains available."
            )
            return
        }
        try {
            val uri = Uri.parse(file.uri)
            val intent =
                if (share)
                    Intent(Intent.ACTION_SEND).apply {
                        type = file.mimeType
                        putExtra(Intent.EXTRA_STREAM, uri)
                    }
                else Intent(Intent.ACTION_VIEW).setDataAndType(uri, file.mimeType)
            intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            intent.clipData = ClipData.newRawUri(receivedFilenameLabel(file.name), uri)
            context.startActivity(
                if (share) Intent.createChooser(intent, "Share saved file") else intent
            )
        } catch (_: Exception) {
            viewModel.error(
                "No app could open this file. Try Share or find it in Downloads/psst.zip."
            )
        }
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        if (state.kind == ScanInputKind.UPLOAD) "Send files"
                        else if (historical) "Downloaded files" else "Scan QR code"
                    )
                },
                actions = {
                    if (state.kind != null)
                        TextButton(onClick = { secondary = true }) { Text("More") }
                },
                navigationIcon = {
                    IconButton(onClick = ::back) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back")
                    }
                },
            )
        }
    ) { padding ->
        if (state.kind == ScanInputKind.UPLOAD) {
            ScannedSendContent(
                viewModel,
                state,
                Modifier.padding(padding),
                { picker.launch(arrayOf("*/*")) },
            )
            return@Scaffold
        }
        Column(
            Modifier.fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(20.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (state.pendingReceipts > 0 && !state.busy && !accountState.isTesting) {
                TextButton(onClick = viewModel::retryAllReceipts) {
                    Text("Retry next pending delivery receipts")
                }
            }
            if (state.pendingCleanup > 0 && !state.busy && !accountState.isTesting) {
                Text(
                    "Interrupted uploads need server cleanup. Each retry processes a bounded batch."
                )
                TextButton(onClick = viewModel::retryCleanup) { Text("Retry upload cleanup") }
            }
            state.notice?.let { Text(it, color = MaterialTheme.colorScheme.primary) }
            if (state.error != null) Text(state.error!!, color = MaterialTheme.colorScheme.error)
            if (state.kind == null) AbuseReportButton(state.reportReference)
            run {
                if (state.origin.isNotBlank())
                    Text(state.origin, style = MaterialTheme.typography.titleMedium)
                if (state.busy) {
                    Text(state.stage, style = MaterialTheme.typography.headlineSmall)
                    Text(
                        "File ${state.fileIndex.coerceAtLeast(1)}${state.record?.files?.size?.takeIf { it > 0 }?.let { " of $it" }.orEmpty()}"
                    )
                    if (state.stage == "Downloading" || state.stage == "Uploading") {
                        Text("${state.bytes} / ${state.totalBytes ?: "?"} bytes")
                        LinearProgressIndicator(
                            progress = {
                                (state.bytes.toFloat() / (state.totalBytes ?: 1).coerceAtLeast(1))
                                    .coerceIn(0f, 1f)
                            },
                            modifier = Modifier.fillMaxWidth(),
                        )
                    } else LinearProgressIndicator(Modifier.fillMaxWidth())
                    Text(
                        "Keep this app in the foreground. Leaving pauses receiving; saved files are kept.",
                        style = MaterialTheme.typography.bodySmall,
                    )
                    OutlinedButton(onClick = viewModel::cancel) { Text("Cancel") }
                } else
                    when (state.kind) {
                        ScanInputKind.DOWNLOAD -> {
                            val record = state.record
                            val availability = viewModel.downloadAvailability()
                            Text(
                                if (record?.complete == true && !viewModel.missingFiles())
                                    "${record.saved.size} ${if (record.saved.size == 1) "file" else "files"} saved"
                                else if (record?.complete == true) "Some local files are missing"
                                else state.stage.ifBlank { "Ready to receive" },
                                style = MaterialTheme.typography.headlineSmall,
                            )
                            record?.sharedTitle?.let {
                                Text(it, style = MaterialTheme.typography.titleMedium)
                            }
                            Text("Downloads/psst.zip")
                            if (state.fileAttempts.isNotEmpty())
                                record?.files?.forEach { file ->
                                    Text(
                                        "${receivedFilenameLabel(file.name)}: " +
                                            when (
                                                val attempts =
                                                    state.fileAttempts[file.blobId.lowercase()]
                                            ) {
                                                0L -> "Download limit reached"
                                                null -> "Availability unknown"
                                                else -> "$attempts download attempts remaining"
                                            },
                                        style = MaterialTheme.typography.bodySmall,
                                    )
                                }
                            record?.saved?.forEach { file ->
                                Card(
                                    Modifier.fillMaxWidth(),
                                    colors =
                                        CardDefaults.cardColors(
                                            containerColor =
                                                MaterialTheme.colorScheme.surfaceVariant
                                        ),
                                ) {
                                    Column(Modifier.padding(12.dp)) {
                                        Text(receivedFilenameLabel(file.name))
                                        Row {
                                            TextButton(onClick = { openFile(file, false) }) {
                                                Text("Open")
                                            }
                                            TextButton(onClick = { openFile(file, true) }) {
                                                Text("Share")
                                            }
                                        }
                                    }
                                }
                            }
                            if (availability?.allMissingExhausted == true)
                                Text(
                                    "Download limit reached. Saved local copies can still be opened or shared."
                                )
                            else if (availability?.partiallyExhausted == true)
                                Text(
                                    "${availability.exhausted} missing files have reached their download limit. Only available files will be downloaded; you will confirm the available-file download first."
                                )
                            val canDownload =
                                availability?.allMissingExhausted != true &&
                                    !state.refreshingAvailability
                            if (viewModel.missingFiles())
                                Button(onClick = { receive(true) }, enabled = canDownload) {
                                    Text(
                                        if (availability?.partiallyExhausted == true)
                                            "Redownload available missing files"
                                        else "Redownload missing files"
                                    )
                                }
                            else if (record?.complete != true)
                                Button(onClick = { receive() }, enabled = canDownload) {
                                    Text(
                                        if (availability?.partiallyExhausted == true)
                                            "Download available files"
                                        else "Resume receiving"
                                    )
                                }
                            if (record?.files?.isNotEmpty() == true)
                                TextButton(
                                    onClick = viewModel::refreshDownloadAvailability,
                                    enabled = !state.refreshingAvailability,
                                ) {
                                    Text(
                                        if (state.refreshingAvailability) "Checking availability…"
                                        else "Refresh availability"
                                    )
                                }
                            if (record?.receiptPending == true) {
                                Text("Files are saved. The sender’s delivery receipt is pending.")
                                TextButton(onClick = viewModel::retryReceipt) {
                                    Text("Retry receipt")
                                }
                            }
                        }
                        ScanInputKind.UPLOAD -> {
                            Text(
                                if (state.uploaded) "Files sent" else "Send to this receive link",
                                style = MaterialTheme.typography.headlineSmall,
                            )
                            if (!state.uploaded) {
                                Text(
                                    "Choose files deliberately to send to the server above. Its file limit is checked before uploading."
                                )
                                Text(
                                    pluralStringResource(
                                        R.plurals.files_selected_count,
                                        state.uploadFiles.size,
                                        state.uploadFiles.size,
                                    )
                                )
                                if (state.maxUploadFiles > 0)
                                    Text(
                                        "${state.remainingUploadFiles ?: "…"} file allocations remaining. Unfinished uploads also count; deletion does not restore the allowance."
                                    )
                                Text(state.uploadCapacityMessage)
                                state.uploadFiles.forEach { uri ->
                                    Row(verticalAlignment = Alignment.CenterVertically) {
                                        Text(
                                            viewModel.uploadName(uri),
                                            modifier = Modifier.weight(1f),
                                        )
                                        TextButton(onClick = { viewModel.removeUpload(uri) }) {
                                            Text("Remove")
                                        }
                                    }
                                }
                                TextButton(onClick = viewModel::refreshUploadPolicy) {
                                    Text("Refresh capacity")
                                }
                                OutlinedButton(onClick = { picker.launch(arrayOf("*/*")) }) {
                                    Text("Add files")
                                }
                                Button(
                                    onClick = viewModel::upload,
                                    enabled = state.uploadFiles.isNotEmpty(),
                                ) {
                                    Text("Send files")
                                }
                            }
                        }
                        ScanInputKind.PAIRING -> {
                            Text(
                                "Set up this account",
                                style = MaterialTheme.typography.headlineSmall,
                            )
                            Text("This replaces your current app login only after you confirm.")
                            (accountState.testResult as? TestResult.Error)?.let {
                                Text(it.message, color = MaterialTheme.colorScheme.error)
                            }
                            if (accountState.isTesting) CircularProgressIndicator()
                            else
                                Button(onClick = { pairingConfirmation = true }) {
                                    Text("Set up account")
                                }
                        }
                        null -> {
                            Text(
                                "Receive encrypted files without signing in.",
                                style = MaterialTheme.typography.titleMedium,
                            )
                            if (!historical)
                                EmbeddedScanner(onCode = ::accept, onError = viewModel::error)
                            OutlinedTextField(
                                value = paste,
                                onValueChange = { paste = it },
                                label = { Text("Paste link") },
                                modifier = Modifier.fillMaxWidth(),
                                minLines = 2,
                                maxLines = 4,
                            )
                            Button(
                                onClick = { accept(paste) },
                                enabled = paste.isNotBlank(),
                                modifier = Modifier.fillMaxWidth(),
                            ) {
                                Text("Receive files")
                            }
                            Text(
                                "Transfers up to 100 MiB start automatically. Larger transfers ask for confirmation. Files are saved in Downloads/psst.zip and never opened automatically.",
                                style = MaterialTheme.typography.bodySmall,
                            )
                        }
                    }
            }
        }
    }
    if (secondary)
        AlertDialog(
            onDismissRequest = { secondary = false },
            title = { Text("Transfer options") },
            text = {
                Column {
                    Text(state.origin)
                    AbuseReportButton(state.reportReference)
                    if (state.pendingReceipts > 0)
                        TextButton(onClick = viewModel::retryAllReceipts, enabled = !state.busy) {
                            Text("Retry receipts")
                        }
                    if (state.pendingCleanup > 0)
                        TextButton(onClick = viewModel::retryCleanup, enabled = !state.busy) {
                            Text("Retry upload cleanup")
                        }
                    if (!historical)
                        TextButton(
                            onClick = {
                                viewModel.clear()
                                secondary = false
                            },
                            enabled = !state.busy,
                        ) {
                            Text("Scan again")
                        }
                    TextButton(
                        onClick = {
                            secondary = false
                            onHistory()
                        },
                        enabled = !state.busy,
                    ) {
                        Text("History")
                    }
                }
            },
            confirmButton = { TextButton(onClick = { secondary = false }) { Text("Close") } },
        )
    state.downloadConsent
        ?.takeIf { !state.busy }
        ?.let { consent ->
            AlertDialog(
                onDismissRequest = viewModel::dismissDownloadConsent,
                title = { Text("Download these files?") },
                text = {
                    Text(
                        "${consent.files.size} ${if (consent.files.size == 1) "file" else "files"} from ${consent.origin}\n\n" +
                            "Total: ${android.text.format.Formatter.formatFileSize(context, consent.totalBytes)}\n" +
                            "Still to save: ${android.text.format.Formatter.formatFileSize(context, consent.remainingBytes)}\n\n" +
                            (if (consent.totalBytes > 100L * 1024 * 1024)
                                "This transfer exceeds 100 MiB and may use mobile data. "
                            else "") +
                            (if (consent.skippedBlobIds.isNotEmpty())
                                "${consent.skippedBlobIds.size} files have no download attempts left and will be skipped: " +
                                    consent.files
                                        .filter { it.blobId in consent.skippedBlobIds }
                                        .joinToString(", ") {
                                            compactHistoryTitle(receivedFilenameLabel(it.name), 48)
                                        } +
                                    ". "
                            else "") +
                            "Downloads keep at least 256 MiB of storage free.",
                        modifier =
                            Modifier.heightIn(max = 280.dp).verticalScroll(rememberScrollState()),
                    )
                },
                confirmButton = {
                    TextButton(onClick = viewModel::confirmDownload) { Text("Download") }
                },
                dismissButton = {
                    TextButton(onClick = viewModel::dismissDownloadConsent) { Text("Cancel") }
                },
            )
        }
    if (stopConfirmation)
        AlertDialog(
            onDismissRequest = { stopConfirmation = false },
            title = { Text("Stop transfer?") },
            text = {
                Text(
                    "Files already saved stay in Downloads/psst.zip. Receiving can be resumed while the link remains available. An interrupted upload will be cleaned up."
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        stopConfirmation = false
                        viewModel.cancel()
                        onBack()
                    }
                ) {
                    Text("Stop transfer")
                }
            },
            dismissButton = {
                TextButton(onClick = { stopConfirmation = false }) { Text("Keep transferring") }
            },
        )
    if (pairingConfirmation)
        AlertDialog(
            onDismissRequest = { pairingConfirmation = false },
            title = { Text("Set up account?") },
            text = {
                Text(
                    "Connect to ${state.origin} and replace your current login? Your local received files stay on this device."
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        pairingConfirmation = false
                        viewModel.pairingPayload()?.let {
                            account.pair(it) { viewModel.accountConnected() }
                        }
                    }
                ) {
                    Text("Set up account")
                }
            },
            dismissButton = {
                TextButton(onClick = { pairingConfirmation = false }) { Text("Cancel") }
            },
        )
}

@Composable
private fun ScannedSendContent(
    viewModel: ScanViewModel,
    state: zip.psst.android.viewmodel.ScanState,
    modifier: Modifier,
    addFiles: () -> Unit,
) {
    Column(
        modifier.fillMaxSize().imePadding().navigationBarsPadding().padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(
            state.sharedTitle ?: "Send to this receive link",
            style = MaterialTheme.typography.headlineSmall,
        )
        Text(state.origin, style = MaterialTheme.typography.bodySmall)
        if (state.origin.startsWith("http://"))
            Text("HTTP · unencrypted connection", color = MaterialTheme.colorScheme.error)
        Text(
            pluralStringResource(
                R.plurals.files_selected_count,
                state.uploadFiles.size,
                state.uploadFiles.size,
            ) + (state.remainingUploadFiles?.let { " · $it files remaining" } ?: "")
        )
        LazyColumn(
            Modifier.weight(1f).fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            item {
                state.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                state.notice?.let { Text(it) }
            }
            items(state.uploadFiles, key = { it.toString() }) { uri ->
                Card {
                    Row(
                        Modifier.fillMaxWidth().padding(12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(viewModel.uploadName(uri), Modifier.weight(1f))
                        if (!state.busy)
                            TextButton(onClick = { viewModel.removeUpload(uri) }) { Text("Remove") }
                    }
                }
            }
            item { Text(state.uploadCapacityMessage, style = MaterialTheme.typography.bodySmall) }
            if (!state.busy && !state.uploaded)
                item {
                    TextButton(onClick = viewModel::refreshUploadPolicy) {
                        Text("Refresh capacity")
                    }
                }
        }
        if (state.busy) {
            Text(state.stage)
            LinearProgressIndicator(Modifier.fillMaxWidth())
            TextButton(onClick = viewModel::cancel) { Text("Cancel") }
        } else if (state.uploaded) Text("Files sent", style = MaterialTheme.typography.titleMedium)
        else {
            OutlinedButton(onClick = addFiles, modifier = Modifier.fillMaxWidth()) {
                Text("Add files")
            }
            Button(
                onClick = viewModel::upload,
                enabled = state.uploadFiles.isNotEmpty(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("Send files")
            }
        }
    }
}
