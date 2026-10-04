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
    var pairing by remember { mutableStateOf<String?>(null) }
    var passwordVisible by remember { mutableStateOf(false) }
    var scanningPairing by remember { mutableStateOf(false) }

    pairing?.let { raw ->
        val server = PairingCode.parse(raw).serverUrl
        AlertDialog(
            onDismissRequest = { pairing = null },
            title = { Text("Set up account?") },
            text = {
                Text(
                    "Connect to $server and replace the current login?" +
                        if (server.startsWith("http://"))
                            " HTTP sends login credentials without transport encryption. Use only on a trusted development network."
                        else ""
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        pairing = null
                        viewModel.pair(raw, onConfigured)
                    }
                ) {
                    Text("Set up account")
                }
            },
            dismissButton = { TextButton(onClick = { pairing = null }) { Text("Cancel") } },
        )
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Server & account") },
                navigationIcon = {
                    IconButton(onClick = onBack, enabled = !state.isTesting) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, "Cancel account changes")
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
            state.notice?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
            if (state.mustChangePassword) {
                Text("Change temporary password", style = MaterialTheme.typography.headlineSmall)
                PasswordEntry(
                    "Temporary password",
                    state.password,
                    viewModel::onPasswordChange,
                    !state.isTesting,
                )
                PasswordEntry(
                    "New password",
                    state.newPassword,
                    viewModel::onNewPasswordChange,
                    !state.isTesting,
                )
                PasswordEntry(
                    "Confirm password",
                    state.confirmPassword,
                    viewModel::onConfirmPasswordChange,
                    !state.isTesting,
                )
                val mismatch =
                    state.confirmPassword.isNotEmpty() && state.newPassword != state.confirmPassword
                if (mismatch)
                    Text("Passwords do not match", color = MaterialTheme.colorScheme.error)
                Button(
                    onClick = viewModel::replacePassword,
                    enabled =
                        !state.isTesting &&
                            !mismatch &&
                            state.password.isNotBlank() &&
                            state.newPassword.isNotBlank() &&
                            state.confirmPassword.isNotBlank(),
                ) {
                    Text("Change password")
                }
                TextButton(
                    onClick = { viewModel.signOut(onAccountCleared) },
                    enabled = !state.isTesting,
                ) {
                    Text("Use another account")
                }
                if (state.isTesting) CircularProgressIndicator()
                (state.testResult as? TestResult.Error)?.let {
                    Text(it.message, color = MaterialTheme.colorScheme.error)
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
                            pairing = raw
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
                            if (passwordVisible) "Hide password" else "Show password",
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
                        result.message,
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
                    if (label == "Temporary password") AutofillType.Password
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
                    if (visible) "Hide password" else "Show password",
                )
            }
        },
    )
}
