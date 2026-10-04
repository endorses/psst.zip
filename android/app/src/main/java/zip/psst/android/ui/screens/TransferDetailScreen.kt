package zip.psst.android.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.R
import zip.psst.android.data.historyStatusLabel
import zip.psst.android.ui.components.AccountIndicator
import zip.psst.android.ui.components.ExpiryCountdown
import zip.psst.android.ui.components.LinkPanel
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
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle, transferId) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) viewModel.load(transferId, encryptionKey, type)
            if (event == Lifecycle.Event.ON_STOP) viewModel.stopRefreshing()
        }
        lifecycle.addObserver(observer)
        if (lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED))
            viewModel.load(transferId, encryptionKey, type)
        onDispose {
            lifecycle.removeObserver(observer)
            viewModel.stopRefreshing()
        }
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.send_files)) },
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
            if (state.isLoading) CircularProgressIndicator()
            else {
                Text(
                    historyStatusLabel(state.type, state.status),
                    style = MaterialTheme.typography.titleMedium,
                )
                if (state.shareUrl.isNotBlank())
                    LinkPanel(state.shareUrl) {
                        Text(
                            stringResource(
                                R.string.selected_files,
                                state.fileCount,
                                formatFileSize(state.totalSize),
                            ),
                            style = MaterialTheme.typography.bodySmall,
                        )
                        ExpiryCountdown(state.expiresAt)
                    }
                if (state.maxDownloads > 0)
                    Text(
                        "Limit: ${state.maxDownloads} download attempts per file. ${state.exhaustedFiles} of ${state.fileCount} files have no attempts remaining. Interrupted downloads and retries count."
                    )
                state.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                if (state.offline) Text(stringResource(R.string.offline_retained))
                TextButton(onClick = { viewModel.load(transferId, encryptionKey, type) }) {
                    Text(stringResource(R.string.refresh))
                }
            }
        }
    }
}
