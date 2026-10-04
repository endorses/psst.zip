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
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.History
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
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
import zip.psst.android.data.*
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.historyStatusLabel
import zip.psst.android.ui.components.AccountIndicator
import zip.psst.android.viewmodel.HistoryViewModel
import zip.psst.android.viewmodel.ScanViewModel
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HistoryScreen(
    onTransferClick: (TransferHistoryEntity) -> Unit,
    onBack: () -> Unit,
    onAccount: () -> Unit,
    onDownloadClick: (GuestDownload) -> Unit,
    guest: ScanViewModel,
    initialFilter: String = "all",
    viewModel: HistoryViewModel = viewModel(),
) {
    val allHistory by viewModel.history.collectAsState()
    val offline by viewModel.offline.collectAsState()
    val accountIssue by viewModel.accountIssue.collectAsState()
    var filter by remember { mutableStateOf(initialFilter) }
    val downloads by guest.state.collectAsState()
    val history = unifiedHistory(allHistory, downloads.history, filter)
    var removal by remember { mutableStateOf<GuestDownload?>(null) }
    removal?.let { record ->
        AlertDialog(
            onDismissRequest = { removal = null },
            title = { Text("Remove from history?") },
            text = {
                Text("Saved files remain on this device. The sender’s link will not be revoked.")
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        guest.remove(record)
                        removal = null
                    }
                ) {
                    Text("Remove from history")
                }
            },
            dismissButton = { TextButton(onClick = { removal = null }) { Text("Cancel") } },
        )
    }

    var renaming by remember { mutableStateOf<TransferHistoryEntity?>(null) }
    var name by remember { mutableStateOf("") }
    renaming?.let { entry ->
        AlertDialog(
            onDismissRequest = { renaming = null },
            title = { Text("Rename on this device") },
            text = {
                Column {
                    Text(
                        "This name is stored only on this device. Clear it to restore the automatic title."
                    )
                    OutlinedTextField(
                        value = name,
                        onValueChange = { name = it.take(200) },
                        label = { Text("Name") },
                        singleLine = true,
                    )
                }
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        viewModel.rename(entry, name)
                        renaming = null
                    }
                ) {
                    Text("Save")
                }
            },
            dismissButton = { TextButton(onClick = { renaming = null }) { Text("Cancel") } },
        )
    }

    val deletingIds by viewModel.deletingIds.collectAsState()
    val deletionError by viewModel.deletionError.collectAsState()
    var confirmDeletion by remember { mutableStateOf<TransferHistoryEntity?>(null) }

    LaunchedEffect(allHistory) {
        if (
            renaming != null &&
                allHistory.none {
                    it.id == renaming?.id &&
                        it.accountId == renaming?.accountId &&
                        it.serverUrl == renaming?.serverUrl
                }
        )
            renaming = null
        if (
            confirmDeletion != null &&
                allHistory.none {
                    it.id == confirmDeletion?.id &&
                        it.accountId == confirmDeletion?.accountId &&
                        it.serverUrl == confirmDeletion?.serverUrl
                }
        )
            confirmDeletion = null
    }
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
                Lifecycle.Event.ON_RESUME -> {
                    viewModel.refresh()
                    guest.refreshHistory()
                }
                Lifecycle.Event.ON_STOP -> viewModel.stopRefreshing()
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        if (lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)) {
            viewModel.refresh()
            guest.refreshHistory()
        }
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
                        "all" to "All",
                        "sent" to "Sent",
                        "received" to "Receive links",
                        "downloaded" to "Downloaded",
                    )
                    .forEach { (value, label) ->
                        FilterChip(
                            selected = filter == value,
                            onClick = { filter = value },
                            label = { Text(label) },
                        )
                    }
            }
            if (downloads.pendingReceipts > 0 && !downloads.busy)
                TextButton(onClick = guest::retryAllReceipts) {
                    Text("Retry pending receipts (${downloads.pendingReceipts})")
                }
            downloads.error?.let {
                Text(
                    it,
                    Modifier.padding(horizontal = 16.dp),
                    color = MaterialTheme.colorScheme.error,
                )
            }
            accountIssue?.let { message ->
                Text(message, Modifier.padding(16.dp), color = MaterialTheme.colorScheme.error)
                TextButton(onClick = onAccount) { Text("Open account settings") }
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
                    items(history, key = { it.key }) { row ->
                        when (row) {
                            is HistoryRow.Owned ->
                                HistoryItem(
                                    entity = row.value,
                                    onClick = { onTransferClick(row.value) },
                                    onDelete = { confirmDeletion = row.value },
                                    onRename = {
                                        renaming = row.value
                                        name = row.value.title.orEmpty()
                                    },
                                    isDeleting = row.value.id in deletingIds,
                                )
                            is HistoryRow.Downloaded ->
                                DownloadHistoryItem(
                                    row.value,
                                    available = row.value.saved.count(guest::fileExists),
                                    onClick = { onDownloadClick(row.value) },
                                    onRemove = { removal = row.value },
                                )
                        }
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
    onRename: () -> Unit,
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
                    if (entity.type == "sent") "Sent" else "Receive link",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.primary,
                )
                val fullTitle = historyTitle(entity)
                Text(
                    text = compactHistoryTitle(fullTitle),
                    modifier = Modifier.semantics { contentDescription = fullTitle },
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

            Column {
                IconButton(onClick = onRename, enabled = !isDeleting) {
                    Icon(Icons.Default.Edit, "Rename on this device", Modifier.size(20.dp))
                }
                IconButton(onClick = onDelete, enabled = !isDeleting) {
                    if (isDeleting) {
                        CircularProgressIndicator(
                            modifier =
                                Modifier.size(20.dp).semantics {
                                    contentDescription = revokingLabel
                                },
                            strokeWidth = 2.dp,
                        )
                    } else {
                        Icon(
                            Icons.Default.Delete,
                            contentDescription =
                                stringResource(R.string.ui_revoke_link_and_delete_2),
                            modifier = Modifier.size(20.dp),
                            tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
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

@Composable
private fun DownloadHistoryItem(
    record: GuestDownload,
    available: Int,
    onClick: () -> Unit,
    onRemove: () -> Unit,
) {
    Card(onClick = onClick, modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(
                    compactHistoryTitle(
                        automaticHistoryTitle(record.files.firstOrNull()?.name, record.files.size)
                            ?: "Downloaded transfer"
                    ),
                    modifier =
                        Modifier.semantics {
                            contentDescription =
                                automaticHistoryTitle(
                                    record.files.firstOrNull()?.name,
                                    record.files.size,
                                ) ?: "Downloaded transfer"
                        },
                    style = MaterialTheme.typography.titleSmall,
                )
                Text(
                    "Downloaded · $available of ${record.files.size} files available",
                    style = MaterialTheme.typography.bodySmall,
                )
                Text(
                    if (record.complete && available == record.files.size) "Saved on this device"
                    else if (record.complete) "Local copies missing" else "Partial / interrupted",
                    style = MaterialTheme.typography.bodySmall,
                )
                Text(record.origin, style = MaterialTheme.typography.bodySmall)
                Text(formatTimestamp(record.createdAt), style = MaterialTheme.typography.bodySmall)
                if (record.receiptPending)
                    Text("Delivery receipt pending", style = MaterialTheme.typography.bodySmall)
            }
            IconButton(onClick = onRemove) {
                Icon(Icons.Default.Delete, "Remove download from history")
            }
        }
    }
}
