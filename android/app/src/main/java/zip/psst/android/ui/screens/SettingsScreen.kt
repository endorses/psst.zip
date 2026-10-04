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
import zip.psst.android.ui.components.AbuseReportButton
import zip.psst.android.ui.components.AppearancePicker
import zip.psst.android.viewmodel.ServerConfigViewModel
import zip.psst.android.viewmodel.TestResult
import zip.psst.shared.model.AbuseReportReference

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    onBack: () -> Unit,
    onAccount: () -> Unit,
    onSignedOut: () -> Unit,
    viewModel: ServerConfigViewModel = viewModel(),
    trafficViewModel: zip.psst.android.viewmodel.TrafficUsageViewModel = viewModel(),
) {
    val prefs = (LocalContext.current.applicationContext as PsstApplication).prefs
    val appearance by prefs.appearance.collectAsState()
    val access by prefs.historyAccess.collectAsState()
    val state by viewModel.uiState.collectAsState()
    val traffic by trafficViewModel.state.collectAsState()
    LaunchedEffect(access) {
        viewModel.refreshSavedSession()
        trafficViewModel.refresh()
    }
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
            if (access.accountId != null) {
                HorizontalDivider()
                Text("Transfer traffic", style = MaterialTheme.typography.titleSmall)
                if (traffic.loading) LinearProgressIndicator(Modifier.fillMaxWidth())
                traffic.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                traffic.snapshot?.let { snapshot ->
                    Text(
                        if (snapshot.policy.enforcementEnabled) "Traffic budget enforced"
                        else "Traffic budget enforcement off"
                    )
                    Text(
                        if (snapshot.policy.basis == "outbound") "Counts downloads"
                        else "Counts uploads and downloads"
                    )
                    Text(
                        "Effective account budget: ${android.text.format.Formatter.formatFileSize(LocalContext.current, snapshot.usage.budgetBytes)}"
                    )
                    Text(
                        "Charged or reserved: ${android.text.format.Formatter.formatFileSize(LocalContext.current, snapshot.usage.chargedBytes)}"
                    )
                    if (snapshot.state == "unavailable")
                        Text(
                            "Traffic accounting is unavailable. Retry later or contact the administrator."
                        )
                    else {
                        Text(
                            "Remaining: ${android.text.format.Formatter.formatFileSize(LocalContext.current, snapshot.usage.remainingBytes)}"
                        )
                        if (snapshot.state == "exhausted")
                            Text(
                                "Traffic budget reached. Retry after the cycle resets or contact the administrator."
                            )
                    }
                    Text(
                        "Cycle resets ${snapshot.cycle.end} (UTC)",
                        style = MaterialTheme.typography.bodySmall,
                    )
                    Text(
                        "Application payload only, not a provider bill. Other traffic and concurrent transfers can change availability.",
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                TextButton(onClick = trafficViewModel::refresh, enabled = !traffic.loading) {
                    Text("Refresh traffic status")
                }
            }
            HorizontalDivider()
            Text("Connection", style = MaterialTheme.typography.titleSmall)
            AbuseReportButton(AbuseReportReference.create(prefs.getServerUrl()))
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
