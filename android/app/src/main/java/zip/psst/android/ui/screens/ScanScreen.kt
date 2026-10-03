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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.data.GuestDownload
import zip.psst.android.data.SavedGuestFile
import zip.psst.android.viewmodel.ScanViewModel
import zip.psst.android.viewmodel.ServerConfigViewModel
import zip.psst.android.viewmodel.TestResult
import zip.psst.shared.model.ScanInputKind
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import java.text.DateFormat
import java.util.Date

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ScanScreen(
    onBack: () -> Unit,
    showHistoryInitially: Boolean = false,
    viewModel: ScanViewModel = viewModel(),
    account: ServerConfigViewModel = viewModel(),
) {
    val context = LocalContext.current
    val state by viewModel.state.collectAsState()
    val accountState by account.uiState.collectAsState()
    var history by remember { mutableStateOf(showHistoryInitially) }
    var paste by remember { mutableStateOf("") }
    var pairingConfirmation by remember { mutableStateOf(false) }
    var removal by remember { mutableStateOf<GuestDownload?>(null) }
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
            history = false
            paste = ""
            when (viewModel.state.value.kind) {
                ScanInputKind.DOWNLOAD -> receive()
                ScanInputKind.PAIRING -> pairingConfirmation = true
                else -> Unit
            }
        }
    }
    val scanner =
        rememberLauncherForActivityResult(ScanContract()) { result ->
            result.contents?.let(::accept)
        }
    fun launchScanner() {
        scanner.launch(
            ScanOptions()
                .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                .setPrompt("Scan a psst.zip QR code. Go back to paste a link.")
                .setBeepEnabled(false)
                .setOrientationLocked(false)
        )
    }
    val camera =
        rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
            if (granted) launchScanner()
            else
                viewModel.error(
                    "Camera access was denied. Paste a link below, or enable Camera in Android app settings and scan again."
                )
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
            intent.clipData = ClipData.newRawUri(file.name, uri)
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
                title = { Text(if (history) "Received on this device" else "Scan QR code") },
                navigationIcon = {
                    IconButton(onClick = ::back) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back")
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
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (state.pendingReceipts > 0 && !state.busy && !accountState.isTesting) {
                TextButton(onClick = viewModel::retryAllReceipts) {
                    Text("Retry pending delivery receipts (${state.pendingReceipts})")
                }
            }
            if (state.pendingCleanup > 0 && !state.busy && !accountState.isTesting) {
                Text("${state.pendingCleanup} interrupted upload(s) need server cleanup.")
                TextButton(onClick = viewModel::retryCleanup) { Text("Retry upload cleanup") }
            }
            state.notice?.let { Text(it, color = MaterialTheme.colorScheme.primary) }
            if (state.error != null) Text(state.error!!, color = MaterialTheme.colorScheme.error)
            if (!state.busy && !accountState.isTesting) {
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    TextButton(
                        onClick = {
                            viewModel.clear()
                            history = false
                        }
                    ) {
                        Text("Scan / Paste")
                    }
                    TextButton(
                        onClick = {
                            viewModel.refreshHistory()
                            history = true
                        }
                    ) {
                        Text("Received history")
                    }
                }
            }
            if (history) {
                Text(
                    "Local history stays on this device, across sign-outs. Removing an entry keeps its files and does not revoke the sender’s link.",
                    style = MaterialTheme.typography.bodySmall,
                )
                if (state.history.isEmpty()) Text("No files received yet")
                state.history.forEach { record ->
                    Card(
                        Modifier.fillMaxWidth(),
                        colors =
                            CardDefaults.cardColors(
                                containerColor = MaterialTheme.colorScheme.surfaceVariant
                            ),
                    ) {
                        Column(
                            Modifier.padding(16.dp),
                            verticalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            Text(
                                record.files.firstOrNull()?.name ?: "Received transfer",
                                style = MaterialTheme.typography.titleMedium,
                            )
                            Text(
                                "${record.saved.count(viewModel::fileExists)} of ${record.files.size} files available · ${record.saved.sumOf { it.size }} bytes"
                            )
                            Text(
                                if (record.complete && record.saved.all(viewModel::fileExists))
                                    "Saved on this device"
                                else if (record.complete) "Some local files are missing"
                                else "Partial / interrupted"
                            )
                            Text(record.origin, style = MaterialTheme.typography.bodySmall)
                            Text(
                                DateFormat.getDateTimeInstance().format(Date(record.createdAt)),
                                style = MaterialTheme.typography.bodySmall,
                            )
                            Row {
                                TextButton(
                                    onClick = {
                                        viewModel.open(record)
                                        history = false
                                    }
                                ) {
                                    Text(if (record.complete) "View files" else "Resume / View")
                                }
                                TextButton(onClick = { removal = record }) { Text("Remove") }
                            }
                        }
                    }
                }
            } else {
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
                            Text(
                                if (record?.complete == true && !viewModel.missingFiles())
                                    "${record.saved.size} ${if (record.saved.size == 1) "file" else "files"} saved"
                                else if (record?.complete == true) "Some local files are missing"
                                else state.stage.ifBlank { "Ready to receive" },
                                style = MaterialTheme.typography.headlineSmall,
                            )
                            Text("Downloads/psst.zip")
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
                                        Text(file.name)
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
                            if (viewModel.missingFiles())
                                Button(onClick = { receive(true) }) {
                                    Text("Redownload missing files")
                                }
                            else if (record?.complete != true)
                                Button(onClick = { receive() }) { Text("Resume receiving") }
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
                                    "Choose files deliberately to send to the server above. Up to 25 MiB per file."
                                )
                                Text("${state.uploadFiles.size} files selected")
                                state.uploadFiles.forEach { Text(viewModel.uploadName(it)) }
                                OutlinedButton(onClick = { picker.launch(arrayOf("*/*")) }) {
                                    Text("Choose files")
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
                            Button(
                                onClick = {
                                    if (
                                        ContextCompat.checkSelfPermission(
                                            context,
                                            Manifest.permission.CAMERA,
                                        ) == PackageManager.PERMISSION_GRANTED
                                    )
                                        launchScanner()
                                    else camera.launch(Manifest.permission.CAMERA)
                                },
                                modifier = Modifier.fillMaxWidth(),
                            ) {
                                Text("Scan QR code")
                            }
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
                                "Scanning starts receiving automatically. Files are saved in Downloads/psst.zip and never opened automatically.",
                                style = MaterialTheme.typography.bodySmall,
                            )
                        }
                    }
            }
        }
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
    removal?.let { record ->
        AlertDialog(
            onDismissRequest = { removal = null },
            title = { Text("Remove local history?") },
            text = {
                Text(
                    "Saved files remain in Downloads/psst.zip. The sender’s link will not be revoked."
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        viewModel.remove(record)
                        removal = null
                    }
                ) {
                    Text("Remove")
                }
            },
            dismissButton = { TextButton(onClick = { removal = null }) { Text("Cancel") } },
        )
    }
}
