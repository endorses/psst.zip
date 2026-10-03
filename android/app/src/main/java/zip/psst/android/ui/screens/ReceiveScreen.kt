package zip.psst.android.ui.screens

import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.HourglassTop
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.ui.components.QrCodeImage
import zip.psst.android.viewmodel.ReceiveViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ReceiveScreen(
    onSlotCreated: (slotId: String, encryptionKey: String) -> Unit,
    onBack: () -> Unit,
    onSignIn: () -> Unit,
    viewModel: ReceiveViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current
    val clipboardManager = LocalClipboardManager.current
    var linkCopied by remember(state.uploadUrl) { mutableStateOf(false) }
    var showLink by remember(state.uploadUrl) { mutableStateOf(false) }

    LaunchedEffect(Unit) {
        if (state.slotId == null && !state.isCreatingSlot) viewModel.createSlot()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Receive Files") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        }
    ) { padding ->
        BoxWithConstraints(Modifier.fillMaxSize().padding(padding)) {
            val qrSize = minOf(220.dp, maxWidth - 80.dp, (maxHeight * 0.4f).coerceAtLeast(140.dp))
            Column(
                modifier =
                    Modifier.fillMaxSize()
                        .verticalScroll(rememberScrollState())
                        .padding(24.dp, 16.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                val status =
                    when {
                        state.isCreatingSlot -> "Creating drop slot…"
                        state.error != null -> "Unable to receive files"
                        state.downloadComplete -> "Files saved to Downloads"
                        state.isDownloading -> "Downloading and decrypting…"
                        state.slotStatus == "has_uploads" -> "Files received"
                        else -> "Waiting for upload"
                    }
                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    if (state.isCreatingSlot || state.isDownloading) {
                        CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                    } else {
                        val icon =
                            when {
                                state.error != null -> Icons.Default.ErrorOutline
                                state.downloadComplete -> Icons.Default.CheckCircle
                                state.slotStatus == "has_uploads" -> Icons.Default.Download
                                else -> Icons.Default.HourglassTop
                            }
                        Icon(icon, contentDescription = null, modifier = Modifier.size(20.dp))
                    }
                    Text(status, style = MaterialTheme.typography.titleMedium)
                }

                state.uploadUrl?.let { uploadUrl ->
                    val copyLink = {
                        clipboardManager.setText(AnnotatedString(uploadUrl))
                        linkCopied = true
                    }
                    Spacer(Modifier.height(12.dp))
                    QrCodeImage(data = uploadUrl, size = qrSize)
                    Spacer(Modifier.height(12.dp))
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        OutlinedButton(onClick = copyLink, modifier = Modifier.weight(1f)) {
                            Text("Copy Link")
                        }
                        FilledTonalButton(
                            onClick = {
                                val intent =
                                    Intent(Intent.ACTION_SEND).apply {
                                        type = "text/plain"
                                        putExtra(Intent.EXTRA_TEXT, uploadUrl)
                                    }
                                context.startActivity(
                                    Intent.createChooser(intent, "Share upload link")
                                )
                            },
                            modifier = Modifier.weight(1f),
                        ) {
                            Text("Share Link")
                        }
                    }
                    if (linkCopied) {
                        Text(
                            "Link copied",
                            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                    Spacer(Modifier.height(8.dp))
                    Text(
                        uploadUrl,
                        modifier = Modifier.fillMaxWidth(),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                    TextButton(onClick = { showLink = true }) { Text("View link") }

                    if (showLink) {
                        AlertDialog(
                            onDismissRequest = { showLink = false },
                            title = { Text("Upload link") },
                            text = {
                                Column {
                                    SelectionContainer {
                                        Text(
                                            uploadUrl,
                                            modifier =
                                                Modifier.heightIn(max = 280.dp)
                                                    .verticalScroll(rememberScrollState()),
                                            style = MaterialTheme.typography.bodyMedium,
                                        )
                                    }
                                    if (linkCopied) {
                                        Text(
                                            "Link copied",
                                            modifier =
                                                Modifier.semantics {
                                                    liveRegion = LiveRegionMode.Polite
                                                },
                                            style = MaterialTheme.typography.bodySmall,
                                        )
                                    }
                                }
                            },
                            confirmButton = {
                                TextButton(onClick = { showLink = false }) { Text("Done") }
                            },
                            dismissButton = { TextButton(onClick = copyLink) { Text("Copy Link") } },
                        )
                    }
                }

                when {
                    state.error != null -> {
                        Spacer(Modifier.height(8.dp))
                        Text(
                            state.error ?: "Unknown error",
                            color = MaterialTheme.colorScheme.error,
                            style = MaterialTheme.typography.bodyMedium,
                            textAlign = TextAlign.Center,
                        )
                        if (state.requiresLogin) {
                            Button(onClick = onSignIn) { Text("Sign in") }
                        } else {
                            Button(onClick = { viewModel.createSlot() }) { Text("Retry") }
                        }
                    }
                    state.isDownloading -> {
                        Spacer(Modifier.height(8.dp))
                        LinearProgressIndicator(
                            progress = { state.downloadProgress },
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                    state.slotStatus == "has_uploads" && !state.downloadComplete -> {
                        Button(onClick = { viewModel.downloadReceivedFiles() }) {
                            Text("Download & Decrypt")
                        }
                    }
                }
            }
        }
    }
}
