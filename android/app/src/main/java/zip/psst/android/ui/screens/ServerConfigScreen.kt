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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.viewmodel.ServerConfigViewModel
import zip.psst.android.viewmodel.TestResult
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions

@Composable
fun ServerConfigScreen(
    onConfigured: () -> Unit,
    onSignedOut: () -> Unit,
    viewModel: ServerConfigViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
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
            Icon(
                Icons.Default.Cloud,
                contentDescription = null,
                modifier = Modifier.size(48.dp),
                tint = MaterialTheme.colorScheme.primary,
            )
            Text("Server Configuration", style = MaterialTheme.typography.headlineMedium)
            Text(
                "Sign in to your self-hosted server to share files and create receive links.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            OutlinedButton(
                onClick = {
                    scanner.launch(
                        ScanOptions()
                            .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                            .setPrompt("Scan the login code from Connect mobile app in the web UI")
                            .setBeepEnabled(false)
                            .setOrientationLocked(false)
                    )
                },
                enabled = !state.isTesting,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("Scan server login QR code")
            }
            OutlinedTextField(
                value = state.url,
                onValueChange = viewModel::onUrlChange,
                label = { Text("Server URL") },
                placeholder = { Text("https://psst.example.com") },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                singleLine = true,
                enabled = !state.isTesting,
                modifier = Modifier.fillMaxWidth(),
            )
            if (state.url.startsWith("http://", ignoreCase = true)) {
                Text(
                    "HTTP sends login credentials without transport encryption. Use it only for local development on a trusted network; the server must explicitly allow it.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            if (state.signedInUsername != null) {
                Text(
                    "Signed in as ${state.signedInUsername}",
                    style = MaterialTheme.typography.bodyLarge,
                )
                Button(
                    onClick = { viewModel.continueSignedIn(onConfigured) },
                    enabled = !state.isTesting,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("Continue")
                }
                OutlinedButton(
                    onClick = { viewModel.signOut(onSignedOut) },
                    enabled = !state.isTesting,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("Sign out")
                }
            } else {
                OutlinedTextField(
                    value = state.username,
                    onValueChange = viewModel::onUsernameChange,
                    label = { Text("Username") },
                    singleLine = true,
                    enabled = !state.isTesting,
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = state.password,
                    onValueChange = viewModel::onPasswordChange,
                    label = { Text("Password") },
                    visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    singleLine = true,
                    enabled = !state.isTesting,
                    modifier = Modifier.fillMaxWidth(),
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
                    Text("Sign in & Continue")
                }
            }
            OutlinedButton(
                onClick = { viewModel.testConnection() },
                enabled = !state.isTesting && state.url.isNotBlank(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("Test Connection")
            }
            if (state.isTesting) CircularProgressIndicator(modifier = Modifier.size(24.dp))
            when (val result = state.testResult) {
                is TestResult.Success ->
                    Text("Connection successful", color = MaterialTheme.colorScheme.primary)
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
