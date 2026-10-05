package zip.psst.android.ui.screens

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.*
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.autofill.AutofillType
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.android.ui.components.EmbeddedScanner
import zip.psst.android.ui.components.accountAutofill
import zip.psst.android.ui.components.loginAutofill
import zip.psst.android.viewmodel.ServerConfigViewModel
import zip.psst.android.viewmodel.TestResult
import zip.psst.shared.api.PairingCode

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServerConfigScreen(
    onConfigured: () -> Unit,
    onBack: () -> Unit = {},
    onAccountCleared: () -> Unit = onBack,
    viewModel: ServerConfigViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    BackHandler(enabled = state.isTesting) {}
    val pairing = state.pairingDraft
    var passwordVisible by rememberSaveable { mutableStateOf(false) }
    var scanningPairing by rememberSaveable { mutableStateOf(false) }

    pairing?.let { raw ->
        val server = PairingCode.parse(raw).serverUrl
        AlertDialog(
            onDismissRequest = { viewModel.setPairingDraft(null) },
            title = { Text(tr(R.string.l_set_up_account_a77621)) },
            text = {
                Text(
                    tr(R.string.l_connect_to_1_s_and_replace_the_current_login_8638f9, (server)) +
                        if (server.startsWith("http://"))
                            tr(
                                R.string
                                    .l_http_sends_login_credentials_without_transport_encryption_use_onl_78d775
                            )
                        else ""
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        viewModel.setPairingDraft(null)
                        viewModel.pair(raw, onConfigured)
                    }
                ) {
                    Text(tr(R.string.l_set_up_account_ddd0f7))
                }
            },
            dismissButton = {
                TextButton(onClick = { viewModel.setPairingDraft(null) }) {
                    Text(tr(R.string.l_cancel_77dfd2))
                }
            },
        )
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(tr(R.string.l_server_account_c2fe7f)) },
                navigationIcon = {
                    IconButton(onClick = onBack, enabled = !state.isTesting) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            tr(R.string.l_cancel_account_changes_d12aee),
                        )
                    }
                },
            )
        }
    ) { padding ->
        Column(
            modifier =
                Modifier.fillMaxSize()
                    .padding(padding)
                    .imePadding()
                    .verticalScroll(rememberScrollState())
                    .padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            state.notice?.let { Text(it.text(), style = MaterialTheme.typography.bodyMedium) }
            if (state.mustChangePassword) {
                Text(
                    tr(R.string.l_change_temporary_password_7ee336),
                    style = MaterialTheme.typography.headlineSmall,
                )
                PasswordEntry(
                    tr(R.string.l_temporary_password_62d606),
                    state.password,
                    viewModel::onPasswordChange,
                    !state.isTesting,
                )
                PasswordEntry(
                    tr(R.string.l_new_password_d850ee),
                    state.newPassword,
                    viewModel::onNewPasswordChange,
                    !state.isTesting,
                )
                PasswordEntry(
                    tr(R.string.l_confirm_password_4a7c56),
                    state.confirmPassword,
                    viewModel::onConfirmPasswordChange,
                    !state.isTesting,
                )
                val mismatch =
                    state.confirmPassword.isNotEmpty() && state.newPassword != state.confirmPassword
                if (mismatch)
                    Text(
                        tr(R.string.l_passwords_do_not_match_d69c3b),
                        color = MaterialTheme.colorScheme.error,
                    )
                Button(
                    onClick = viewModel::replacePassword,
                    enabled =
                        !state.isTesting &&
                            !mismatch &&
                            state.password.isNotBlank() &&
                            state.newPassword.isNotBlank() &&
                            state.confirmPassword.isNotBlank(),
                ) {
                    Text(tr(R.string.l_change_password_8c6842))
                }
                TextButton(
                    onClick = { viewModel.signOut(onAccountCleared) },
                    enabled = !state.isTesting,
                ) {
                    Text(tr(R.string.l_use_another_account_48a644))
                }
                if (state.isTesting) CircularProgressIndicator()
                (state.testResult as? TestResult.Error)?.let {
                    Text(it.message.text(), color = MaterialTheme.colorScheme.error)
                }
                return@Column
            }
            Text(
                stringResource(R.string.server_intro),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            OutlinedButton(
                onClick = { scanningPairing = !scanningPairing },
                enabled = !state.isTesting,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(stringResource(R.string.ui_scan_server_login_qr_code))
            }
            if (scanningPairing && !state.isTesting)
                EmbeddedScanner(
                    onCode = { raw ->
                        try {
                            PairingCode.parse(raw)
                            viewModel.setPairingDraft(raw)
                            scanningPairing = false
                        } catch (_: Exception) {
                            viewModel.invalidPairing()
                        }
                    },
                    onError = { viewModel.scanError(it) },
                )
            OutlinedTextField(
                value = state.url,
                onValueChange = viewModel::onUrlChange,
                label = { Text(stringResource(R.string.ui_server_url)) },
                placeholder = { Text(stringResource(R.string.ui_https_transfer_example_com)) },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                singleLine = true,
                enabled = !state.isTesting,
                modifier = Modifier.fillMaxWidth(),
            )
            if (state.url.startsWith("http://", ignoreCase = true)) {
                Text(
                    stringResource(R.string.http_warning),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            OutlinedTextField(
                value = state.username,
                onValueChange = viewModel::onUsernameChange,
                label = { Text(stringResource(R.string.username)) },
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
                singleLine = true,
                enabled = !state.isTesting,
                modifier = Modifier.fillMaxWidth().loginAutofill(false, viewModel::onUsernameChange),
            )
            OutlinedTextField(
                value = state.password,
                onValueChange = viewModel::onPasswordChange,
                label = { Text(stringResource(R.string.password)) },
                trailingIcon = {
                    IconButton(onClick = { passwordVisible = !passwordVisible }) {
                        Icon(
                            if (passwordVisible) Icons.Default.VisibilityOff
                            else Icons.Default.Visibility,
                            if (passwordVisible) tr(R.string.l_hide_password_e40123)
                            else tr(R.string.l_show_password_044b85),
                        )
                    }
                },
                visualTransformation =
                    if (passwordVisible) VisualTransformation.None
                    else PasswordVisualTransformation(),
                keyboardOptions =
                    KeyboardOptions(
                        keyboardType = KeyboardType.Password,
                        imeAction = ImeAction.Done,
                    ),
                keyboardActions =
                    KeyboardActions(
                        onDone = {
                            if (
                                !state.isTesting &&
                                    state.username.isNotBlank() &&
                                    state.password.isNotBlank()
                            )
                                viewModel.signIn(onConfigured)
                        }
                    ),
                singleLine = true,
                enabled = !state.isTesting,
                modifier = Modifier.fillMaxWidth().loginAutofill(true, viewModel::onPasswordChange),
            )
            Button(
                onClick = { viewModel.signIn(onConfigured) },
                enabled =
                    !state.isTesting &&
                        state.url.isNotBlank() &&
                        state.username.isNotBlank() &&
                        state.password.isNotBlank(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(stringResource(R.string.ui_sign_in_continue))
            }
            OutlinedButton(
                onClick = { viewModel.testConnection() },
                enabled = !state.isTesting && state.url.isNotBlank(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(stringResource(R.string.ui_test_connection))
            }
            if (state.isTesting) CircularProgressIndicator(modifier = Modifier.size(24.dp))
            when (val result = state.testResult) {
                is TestResult.Success ->
                    Text(
                        stringResource(R.string.ui_connection_successful),
                        color = MaterialTheme.colorScheme.primary,
                    )
                is TestResult.Error ->
                    Text(
                        result.message.text(),
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                    )
                null -> {}
            }
            Spacer(Modifier.height(12.dp))
        }
    }
}

@OptIn(ExperimentalComposeUiApi::class)
@Composable
private fun PasswordEntry(
    label: String,
    value: String,
    onChange: (String) -> Unit,
    enabled: Boolean,
) {
    var visible by remember { mutableStateOf(false) }
    OutlinedTextField(
        value = value,
        onValueChange = onChange,
        label = { Text(label) },
        singleLine = true,
        enabled = enabled,
        modifier =
            Modifier.fillMaxWidth()
                .accountAutofill(
                    if (label == tr(R.string.l_temporary_password_62d606)) AutofillType.Password
                    else AutofillType.NewPassword,
                    onChange,
                ),
        visualTransformation =
            if (visible) VisualTransformation.None else PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
        trailingIcon = {
            IconButton(onClick = { visible = !visible }) {
                Icon(
                    if (visible) Icons.Default.VisibilityOff else Icons.Default.Visibility,
                    if (visible) tr(R.string.l_hide_password_e40123)
                    else tr(R.string.l_show_password_044b85),
                )
            }
        },
    )
}
