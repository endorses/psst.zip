package zip.psst.android.ui.screens

import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.History
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.R
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.historyStatusLabel
import zip.psst.android.ui.components.AccountIndicator
import zip.psst.android.viewmodel.HistoryViewModel
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HistoryScreen(
    onTransferClick: (TransferHistoryEntity) -> Unit,
    onBack: () -> Unit,
    onLocalReceived: () -> Unit = {},
    viewModel: HistoryViewModel = viewModel(),
) {
    val allHistory by viewModel.history.collectAsState()
    val offline by viewModel.offline.collectAsState()
    var filter by remember { mutableStateOf("all") }
    val history = allHistory.filter { filter == "all" || (filter == "sent") == (it.type == "sent") }
    val deletingIds by viewModel.deletingIds.collectAsState()
    val deletionError by viewModel.deletionError.collectAsState()
    var confirmDeletion by remember { mutableStateOf<TransferHistoryEntity?>(null) }

    confirmDeletion?.let { entry ->
        AlertDialog(
            onDismissRequest = { confirmDeletion = null },
            title = { Text(stringResource(R.string.ui_revoke_link_and_delete)) },
            text = {
                Text(
                    if (entry.type == "received")
                        stringResource(R.string.revoke_receive_explanation)
                    else stringResource(R.string.revoke_sent_explanation)
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        confirmDeletion = null
                        viewModel.delete(entry.id)
                    }
                ) {
                    Text(stringResource(R.string.ui_revoke_and_delete))
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmDeletion = null }) {
                    Text(stringResource(R.string.cancel))
                }
            },
        )
    }
    deletionError?.let { error ->
        AlertDialog(
            onDismissRequest = viewModel::dismissDeletionError,
            title = { Text(stringResource(R.string.ui_link_could_not_be_revoked)) },
            text = { Text(error.message) },
            confirmButton = {
                TextButton(onClick = { viewModel.delete(error.id) }) {
                    Text(stringResource(R.string.retry))
                }
            },
            dismissButton = {
                TextButton(onClick = viewModel::dismissDeletionError) {
                    Text(stringResource(R.string.ui_keep_entry))
                }
            },
        )
    }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle, viewModel) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_RESUME -> viewModel.refresh()
                Lifecycle.Event.ON_STOP -> viewModel.stopRefreshing()
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        if (lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)) viewModel.refresh()
        onDispose {
            lifecycle.removeObserver(observer)
            viewModel.stopRefreshing()
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.history)) },
                actions = {
                    TextButton(onClick = onLocalReceived) { Text("Received") }
                    TextButton(onClick = viewModel::refresh) {
                        Text(stringResource(R.string.refresh))
                    }
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.back),
                        )
                    }
                },
            )
        }
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            AccountIndicator(Modifier.padding(horizontal = 16.dp))
            Row(
                Modifier.fillMaxWidth()
                    .horizontalScroll(rememberScrollState())
                    .padding(horizontal = 16.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                listOf(
                        "all" to R.string.all,
                        "sent" to R.string.sent,
                        "received" to R.string.receive_links,
                    )
                    .forEach { (value, label) ->
                        FilterChip(
                            selected = filter == value,
                            onClick = { filter = value },
                            label = { Text(stringResource(label)) },
                        )
                    }
            }
            if (offline)
                Text(
                    stringResource(R.string.offline_retained),
                    Modifier.padding(16.dp),
                    style = MaterialTheme.typography.bodySmall,
                )
            if (history.isEmpty()) {
                Column(
                    modifier = Modifier.fillMaxSize(),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                ) {
                    Icon(
                        imageVector = Icons.Default.History,
                        contentDescription = null,
                        modifier = Modifier.size(64.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(16.dp))
                    Text(
                        text =
                            stringResource(
                                if (filter == "all") R.string.no_transfers
                                else R.string.no_filtered_transfers
                            ),
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            } else {
                LazyColumn(
                    modifier = Modifier.fillMaxSize().padding(horizontal = 16.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    items(history, key = { it.id }) { entity ->
                        HistoryItem(
                            entity = entity,
                            onClick = { onTransferClick(entity) },
                            onDelete = { confirmDeletion = entity },
                            isDeleting = entity.id in deletingIds,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun HistoryItem(
    entity: TransferHistoryEntity,
    onClick: () -> Unit,
    onDelete: () -> Unit,
    isDeleting: Boolean,
) {
    val revokingLabel = stringResource(R.string.ui_revoking_link)
    Card(onClick = onClick, modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                imageVector =
                    if (entity.type == "sent") {
                        Icons.AutoMirrored.Filled.Send
                    } else {
                        Icons.Default.Download
                    },
                contentDescription = null,
                modifier = Modifier.size(24.dp),
                tint = MaterialTheme.colorScheme.primary,
            )

            Spacer(Modifier.width(12.dp))

            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text =
                        entity.title
                            ?: stringResource(
                                if (entity.type == "sent") R.string.sent else R.string.receive_link
                            ),
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.titleSmall,
                )
                Text(
                    text =
                        "${entity.fileCount} file(s)" +
                            if (entity.totalSize > 0) {
                                " - ${formatFileSize(entity.totalSize)}"
                            } else {
                                ""
                            },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (entity.encryptionKey.isBlank())
                    Text(
                        stringResource(R.string.key_on_other_device),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                entity.expiresAt?.let { expiresAt ->
                    Text(
                        relativeExpiry(expiresAt),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    text = formatTimestamp(entity.createdAt),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = historyStatusLabel(entity.type, entity.status),
                    style = MaterialTheme.typography.labelSmall,
                    color =
                        when (entity.status) {
                            "complete",
                            "downloaded",
                            "has_uploads" -> MaterialTheme.colorScheme.primary
                            "expired" -> MaterialTheme.colorScheme.error
                            else -> MaterialTheme.colorScheme.onSurfaceVariant
                        },
                )
            }

            Spacer(Modifier.width(8.dp))

            IconButton(onClick = onDelete, enabled = !isDeleting) {
                if (isDeleting) {
                    CircularProgressIndicator(
                        modifier =
                            Modifier.size(20.dp).semantics { contentDescription = revokingLabel },
                        strokeWidth = 2.dp,
                    )
                } else {
                    Icon(
                        Icons.Default.Delete,
                        contentDescription = stringResource(R.string.ui_revoke_link_and_delete_2),
                        modifier = Modifier.size(20.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

private fun formatTimestamp(millis: Long): String {
    val formatter = SimpleDateFormat("MMM d, yyyy HH:mm", Locale.getDefault())
    return formatter.format(Date(millis))
}

@Composable
private fun relativeExpiry(time: Long): String {
    val minutes = (time - System.currentTimeMillis()) / 60000
    return when {
        minutes <= 0 -> stringResource(R.string.expired)
        minutes < 60 -> stringResource(R.string.expires_minutes, minutes)
        minutes < 1440 -> stringResource(R.string.expires_hours, minutes / 60)
        else -> stringResource(R.string.expires_days, minutes / 1440)
    }
}
