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
import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.android.ui.components.AbuseReportButton
import zip.psst.android.ui.components.AppearancePicker
import zip.psst.android.ui.components.LanguagePicker
import zip.psst.android.viewmodel.ServerConfigViewModel
import zip.psst.android.viewmodel.TestResult
import zip.psst.shared.model.AbuseReportReference

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    onBack: () -> Unit,
    onAccount: () -> Unit,
    onSignedOut: () -> Unit,
    onUsage: () -> Unit,
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
                title = { Text(tr(R.string.l_settings_c7f73b)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, tr(R.string.l_back_b52b36))
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
            Text(tr(R.string.l_appearance_41def7), style = MaterialTheme.typography.titleSmall)
            AppearancePicker(appearance, prefs::setAppearance)
            LanguagePicker()
            HorizontalDivider()
            Text(tr(R.string.l_server_account_c2fe7f), style = MaterialTheme.typography.titleSmall)
            Text(
                if (access.accountId != null)
                    tr(R.string.l_signed_in_as_1_s_aff3e2, (prefs.getUsername()))
                else tr(R.string.l_not_signed_in_848d80)
            )
            if (prefs.getServerUrl().isNotBlank())
                Text(prefs.getServerUrl(), style = MaterialTheme.typography.bodyMedium)
            OutlinedButton(onClick = onAccount, enabled = !state.isTesting) {
                Text(
                    if (access.accountId != null) tr(R.string.l_change_server_or_account_d2bed2)
                    else tr(R.string.l_sign_in_to_a_server_3c5b36)
                )
            }
            if (access.accountId != null && !access.isAdmin) {
                HorizontalDivider()
                TextButton(onClick = onUsage) {
                    Text(
                        tr(R.string.l_usage_traffic_b83c32) +
                            (traffic.snapshot
                                ?.takeIf { it.policy.enforcementEnabled }
                                ?.let {
                                    tr(
                                        R.string.l_1_s_remaining_28d5ba,
                                        (displayBytes(it.usage.remainingBytes)),
                                    )
                                } ?: "")
                    )
                }
            }
            HorizontalDivider()
            Text(tr(R.string.l_connection_6512ee), style = MaterialTheme.typography.titleSmall)
            AbuseReportButton(AbuseReportReference.create(prefs.getServerUrl()))
            Text(
                when {
                    prefs.getServerUrl().isBlank() -> tr(R.string.l_no_server_configured_a38920)
                    prefs.getServerUrl().startsWith("https://") ->
                        tr(R.string.l_https_encrypted_connection_b789f2)
                    else -> tr(R.string.l_http_unencrypted_connection_7ad9ab)
                }
            )
            TextButton(
                onClick = viewModel::testConnection,
                enabled = !state.isTesting && prefs.getServerUrl().isNotBlank(),
            ) {
                Text(tr(R.string.l_test_connection_ccf66f))
            }
            if (state.isTesting) LinearProgressIndicator(Modifier.fillMaxWidth())
            when (val result = state.testResult) {
                is TestResult.Success -> Text(tr(R.string.l_connection_successful_2d2106))
                is TestResult.Error ->
                    Text(result.message.text(), color = MaterialTheme.colorScheme.error)
                null -> Unit
            }
            HorizontalDivider()
            zip.psst.android.ui.components.SourceLicenses(prefs.getServerUrl())
            if (access.accountId != null) {
                HorizontalDivider()
                TextButton(
                    onClick = { viewModel.signOut(onSignedOut) },
                    enabled = !state.isTesting,
                ) {
                    Text(tr(R.string.l_sign_out_dc1649), color = MaterialTheme.colorScheme.error)
                }
            }
        }
    }
}
