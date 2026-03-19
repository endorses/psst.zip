package zip.psst.android.ui.screens

import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.HourglassTop
import androidx.compose.material.icons.filled.Share
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.ui.components.ExpiryCountdown
import zip.psst.android.ui.components.QrCodeImage
import zip.psst.android.viewmodel.TransferDetailViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TransferDetailScreen(
    transferId: String,
    encryptionKey: String,
    type: String,
    onBack: () -> Unit,
    viewModel: TransferDetailViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current
    val clipboardManager = LocalClipboardManager.current

    LaunchedEffect(transferId) {
        viewModel.load(transferId, encryptionKey, type)
    }

    val title = when (type) {
        "sent" -> "Transfer Details"
        "receive", "received" -> "Drop Slot"
        else -> "Transfer"
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            if (state.isLoading) {
                Column(
                    modifier = Modifier.fillMaxSize(),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                ) {
                    CircularProgressIndicator()
                }
            } else {
                // Status icon
                val statusIcon = when (state.status) {
                    "complete", "has_uploads" -> Icons.Default.CheckCircle
                    else -> Icons.Default.HourglassTop
                }
                val statusColor = when (state.status) {
                    "complete", "has_uploads" -> MaterialTheme.colorScheme.primary
                    "expired" -> MaterialTheme.colorScheme.error
                    else -> MaterialTheme.colorScheme.onSurfaceVariant
                }

                Icon(
                    imageVector = statusIcon,
                    contentDescription = null,
                    modifier = Modifier.size(32.dp),
                    tint = statusColor,
                )

                Spacer(Modifier.height(8.dp))

                Text(
                    text = "Status: ${state.status}",
                    style = MaterialTheme.typography.titleSmall,
                    color = statusColor,
                )

                if (state.fileCount > 0) {
                    Spacer(Modifier.height(4.dp))
                    val sizeText = if (state.totalSize > 0) {
                        " (${formatFileSize(state.totalSize)})"
                    } else {
                        ""
                    }
                    Text(
                        text = "${state.fileCount} file(s)$sizeText",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }

                ExpiryCountdown(
                    expiresAt = state.expiresAt,
                    modifier = Modifier.padding(top = 4.dp),
                )

                Spacer(Modifier.height(24.dp))

                // QR Code
                if (state.shareUrl.isNotBlank()) {
                    Text(
                        text = if (type == "receive" || type == "received") {
                            "Scan to upload files"
                        } else {
                            "Scan to download"
                        },
                        style = MaterialTheme.typography.titleMedium,
                    )

                    Spacer(Modifier.height(16.dp))

                    QrCodeImage(data = state.shareUrl)

                    Spacer(Modifier.height(24.dp))

                    // Share / Copy buttons
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        OutlinedButton(
                            onClick = {
                                clipboardManager.setText(AnnotatedString(state.shareUrl))
                            },
                            modifier = Modifier.weight(1f),
                        ) {
                            Icon(Icons.Default.ContentCopy, contentDescription = null, modifier = Modifier.size(18.dp))
                            Spacer(Modifier.width(8.dp))
                            Text("Copy Link")
                        }

                        FilledTonalButton(
                            onClick = {
                                val sendIntent = Intent().apply {
                                    action = Intent.ACTION_SEND
                                    putExtra(Intent.EXTRA_TEXT, state.shareUrl)
                                    this.type = "text/plain"
                                }
                                val shareIntent = Intent.createChooser(sendIntent, "Share link")
                                context.startActivity(shareIntent)
                            },
                            modifier = Modifier.weight(1f),
                        ) {
                            Icon(Icons.Default.Share, contentDescription = null, modifier = Modifier.size(18.dp))
                            Spacer(Modifier.width(8.dp))
                            Text("Share Link")
                        }
                    }
                }

                state.error?.let { error ->
                    Spacer(Modifier.height(16.dp))
                    Text(
                        text = error,
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                        textAlign = TextAlign.Center,
                    )
                }
            }
        }
    }
}
