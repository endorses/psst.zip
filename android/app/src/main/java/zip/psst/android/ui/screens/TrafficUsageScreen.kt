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
import zip.psst.android.viewmodel.TrafficUsageViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TrafficUsageScreen(onBack: () -> Unit, trafficViewModel: TrafficUsageViewModel = viewModel()) {
    val app = LocalContext.current.applicationContext as PsstApplication
    val access by app.prefs.historyAccess.collectAsState()
    val traffic by trafficViewModel.state.collectAsState()
    LaunchedEffect(access) { trafficViewModel.refresh() }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Usage / Traffic") },
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
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (access.accountId == null || access.isAdmin || access.mustChangePassword)
                Text("Sign in with a regular account to view its usage.")
            else {
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
        }
    }
}
