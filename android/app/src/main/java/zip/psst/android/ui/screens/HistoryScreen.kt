package zip.psst.android.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
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
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.R
import zip.psst.android.data.*
import zip.psst.android.data.TransferHistoryEntity
import zip.psst.android.data.historyStatusLabel
import zip.psst.android.i18n.*
import zip.psst.android.ui.components.AccountIndicator
import zip.psst.android.viewmodel.HistoryViewModel
import zip.psst.android.viewmodel.ScanViewModel
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
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
    val loadedHistory by viewModel.history.collectAsState()
    val currentAccess by viewModel.currentAccess.collectAsState()
    val pageState by viewModel.pageState.collectAsState()
    val allHistory =
        if (pageState.access == currentAccess) loadedHistory.filter(currentAccess::permits)
        else emptyList()
    val deviceHistory by viewModel.deviceHistory.collectAsState()
    val legacyCount by viewModel.legacyCount.collectAsState()
    val offline by viewModel.offline.collectAsState()
    val accountIssue by viewModel.accountIssue.collectAsState()
    val transferIssue by viewModel.transferIssue.collectAsState()
    val filter by viewModel.filter.collectAsState()
    val mergedRows by viewModel.deviceRows.collectAsState()
    val deviceHydratedAccess by viewModel.deviceHydratedAccess.collectAsState()
    val mergedNext by viewModel.deviceNext.collectAsState()
    val mergedLoading by viewModel.deviceLoading.collectAsState()
    val mergedIssue by viewModel.deviceIssue.collectAsState()
    val importing by viewModel.deviceImporting.collectAsState()
    val importErrors by viewModel.deviceImportErrors.collectAsState()
    val downloads by guest.state.collectAsState()
    val guestPage by guest.historyPage.collectAsState()
    val history =
        if (deviceHistory) {
            if (deviceHydratedAccess == currentAccess) mergedRows else emptyList()
        } else unifiedHistory(allHistory, emptyList(), "all")
    val listState = remember(deviceHistory, filter) { LazyListState() }
    val scope = rememberCoroutineScope()
    var sourceMenu by remember { mutableStateOf(false) }
    var filterMenu by remember { mutableStateOf(false) }
    var removal by remember { mutableStateOf<GuestDownload?>(null) }
    removal?.let { record ->
        AlertDialog(
            onDismissRequest = { removal = null },
            title = { Text(tr(R.string.l_remove_from_history_592783)) },
            text = {
                Text(
                    tr(
                        R.string
                            .l_saved_files_remain_on_this_device_the_sender_s_link_will_not_be_r_24b096
                    )
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        guest.remove(record)
                        viewModel.refreshDeviceHistory()
                        removal = null
                    }
                ) {
                    Text(tr(R.string.l_remove_from_history_5c3f6c))
                }
            },
            dismissButton = {
                TextButton(onClick = { removal = null }) { Text(tr(R.string.l_cancel_77dfd2)) }
            },
        )
    }

    var renaming by remember { mutableStateOf<TransferHistoryEntity?>(null) }
    var name by remember { mutableStateOf("") }
    renaming?.let { entry ->
        AlertDialog(
            onDismissRequest = { renaming = null },
            title = { Text(tr(R.string.l_rename_link_8e2e05)) },
            text = {
                Column {
                    Text(
                        tr(
                            R.string
                                .l_shown_to_people_using_this_link_clear_it_to_remove_the_shared_tit_a05b59
                        )
                    )
                    OutlinedTextField(
                        value = name,
                        onValueChange = { name = it },
                        label = { Text(tr(R.string.l_name_709a23)) },
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
                    Text(tr(R.string.l_save_efc007))
                }
            },
            dismissButton = {
                TextButton(onClick = { renaming = null }) { Text(tr(R.string.l_cancel_77dfd2)) }
            },
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
            text = { Text(error.message.text()) },
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
    LaunchedEffect(initialFilter) {
        viewModel.setFilter(initialFilter)
        if (initialFilter == "downloaded") viewModel.setDeviceHistory(true)
    }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle, viewModel) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_RESUME -> {
                    viewModel.refresh()
                    guest.refreshHistory()
                }
                Lifecycle.Event.ON_PAUSE,
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
                    TextButton(
                        onClick = {
                            viewModel.refreshNewest()
                            scope.launch { listState.scrollToItem(0) }
                        }
                    ) {
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
            FlowRow(
                Modifier.fillMaxWidth().padding(horizontal = 16.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Box {
                    OutlinedButton(onClick = { sourceMenu = true }) {
                        Text(
                            if (deviceHistory) tr(R.string.l_on_this_device_a7f962)
                            else tr(R.string.l_server_history_075bb5)
                        )
                    }
                    DropdownMenu(expanded = sourceMenu, onDismissRequest = { sourceMenu = false }) {
                        DropdownMenuItem(
                            text = { Text(tr(R.string.l_server_history_075bb5)) },
                            onClick = {
                                sourceMenu = false
                                viewModel.setDeviceHistory(false)
                                if (filter == "downloaded") viewModel.setFilter("all")
                            },
                        )
                        DropdownMenuItem(
                            text = { Text(tr(R.string.l_on_this_device_a7f962)) },
                            onClick = {
                                sourceMenu = false
                                viewModel.setDeviceHistory(true)
                            },
                        )
                    }
                }
                Box {
                    val filters =
                        listOf(
                            "all" to tr(R.string.l_all_6a7208),
                            "sent" to tr(R.string.l_sent_35f49d),
                            "received" to tr(R.string.l_receive_links_48d9c2),
                            "downloaded" to tr(R.string.l_downloaded_c61970),
                        )
                    OutlinedButton(onClick = { filterMenu = true }) {
                        Text(
                            tr(
                                R.string.ui_filter_value,
                                filters.first { it.first == filter }.second,
                            )
                        )
                    }
                    DropdownMenu(expanded = filterMenu, onDismissRequest = { filterMenu = false }) {
                        filters.forEach { (value, label) ->
                            DropdownMenuItem(
                                text = { Text(label) },
                                onClick = {
                                    filterMenu = false
                                    viewModel.setFilter(value)
                                },
                            )
                        }
                    }
                }
            }
            mergedIssue?.let {
                Text(it.text(), Modifier.padding(16.dp))
                TextButton(onClick = viewModel::refreshDeviceHistory) {
                    Text(tr(R.string.l_retry_9f5cd8))
                }
            }
            if (deviceHistory && importing)
                TextButton(onClick = viewModel::refreshDeviceHistory, enabled = !mergedLoading) {
                    Text(tr(R.string.l_continue_importing_older_downloads_02798b))
                }
            if (deviceHistory && importErrors)
                Text(
                    tr(
                        R.string
                            .l_some_older_metadata_needs_recovery_original_records_keys_and_save_0124c2
                    ),
                    Modifier.padding(16.dp),
                )
            if (deviceHistory && legacyCount > 0)
                Text(
                    tr(
                        R.string
                            .l_pre_account_records_are_retained_manage_pre_account_server_resour_d509f6
                    ),
                    Modifier.padding(horizontal = 16.dp),
                    style = MaterialTheme.typography.bodySmall,
                )
            if (downloads.pendingReceipts > 0 && !downloads.busy)
                TextButton(onClick = guest::retryAllReceipts) {
                    Text(tr(R.string.l_retry_next_pending_receipts_765a85))
                }
            downloads.error?.let {
                Text(
                    it.text(),
                    Modifier.padding(horizontal = 16.dp),
                    color = MaterialTheme.colorScheme.error,
                )
            }
            transferIssue?.let {
                Text(it.text(), Modifier.padding(16.dp), color = MaterialTheme.colorScheme.error)
            }
            accountIssue?.let { message ->
                Text(
                    message.text(),
                    Modifier.padding(16.dp),
                    color = MaterialTheme.colorScheme.error,
                )
                TextButton(onClick = onAccount) {
                    Text(tr(R.string.l_open_account_settings_3ac9c2))
                }
            }
            if (offline)
                Text(
                    stringResource(R.string.offline_retained),
                    Modifier.padding(16.dp),
                    style = MaterialTheme.typography.bodySmall,
                )
            val knownEmpty =
                if (deviceHistory) deviceHydratedAccess == currentAccess
                else pageState.isKnownEmptyFor(currentAccess)
            if (history.isEmpty() && knownEmpty) {
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
                    if (deviceHistory && mergedNext != null)
                        TextButton(
                            onClick = viewModel::moreDeviceHistory,
                            enabled = !mergedLoading,
                        ) {
                            Text(tr(R.string.l_load_more_dfe60c))
                        }
                    if (!deviceHistory && pageState.page?.nextCursor != null)
                        TextButton(onClick = viewModel::nextPage, enabled = !pageState.loading) {
                            Text(tr(R.string.l_next_bc9819))
                        }
                    if (!deviceHistory && pageState.pager.previous.isNotEmpty())
                        TextButton(
                            onClick = viewModel::previousPage,
                            enabled = !pageState.loading,
                        ) {
                            Text(tr(R.string.l_previous_50f942))
                        }
                }
            } else {
                LazyColumn(
                    state = listState,
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
                                        name = row.value.sharedTitle ?: row.value.title.orEmpty()
                                    },
                                    isDeleting = row.value.id in deletingIds,
                                    canManage = row.value.accountId != null,
                                    serverSize =
                                        if (deviceHistory) null
                                        else
                                            pageState.page?.let { page ->
                                                page.transfers
                                                    .firstOrNull { it.id == row.value.id }
                                                    ?.totalSize
                                                    ?: page.slots
                                                        .firstOrNull { it.id == row.value.id }
                                                        ?.totalSize
                                            },
                                    serverCount =
                                        if (deviceHistory) null
                                        else
                                            pageState.page?.let { page ->
                                                page.transfers
                                                    .firstOrNull { it.id == row.value.id }
                                                    ?.fileCount
                                                    ?: page.slots
                                                        .firstOrNull { it.id == row.value.id }
                                                        ?.completedFiles
                                            },
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
                    item {
                        if (deviceHistory && mergedNext != null)
                            TextButton(
                                onClick = viewModel::moreDeviceHistory,
                                enabled = !mergedLoading,
                            ) {
                                Text(tr(R.string.l_load_more_dfe60c))
                            }
                        if (
                            !deviceHistory &&
                                (pageState.pager.previous.isNotEmpty() ||
                                    pageState.page?.nextCursor != null)
                        ) {
                            Row {
                                if (pageState.pager.cursor != null)
                                    TextButton(
                                        onClick = viewModel::firstPage,
                                        enabled = !pageState.loading,
                                    ) {
                                        Text(tr(R.string.l_first_916a78))
                                    }
                                if (pageState.pager.previous.isNotEmpty())
                                    TextButton(
                                        onClick = viewModel::previousPage,
                                        enabled = !pageState.loading,
                                    ) {
                                        Text(tr(R.string.l_previous_50f942))
                                    }
                                if (pageState.page?.nextCursor != null)
                                    TextButton(
                                        onClick = viewModel::nextPage,
                                        enabled = !pageState.loading,
                                    ) {
                                        Text(tr(R.string.l_next_bc9819))
                                    }
                            }
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
    canManage: Boolean = true,
    serverCount: Long? = null,
    serverSize: Long? = null,
) {
    val revokingLabel = stringResource(R.string.ui_revoking_link)
    Card(onClick = { if (canManage) onClick() }, modifier = Modifier.fillMaxWidth()) {
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
                    if (entity.type == "sent") tr(R.string.l_sent_35f49d)
                    else tr(R.string.l_receive_link_ef4dc0),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.primary,
                )
                val fullTitle = historyTitle(entity).text()
                Text(
                    text = compactHistoryTitle(fullTitle),
                    modifier = Modifier.semantics { contentDescription = fullTitle },
                    style = MaterialTheme.typography.titleSmall,
                )
                val fileCount = serverCount ?: entity.fileCount.toLong()
                val fileCountLabel =
                    pluralStringResource(
                        R.plurals.file_count_label,
                        fileCount.coerceIn(0, Int.MAX_VALUE.toLong()).toInt(),
                        fileCount,
                    )
                Text(
                    text =
                        (if (entity.summaryUpdating)
                            plural(
                                R.plurals.counts_last_known,
                                entity.fileCount.toLong(),
                                entity.fileCount,
                            )
                        else "${formatTimestamp(entity.createdAt)} · $fileCountLabel") +
                            if (serverSize != null) {
                                tr(R.string.l_1_s_encrypted_fc9e99, (formatFileSize(serverSize)))
                            } else if (entity.totalSize > 0) {
                                " - ${formatFileSize(entity.totalSize)}"
                            } else {
                                ""
                            },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (!canManage)
                    Text(
                        tr(
                            R.string
                                .l_pre_account_record_manage_server_links_from_the_administrator_web_042324
                        ),
                        style = MaterialTheme.typography.bodySmall,
                    )
                if (entity.encryptionKey.isBlank())
                    Text(
                        stringResource(R.string.key_on_other_device),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                if (entity.status !in listOf("exhausted", "unavailable", "expired", "revoked")) {
                    entity.expiresAt?.let { expiresAt ->
                        Text(
                            relativeExpiry(expiresAt),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                Text(
                    text = historyStatusLabel(entity.type, entity.status).text(),
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

            if (canManage)
                Column {
                    IconButton(onClick = onRename, enabled = !isDeleting) {
                        Icon(
                            Icons.Default.Edit,
                            tr(R.string.l_rename_link_8e2e05),
                            Modifier.size(20.dp),
                        )
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

private fun formatTimestamp(millis: Long): String =
    UiFormatting.timestamp(millis, UiStrings.context().resources.configuration.locales[0])

@Composable
private fun relativeExpiry(time: Long): String {
    val minutes = (time - System.currentTimeMillis()) / 60000
    return when {
        minutes <= 0 -> stringResource(R.string.expired)
        minutes < 60 -> plural(R.plurals.expiry_minutes, (minutes), (minutes))
        minutes < 1440 -> plural(R.plurals.expiry_hours, (minutes / 60), (minutes / 60))
        else -> plural(R.plurals.expiry_days, (minutes / 1440), (minutes / 1440))
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
                val fullTitle =
                    record.sharedTitle?.let(::userText)
                        ?: automaticHistoryTitle(
                            record.files.firstOrNull()?.name,
                            record.files.size,
                        )
                        ?: message(R.string.l_downloaded_transfer_3b8513)
                Text(
                    compactHistoryTitle(fullTitle.text()),
                    modifier = Modifier.semantics { contentDescription = fullTitle.text() },
                    style = MaterialTheme.typography.titleSmall,
                )
                Text(
                    plural(
                        R.plurals.files_available,
                        record.files.size.toLong(),
                        available,
                        record.files.size,
                    ),
                    style = MaterialTheme.typography.bodySmall,
                )
                Text(
                    if (record.complete && available == record.files.size)
                        tr(R.string.l_saved_on_this_device_87222e)
                    else if (record.complete) tr(R.string.l_local_copies_missing_c9fb77)
                    else tr(R.string.l_partial_interrupted_0677f4),
                    style = MaterialTheme.typography.bodySmall,
                )
                Text(record.origin, style = MaterialTheme.typography.bodySmall)
                Text(formatTimestamp(record.createdAt), style = MaterialTheme.typography.bodySmall)
                if (record.receiptPending)
                    Text(
                        tr(R.string.l_delivery_receipt_pending_4f30b8),
                        style = MaterialTheme.typography.bodySmall,
                    )
            }
            IconButton(onClick = onRemove) {
                Icon(Icons.Default.Delete, tr(R.string.l_remove_download_from_history_595e31))
            }
        }
    }
}
