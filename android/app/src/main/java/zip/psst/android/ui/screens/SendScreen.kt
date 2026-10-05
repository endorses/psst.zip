package zip.psst.android.ui.screens

import android.content.Intent
import android.net.Uri
import androidx.activity.compose.BackHandler
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.InsertDriveFile
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.android.ui.components.AccountIndicator
import zip.psst.android.ui.components.LinkLimits
import zip.psst.android.viewmodel.SendViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SendScreen(
    sharedUris: List<Uri>,
    onSharedUrisConsumed: () -> Unit = {},
    onTransferCreated: (transferId: String, encryptionKey: String, type: String) -> Unit,
    onBack: () -> Unit,
    onSignIn: () -> Unit,
    viewModel: SendViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current
    var settings by rememberSaveable { mutableStateOf(false) }
    var confirmStop by rememberSaveable { mutableStateOf(false) }
    var leaveAfterStop by rememberSaveable { mutableStateOf(false) }
    val requestBack = {
        if (state.isUploading) {
            leaveAfterStop = true
            confirmStop = true
        } else onBack()
    }
    BackHandler(state.isUploading) { requestBack() }
    if (confirmStop)
        AlertDialog(
            onDismissRequest = { confirmStop = false },
            title = { Text(stringResource(R.string.stop_upload)) },
            text = { Text(stringResource(R.string.stop_upload_explanation)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        confirmStop = false
                        viewModel.cancelUpload()
                        if (leaveAfterStop) onBack()
                    }
                ) {
                    Text(stringResource(R.string.stop))
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmStop = false }) {
                    Text(stringResource(R.string.keep_uploading))
                }
            },
        )

    if (settings)
        AlertDialog(
            onDismissRequest = { settings = false },
            title = { Text(tr(R.string.l_link_settings_14b504)) },
            text = {
                Column(
                    Modifier.verticalScroll(rememberScrollState()),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    OutlinedTextField(
                        value = state.sharedTitle,
                        onValueChange = viewModel::setSharedTitle,
                        label = { Text(tr(R.string.l_link_title_optional_fc0c15)) },
                        supportingText = {
                            Text(tr(R.string.l_shown_to_people_using_this_link_1c969c))
                        },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    LinkLimits(
                        enabled = state.downloadLimitEnabled,
                        value = state.maxDownloadsInput,
                        editable = !state.linkPolicyLocked,
                        label = tr(R.string.l_maximum_downloads_per_file_657845),
                        help =
                            tr(
                                R.string
                                    .l_each_started_download_counts_including_interrupted_downloads_2be8c5
                            ),
                        onEnabledChange = viewModel::setDownloadLimitEnabled,
                        onValueChange = viewModel::setMaxDownloads,
                    )
                }
            },
            confirmButton = {
                TextButton(onClick = { settings = false }) { Text(tr(R.string.l_done_e9b450)) }
            },
        )

    LaunchedEffect(Unit) { viewModel.refreshLimit() }

    // Add files shared via intent
    LaunchedEffect(sharedUris, state.isUploading) {
        if (sharedUris.isNotEmpty() && !state.isUploading) {
            viewModel.addFiles(sharedUris)
            onSharedUrisConsumed()
        }
    }

    // Consume before navigating: returning to a retained screen cannot replay completion.
    LaunchedEffect(state.transferId, state.encryptionKey, state.isUploading) {
        viewModel.consumeCompletion()?.let { (id, key) -> onTransferCreated(id, key, "sent") }
    }

    val filePicker =
        rememberLauncherForActivityResult(
            contract = ActivityResultContracts.OpenMultipleDocuments()
        ) { uris ->
            if (uris.isNotEmpty()) {
                uris.forEach {
                    runCatching {
                        context.contentResolver.takePersistableUriPermission(
                            it,
                            Intent.FLAG_GRANT_READ_URI_PERMISSION,
                        )
                    }
                }
                viewModel.addFiles(uris)
            }
        }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.send_files)) },
                navigationIcon = {
                    IconButton(onClick = requestBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.back),
                        )
                    }
                },
            )
        }
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding).padding(horizontal = 16.dp)) {
            AccountIndicator()
            if (!state.isUploading)
                TextButton(onClick = { settings = true }) {
                    Text(
                        if (state.sharedTitle.isBlank() && !state.downloadLimitEnabled)
                            tr(R.string.l_link_settings_title_and_limits_2e28cf)
                        else tr(R.string.l_link_settings_edit_title_or_limits_7e5458)
                    )
                }
            Text(
                state.maxFileBytes?.let {
                    tr(
                        R.string.l_up_to_1_s_per_file_files_are_encrypted_automatically_7b19e3,
                        (formatFileSize(it)),
                    )
                } ?: tr(R.string.l_the_server_s_file_limit_is_checked_before_uploading_5b14b0),
                style = MaterialTheme.typography.bodySmall,
            )
            if (state.files.isNotEmpty())
                Text(
                    pluralStringResource(
                        R.plurals.selected_file_summary,
                        state.files.size,
                        state.files.size,
                        formatFileSize(state.files.sumOf { it.size }),
                    ),
                    style = MaterialTheme.typography.bodyMedium,
                )
            if (state.files.isEmpty()) {
                Column(
                    modifier =
                        Modifier.fillMaxWidth().weight(1f).verticalScroll(rememberScrollState()),
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
                        text = stringResource(R.string.ui_no_files_selected),
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text =
                            stringResource(
                                R.string.ui_choose_files_to_share_with_a_link_or_qr_code
                            ),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            } else {
                LazyColumn(
                    modifier = Modifier.fillMaxWidth().weight(1f),
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
                                            contentDescription = stringResource(R.string.ui_remove),
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
                        if (state.isPreparing) stringResource(R.string.preparing_files)
                        else
                            stringResource(
                                R.string.uploading_file,
                                state.files.getOrNull(state.currentFileIndex)?.name.orEmpty(),
                            ),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(8.dp))
                if (state.isPreparing) LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                else
                    LinearProgressIndicator(
                        progress = { state.uploadProgress },
                        modifier = Modifier.fillMaxWidth(),
                    )
                Spacer(Modifier.height(8.dp))
                Text(
                    text =
                        stringResource(
                            R.string.upload_bytes,
                            formatFileSize(state.uploadedBytes),
                            formatFileSize(state.totalUploadBytes),
                        ),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(16.dp))
                OutlinedButton(
                    onClick = {
                        leaveAfterStop = false
                        confirmStop = true
                    },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(stringResource(R.string.cancel))
                }
            } else {
                state.error?.let { error ->
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = error.text(),
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }

                if (state.requiresLogin) {
                    Button(onClick = onSignIn, modifier = Modifier.fillMaxWidth()) {
                        Text(stringResource(R.string.sign_in))
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
                    Text(stringResource(R.string.ui_add_files))
                }

                Spacer(Modifier.height(8.dp))

                Button(
                    onClick = { viewModel.startUpload() },
                    enabled =
                        state.files.isNotEmpty() &&
                            zip.psst.android.data.linkLimitError(
                                state.downloadLimitEnabled,
                                state.maxDownloadsInput,
                            ) == null,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(stringResource(R.string.send_files))
                }
            }

            Spacer(Modifier.height(16.dp))
        }
    }
}

internal fun formatFileSize(bytes: Long): String =
    UiFormatting.bytes(bytes, UiStrings.context().resources.configuration.locales[0])
