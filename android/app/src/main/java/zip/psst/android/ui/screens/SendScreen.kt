package zip.psst.android.ui.screens

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.InsertDriveFile
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
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
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.viewmodel.SendViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SendScreen(
    sharedUris: List<Uri>,
    onTransferCreated: (transferId: String, encryptionKey: String, type: String) -> Unit,
    onBack: () -> Unit,
    onSignIn: () -> Unit,
    viewModel: SendViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()

    // Add files shared via intent
    LaunchedEffect(sharedUris) {
        if (sharedUris.isNotEmpty()) {
            viewModel.addFiles(sharedUris)
        }
    }

    // Navigate when upload completes
    LaunchedEffect(state.transferId, state.encryptionKey) {
        val tid = state.transferId
        val key = state.encryptionKey
        if (tid != null && key != null && !state.isUploading) {
            onTransferCreated(tid, key, "sent")
        }
    }

    val filePicker =
        rememberLauncherForActivityResult(
            contract = ActivityResultContracts.OpenMultipleDocuments()
        ) { uris ->
            if (uris.isNotEmpty()) {
                viewModel.addFiles(uris)
            }
        }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Share Files") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        }
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding).padding(horizontal = 16.dp)) {
            if (state.files.isEmpty()) {
                Column(
                    modifier = Modifier.fillMaxWidth().weight(1f),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                ) {
                    Icon(
                        imageVector = Icons.Default.InsertDriveFile,
                        contentDescription = null,
                        modifier = Modifier.size(64.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(16.dp))
                    Text(
                        text = "No files selected",
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = "Pick files to encrypt and share",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            } else {
                LazyColumn(
                    modifier = Modifier.weight(1f),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    itemsIndexed(state.files) { index, file ->
                        Card(modifier = Modifier.fillMaxWidth()) {
                            Row(
                                modifier = Modifier.fillMaxWidth().padding(12.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Icon(
                                    imageVector = Icons.Default.InsertDriveFile,
                                    contentDescription = null,
                                    modifier = Modifier.size(24.dp),
                                    tint = MaterialTheme.colorScheme.primary,
                                )
                                Spacer(Modifier.width(12.dp))
                                Column(modifier = Modifier.weight(1f)) {
                                    Text(
                                        text = file.name,
                                        style = MaterialTheme.typography.bodyMedium,
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                    )
                                    Text(
                                        text = formatFileSize(file.size),
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                                if (!state.isUploading) {
                                    IconButton(onClick = { viewModel.removeFile(index) }) {
                                        Icon(
                                            Icons.Default.Close,
                                            contentDescription = "Remove",
                                            modifier = Modifier.size(18.dp),
                                        )
                                    }
                                }
                            }
                        }
                    }
                }
            }

            if (state.isUploading) {
                Spacer(Modifier.height(16.dp))
                Text(
                    text =
                        "Encrypting and uploading file ${state.currentFileIndex + 1} of ${state.files.size}...",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(8.dp))
                LinearProgressIndicator(
                    progress = { state.uploadProgress },
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(8.dp))
                Text(
                    text = "${(state.uploadProgress * 100).toInt()}%",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(16.dp))
                OutlinedButton(
                    onClick = { viewModel.cancelUpload() },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("Cancel")
                }
            } else {
                state.error?.let { error ->
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = error,
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }

                if (state.requiresLogin) {
                    Button(onClick = onSignIn, modifier = Modifier.fillMaxWidth()) {
                        Text("Sign in")
                    }
                }
                Spacer(Modifier.height(16.dp))

                OutlinedButton(
                    onClick = { filePicker.launch(arrayOf("*/*")) },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Icon(
                        Icons.Default.Add,
                        contentDescription = null,
                        modifier = Modifier.size(18.dp),
                    )
                    Spacer(Modifier.width(8.dp))
                    Text("Add Files")
                }

                Spacer(Modifier.height(8.dp))

                Button(
                    onClick = { viewModel.startUpload() },
                    enabled = state.files.isNotEmpty(),
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("Encrypt & Upload")
                }
            }

            Spacer(Modifier.height(16.dp))
        }
    }
}

internal fun formatFileSize(bytes: Long): String {
    return when {
        bytes < 1024 -> "$bytes B"
        bytes < 1024 * 1024 -> "${bytes / 1024} KB"
        bytes < 1024 * 1024 * 1024 -> String.format("%.1f MB", bytes / (1024.0 * 1024.0))
        else -> String.format("%.2f GB", bytes / (1024.0 * 1024.0 * 1024.0))
    }
}
