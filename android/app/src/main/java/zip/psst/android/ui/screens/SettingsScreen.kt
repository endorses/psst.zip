package zip.psst.android.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import zip.psst.android.PsstApplication
import zip.psst.android.ui.components.AppearancePicker
import zip.psst.android.viewmodel.ServerConfigViewModel
import zip.psst.android.viewmodel.TestResult

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    onBack: () -> Unit,
    onAccount: () -> Unit,
    onSignedOut: () -> Unit,
    viewModel: ServerConfigViewModel = viewModel(),
) {
    val prefs = (LocalContext.current.applicationContext as PsstApplication).prefs
    val appearance by prefs.appearance.collectAsState()
    val access by prefs.historyAccess.collectAsState()
    val state by viewModel.uiState.collectAsState()
    LaunchedEffect(access) { viewModel.refreshSavedSession() }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Settings") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back")
                    }
                },
            )
        }
    ) { padding ->
        Column(
            Modifier.fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text("Appearance", style = MaterialTheme.typography.titleSmall)
            AppearancePicker(appearance, prefs::setAppearance)
            HorizontalDivider()
            Text("Server & account", style = MaterialTheme.typography.titleSmall)
            Text(
                if (access.accountId != null) "Signed in as ${prefs.getUsername()}"
                else "Not signed in"
            )
            if (prefs.getServerUrl().isNotBlank())
                Text(prefs.getServerUrl(), style = MaterialTheme.typography.bodyMedium)
            OutlinedButton(onClick = onAccount, enabled = !state.isTesting) {
                Text(
                    if (access.accountId != null) "Change server or account"
                    else "Sign in to a server"
                )
            }
            HorizontalDivider()
            Text("Connection", style = MaterialTheme.typography.titleSmall)
            Text(
                when {
                    prefs.getServerUrl().isBlank() -> "No server configured"
                    prefs.getServerUrl().startsWith("https://") -> "HTTPS · encrypted connection"
                    else -> "HTTP · unencrypted connection"
                }
            )
            TextButton(
                onClick = viewModel::testConnection,
                enabled = !state.isTesting && prefs.getServerUrl().isNotBlank(),
            ) {
                Text("Test connection")
            }
            if (state.isTesting) LinearProgressIndicator(Modifier.fillMaxWidth())
            when (val result = state.testResult) {
                is TestResult.Success -> Text("Connection successful")
                is TestResult.Error -> Text(result.message, color = MaterialTheme.colorScheme.error)
                null -> Unit
            }
            if (access.accountId != null) {
                HorizontalDivider()
                TextButton(
                    onClick = { viewModel.signOut(onSignedOut) },
                    enabled = !state.isTesting,
                ) {
                    Text("Sign out", color = MaterialTheme.colorScheme.error)
                }
            }
        }
    }
}
