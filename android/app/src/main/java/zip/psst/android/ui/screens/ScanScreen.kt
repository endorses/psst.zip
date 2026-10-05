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
import androidx.compose.runtime.saveable.rememberSaveable
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
import zip.psst.android.i18n.*
import zip.psst.android.i18n.ScanStage
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

    var secondary by rememberSaveable { mutableStateOf(false) }
    val paste = state.inputDraft
    var pairingConfirmation by rememberSaveable { mutableStateOf(false) }

    var redownload by rememberSaveable { mutableStateOf(false) }
    var stopConfirmation by rememberSaveable { mutableStateOf(false) }
    val storage =
        rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
            if (granted) viewModel.receive(redownload)
            else
                viewModel.error(
                    message(
                        R.string
                            .l_storage_access_is_required_to_save_in_downloads_psst_zip_on_this__32debb
                    )
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
            viewModel.setInputDraft("")
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
    fun leave() {
        account.cancelPendingOperation()
        viewModel.cancel()
        onBack()
    }
    fun back() {
        if (
            state.busy &&
                state.stage in
                    listOf(
                        ScanStage.DOWNLOADING,
                        ScanStage.DECRYPTING,
                        ScanStage.SAVING,
                        ScanStage.PREPARING,
                        ScanStage.ENCRYPTING,
                        ScanStage.UPLOADING,
                    )
        )
            stopConfirmation = true
        else leave()
    }
    BackHandler { back() }
    fun openFile(file: SavedGuestFile, share: Boolean) {
        if (!viewModel.fileExists(file)) {
            viewModel.error(
                message(
                    R.string
                        .l_this_saved_file_is_missing_you_can_explicitly_redownload_it_while_845b63
                )
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
                if (share) Intent.createChooser(intent, tr(R.string.l_share_saved_file_81dfc4))
                else intent
            )
        } catch (_: Exception) {
            viewModel.error(
                message(
                    R.string
                        .l_no_app_could_open_this_file_try_share_or_find_it_in_downloads_pss_8bf749
                )
            )
        }
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        if (state.kind == ScanInputKind.UPLOAD) tr(R.string.l_send_files_ea4b35)
                        else if (historical) tr(R.string.l_downloaded_files_a9609f)
                        else tr(R.string.l_scan_qr_code_e7d8c3)
                    )
                },
                actions = {
                    if (state.kind != null)
                        TextButton(onClick = { secondary = true }) {
                            Text(tr(R.string.l_more_4bab2d))
                        }
                },
                navigationIcon = {
                    IconButton(onClick = ::back) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, tr(R.string.l_back_b52b36))
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
                    Text(tr(R.string.l_retry_next_pending_delivery_receipts_4692e8))
                }
            }
            if (state.pendingCleanup > 0 && !state.busy && !accountState.isTesting) {
                Text(
                    tr(
                        R.string
                            .l_interrupted_uploads_need_server_cleanup_each_retry_processes_a_bo_0d10d5
                    )
                )
                TextButton(onClick = viewModel::retryCleanup) {
                    Text(tr(R.string.l_retry_upload_cleanup_532a80))
                }
            }
            state.notice?.let { Text(it.text(), color = MaterialTheme.colorScheme.primary) }
            if (state.error != null)
                Text(state.error!!.text(), color = MaterialTheme.colorScheme.error)
            if (state.kind == null) AbuseReportButton(state.reportReference)
            run {
                if (state.origin.isNotBlank())
                    Text(state.origin, style = MaterialTheme.typography.titleMedium)
                if (state.busy) {
                    Text(state.stage.label(), style = MaterialTheme.typography.headlineSmall)
                    Text(
                        if (state.record?.files?.isNotEmpty() == true)
                            tr(
                                R.string.ui_file_progress,
                                state.fileIndex.coerceAtLeast(1),
                                state.record!!.files.size,
                            )
                        else
                            tr(R.string.l_file_1_s_2_s_62e558, state.fileIndex.coerceAtLeast(1), "")
                    )
                    if (
                        state.stage == ScanStage.DOWNLOADING || state.stage == ScanStage.UPLOADING
                    ) {
                        Text(
                            tr(
                                R.string.l_1_s_2_s_bytes_975e83,
                                (state.bytes),
                                (state.totalBytes ?: "?"),
                            )
                        )
                        LinearProgressIndicator(
                            progress = {
                                (state.bytes.toFloat() / (state.totalBytes ?: 1).coerceAtLeast(1))
                                    .coerceIn(0f, 1f)
                            },
                            modifier = Modifier.fillMaxWidth(),
                        )
                    } else LinearProgressIndicator(Modifier.fillMaxWidth())
                    Text(
                        tr(
                            R.string
                                .l_keep_this_app_in_the_foreground_leaving_pauses_receiving_saved_fi_e2c7a1
                        ),
                        style = MaterialTheme.typography.bodySmall,
                    )
                    OutlinedButton(onClick = viewModel::cancel) {
                        Text(tr(R.string.l_cancel_77dfd2))
                    }
                } else
                    when (state.kind) {
                        ScanInputKind.DOWNLOAD -> {
                            val record = state.record
                            val availability = viewModel.downloadAvailability()
                            Text(
                                if (record?.complete == true && !viewModel.missingFiles())
                                    plural(
                                        R.plurals.files_saved_short,
                                        record.saved.size.toLong(),
                                        record.saved.size,
                                    )
                                else if (record?.complete == true)
                                    tr(R.string.l_some_local_files_are_missing_5b465c)
                                else
                                    state.stage.label().ifBlank {
                                        tr(R.string.l_ready_to_receive_b4814d)
                                    },
                                style = MaterialTheme.typography.headlineSmall,
                            )
                            record?.sharedTitle?.let {
                                Text(it.text(), style = MaterialTheme.typography.titleMedium)
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
                                                0L -> tr(R.string.l_download_limit_reached_8745b6)
                                                null -> tr(R.string.l_availability_unknown_692251)
                                                else ->
                                                    plural(
                                                        R.plurals.attempts_remaining,
                                                        attempts,
                                                        attempts,
                                                    )
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
                                                Text(tr(R.string.l_open_cf9b77))
                                            }
                                            TextButton(onClick = { openFile(file, true) }) {
                                                Text(tr(R.string.l_share_09ca55))
                                            }
                                        }
                                    }
                                }
                            }
                            if (availability?.allMissingExhausted == true)
                                Text(
                                    tr(
                                        R.string
                                            .l_download_limit_reached_saved_local_copies_can_still_be_opened_or__04af1b
                                    )
                                )
                            else if (availability?.partiallyExhausted == true)
                                Text(
                                    plural(
                                        R.plurals.files_exhausted_missing,
                                        availability.exhausted.toLong(),
                                        availability.exhausted,
                                    )
                                )
                            val canDownload =
                                availability?.allMissingExhausted != true &&
                                    !state.refreshingAvailability
                            if (viewModel.missingFiles())
                                Button(onClick = { receive(true) }, enabled = canDownload) {
                                    Text(
                                        if (availability?.partiallyExhausted == true)
                                            tr(R.string.l_redownload_available_missing_files_4f0301)
                                        else tr(R.string.l_redownload_missing_files_677f43)
                                    )
                                }
                            else if (record?.complete != true)
                                Button(onClick = { receive() }, enabled = canDownload) {
                                    Text(
                                        if (availability?.partiallyExhausted == true)
                                            tr(R.string.l_download_available_files_1fc1f3)
                                        else tr(R.string.l_resume_receiving_bcb430)
                                    )
                                }
                            if (record?.files?.isNotEmpty() == true)
                                TextButton(
                                    onClick = viewModel::refreshDownloadAvailability,
                                    enabled = !state.refreshingAvailability,
                                ) {
                                    Text(
                                        if (state.refreshingAvailability)
                                            tr(R.string.l_checking_availability_24f241)
                                        else tr(R.string.l_refresh_availability_812724)
                                    )
                                }
                            if (record?.receiptPending == true) {
                                Text(
                                    tr(
                                        R.string
                                            .l_files_are_saved_the_sender_s_delivery_receipt_is_pending_8c3671
                                    )
                                )
                                TextButton(onClick = viewModel::retryReceipt) {
                                    Text(tr(R.string.l_retry_receipt_78de8a))
                                }
                            }
                        }
                        ScanInputKind.UPLOAD -> {
                            Text(
                                if (state.uploaded) tr(R.string.l_files_sent_564c79)
                                else tr(R.string.l_send_to_this_receive_link_c3894e),
                                style = MaterialTheme.typography.headlineSmall,
                            )
                            if (!state.uploaded) {
                                Text(
                                    tr(
                                        R.string
                                            .l_choose_files_deliberately_to_send_to_the_server_above_its_file_li_bd9395
                                    )
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
                                        state.remainingUploadFiles?.let {
                                            plural(R.plurals.allocations_remaining, it, it)
                                        } ?: tr(R.string.ui_unknown_files_remaining)
                                    )
                                Text(state.uploadCapacityMessage.text())
                                state.uploadFiles.forEach { uri ->
                                    Row(verticalAlignment = Alignment.CenterVertically) {
                                        Text(
                                            viewModel.uploadName(uri),
                                            modifier = Modifier.weight(1f),
                                        )
                                        TextButton(onClick = { viewModel.removeUpload(uri) }) {
                                            Text(tr(R.string.l_remove_e96390))
                                        }
                                    }
                                }
                                TextButton(onClick = viewModel::refreshUploadPolicy) {
                                    Text(tr(R.string.l_refresh_capacity_61f820))
                                }
                                OutlinedButton(onClick = { picker.launch(arrayOf("*/*")) }) {
                                    Text(tr(R.string.l_add_files_c06342))
                                }
                                Button(
                                    onClick = viewModel::upload,
                                    enabled = state.uploadFiles.isNotEmpty(),
                                ) {
                                    Text(tr(R.string.l_send_files_ea4b35))
                                }
                            }
                        }
                        ScanInputKind.PAIRING -> {
                            Text(
                                tr(R.string.l_set_up_this_account_6308a7),
                                style = MaterialTheme.typography.headlineSmall,
                            )
                            Text(
                                tr(
                                    R.string
                                        .l_this_replaces_your_current_app_login_only_after_you_confirm_dc01cc
                                )
                            )
                            (accountState.testResult as? TestResult.Error)?.let {
                                Text(it.message.text(), color = MaterialTheme.colorScheme.error)
                            }
                            if (accountState.isTesting) CircularProgressIndicator()
                            else
                                Button(onClick = { pairingConfirmation = true }) {
                                    Text(tr(R.string.l_set_up_account_ddd0f7))
                                }
                        }
                        null -> {
                            Text(
                                tr(R.string.l_receive_encrypted_files_without_signing_in_fb4a07),
                                style = MaterialTheme.typography.titleMedium,
                            )
                            if (!historical)
                                EmbeddedScanner(onCode = ::accept, onError = viewModel::error)
                            OutlinedTextField(
                                value = paste,
                                onValueChange = viewModel::setInputDraft,
                                label = { Text(tr(R.string.l_paste_link_035922)) },
                                modifier = Modifier.fillMaxWidth(),
                                minLines = 2,
                                maxLines = 4,
                            )
                            Button(
                                onClick = { accept(paste) },
                                enabled = paste.isNotBlank(),
                                modifier = Modifier.fillMaxWidth(),
                            ) {
                                Text(tr(R.string.l_receive_files_bf1734))
                            }
                            Text(
                                tr(
                                    R.string
                                        .l_transfers_up_to_100_mib_start_automatically_larger_transfers_ask__621d73
                                ),
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
            title = { Text(tr(R.string.l_transfer_options_c9f040)) },
            text = {
                Column {
                    Text(state.origin)
                    AbuseReportButton(state.reportReference)
                    if (state.pendingReceipts > 0)
                        TextButton(onClick = viewModel::retryAllReceipts, enabled = !state.busy) {
                            Text(tr(R.string.l_retry_receipts_bc4f30))
                        }
                    if (state.pendingCleanup > 0)
                        TextButton(onClick = viewModel::retryCleanup, enabled = !state.busy) {
                            Text(tr(R.string.l_retry_upload_cleanup_532a80))
                        }
                    if (!historical)
                        TextButton(
                            onClick = {
                                viewModel.clear()
                                secondary = false
                            },
                            enabled = !state.busy,
                        ) {
                            Text(tr(R.string.l_scan_again_f6ab55))
                        }
                    TextButton(
                        onClick = {
                            secondary = false
                            onHistory()
                        },
                        enabled = !state.busy,
                    ) {
                        Text(tr(R.string.l_history_90ccd6))
                    }
                }
            },
            confirmButton = {
                TextButton(onClick = { secondary = false }) { Text(tr(R.string.l_close_bbfa77)) }
            },
        )
    state.downloadConsent
        ?.takeIf { !state.busy }
        ?.let { consent ->
            AlertDialog(
                onDismissRequest = viewModel::dismissDownloadConsent,
                title = { Text(tr(R.string.l_download_these_files_f0f150)) },
                text = {
                    Text(
                        plural(
                            R.plurals.download_consent_origin,
                            consent.files.size.toLong(),
                            consent.files.size,
                            consent.origin,
                        ) +
                            "\n\n" +
                            tr(R.string.l_total_1_s_n_e4c487, (displayBytes(consent.totalBytes))) +
                            tr(
                                R.string.l_still_to_save_1_s_n_n_28ca8e,
                                (displayBytes(consent.remainingBytes)),
                            ) +
                            (if (consent.totalBytes > 100L * 1024 * 1024)
                                tr(
                                    R.string
                                        .l_this_transfer_exceeds_100_mib_and_may_use_mobile_data_c67f5a
                                )
                            else "") +
                            (if (consent.skippedBlobIds.isNotEmpty())
                                plural(
                                    R.plurals.files_skipped,
                                    consent.skippedBlobIds.size.toLong(),
                                    consent.skippedBlobIds.size,
                                    consent.files
                                        .filter { it.blobId in consent.skippedBlobIds }
                                        .joinToString(", ") {
                                            compactHistoryTitle(receivedFilenameLabel(it.name), 48)
                                        },
                                ) + "\n\n"
                            else "") +
                            tr(R.string.l_downloads_keep_at_least_256_mib_of_storage_free_167089),
                        modifier =
                            Modifier.heightIn(max = 280.dp).verticalScroll(rememberScrollState()),
                    )
                },
                confirmButton = {
                    TextButton(onClick = viewModel::confirmDownload) {
                        Text(tr(R.string.l_download_a479c9))
                    }
                },
                dismissButton = {
                    TextButton(onClick = viewModel::dismissDownloadConsent) {
                        Text(tr(R.string.l_cancel_77dfd2))
                    }
                },
            )
        }
    if (stopConfirmation)
        AlertDialog(
            onDismissRequest = { stopConfirmation = false },
            title = { Text(tr(R.string.l_stop_transfer_73baee)) },
            text = {
                Text(
                    tr(
                        R.string
                            .l_files_already_saved_stay_in_downloads_psst_zip_receiving_can_be_r_93f4a0
                    )
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        stopConfirmation = false
                        leave()
                    }
                ) {
                    Text(tr(R.string.l_stop_transfer_d0b705))
                }
            },
            dismissButton = {
                TextButton(onClick = { stopConfirmation = false }) {
                    Text(tr(R.string.l_keep_transferring_08f378))
                }
            },
        )
    if (pairingConfirmation)
        AlertDialog(
            onDismissRequest = { pairingConfirmation = false },
            title = { Text(tr(R.string.l_set_up_account_a77621)) },
            text = {
                Text(
                    tr(
                        R.string
                            .l_connect_to_1_s_and_replace_your_current_login_your_local_received_295f40,
                        (state.origin),
                    )
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
                    Text(tr(R.string.l_set_up_account_ddd0f7))
                }
            },
            dismissButton = {
                TextButton(onClick = { pairingConfirmation = false }) {
                    Text(tr(R.string.l_cancel_77dfd2))
                }
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
            state.sharedTitle ?: tr(R.string.l_send_to_this_receive_link_c3894e),
            style = MaterialTheme.typography.headlineSmall,
        )
        Text(state.origin, style = MaterialTheme.typography.bodySmall)
        if (state.origin.startsWith("http://"))
            Text(
                tr(R.string.l_http_unencrypted_connection_7ad9ab),
                color = MaterialTheme.colorScheme.error,
            )
        Text(
            pluralStringResource(
                R.plurals.files_selected_count,
                state.uploadFiles.size,
                state.uploadFiles.size,
            ) +
                (state.remainingUploadFiles?.let {
                    " · " + plural(R.plurals.files_remaining, it, it)
                } ?: "")
        )
        LazyColumn(
            Modifier.weight(1f).fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            item {
                state.error?.let { Text(it.text(), color = MaterialTheme.colorScheme.error) }
                state.notice?.let { Text(it.text()) }
            }
            items(state.uploadFiles, key = { it.toString() }) { uri ->
                Card {
                    Row(
                        Modifier.fillMaxWidth().padding(12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(viewModel.uploadName(uri), Modifier.weight(1f))
                        if (!state.busy)
                            TextButton(onClick = { viewModel.removeUpload(uri) }) {
                                Text(tr(R.string.l_remove_e96390))
                            }
                    }
                }
            }
            item {
                Text(state.uploadCapacityMessage.text(), style = MaterialTheme.typography.bodySmall)
            }
            if (!state.busy && !state.uploaded)
                item {
                    TextButton(onClick = viewModel::refreshUploadPolicy) {
                        Text(tr(R.string.l_refresh_capacity_61f820))
                    }
                }
        }
        if (state.busy) {
            Text(state.stage.label())
            LinearProgressIndicator(Modifier.fillMaxWidth())
            TextButton(onClick = viewModel::cancel) { Text(tr(R.string.l_cancel_77dfd2)) }
        } else if (state.uploaded)
            Text(tr(R.string.l_files_sent_564c79), style = MaterialTheme.typography.titleMedium)
        else {
            OutlinedButton(onClick = addFiles, modifier = Modifier.fillMaxWidth()) {
                Text(tr(R.string.l_add_files_c06342))
            }
            Button(
                onClick = viewModel::upload,
                enabled = state.uploadFiles.isNotEmpty(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(tr(R.string.l_send_files_ea4b35))
            }
        }
    }
}
