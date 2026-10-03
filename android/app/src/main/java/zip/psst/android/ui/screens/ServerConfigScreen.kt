package zip.psst.android.ui.screens

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Cloud
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
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.PsstApplication
import zip.psst.android.R
import zip.psst.android.ui.components.AppearancePicker
import zip.psst.android.ui.components.loginAutofill
import zip.psst.android.viewmodel.ServerConfigViewModel
import zip.psst.android.viewmodel.TestResult
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions

@Composable
fun ServerConfigScreen(
    onConfigured: () -> Unit,
    onSignedOut: () -> Unit,
    onBack: () -> Unit = {},
    viewModel: ServerConfigViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current
    val prefs = (context.applicationContext as PsstApplication).prefs
    val appearance by prefs.appearance.collectAsStateWithLifecycle()
    var passwordVisible by remember { mutableStateOf(false) }
    val scanner =
        rememberLauncherForActivityResult(ScanContract()) { result ->
            result.contents?.let { viewModel.pair(it, onConfigured) }
        }

    Scaffold { padding ->
        Column(
            modifier =
                Modifier.fillMaxSize()
                    .padding(padding)
                    .verticalScroll(rememberScrollState())
                    .padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            TextButton(onClick = onBack) { Text(stringResource(R.string.back)) }
            Text(stringResource(R.string.app_name), style = MaterialTheme.typography.headlineMedium)
            Icon(
                Icons.Default.Cloud,
                contentDescription = null,
                modifier = Modifier.size(48.dp),
                tint = MaterialTheme.colorScheme.primary,
            )
            Text(stringResource(R.string.settings), style = MaterialTheme.typography.titleLarge)
            AppearancePicker(appearance = appearance, onSelected = prefs::setAppearance)
            Text(
                stringResource(R.string.server_intro),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            OutlinedButton(
                onClick = {
                    scanner.launch(
                        ScanOptions()
                            .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                            .setPrompt(context.getString(R.string.scan_prompt))
                            .setBeepEnabled(false)
                            .setOrientationLocked(false)
                    )
                },
                enabled = !state.isTesting,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(stringResource(R.string.ui_scan_server_login_qr_code))
            }
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
            if (state.signedInUsername != null) {
                Text(
                    stringResource(R.string.signed_in_as, state.signedInUsername.orEmpty()),
                    style = MaterialTheme.typography.bodyLarge,
                )
                Button(
                    onClick = { viewModel.continueSignedIn(onConfigured) },
                    enabled = !state.isTesting,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(stringResource(R.string.ui_continue))
                }
                OutlinedButton(
                    onClick = { viewModel.signOut(onSignedOut) },
                    enabled = !state.isTesting,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(stringResource(R.string.ui_sign_out))
                }
            } else {
                OutlinedTextField(
                    value = state.username,
                    onValueChange = viewModel::onUsernameChange,
                    label = { Text(stringResource(R.string.username)) },
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
                    singleLine = true,
                    enabled = !state.isTesting,
                    modifier =
                        Modifier.fillMaxWidth().loginAutofill(false, viewModel::onUsernameChange),
                )
                OutlinedTextField(
                    value = state.password,
                    onValueChange = viewModel::onPasswordChange,
                    label = { Text(stringResource(R.string.password)) },
                    trailingIcon = {
                        TextButton(onClick = { passwordVisible = !passwordVisible }) {
                            Text(
                                stringResource(
                                    if (passwordVisible) R.string.hide_password
                                    else R.string.show_password
                                )
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
                    modifier =
                        Modifier.fillMaxWidth().loginAutofill(true, viewModel::onPasswordChange),
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
