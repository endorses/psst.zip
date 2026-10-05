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
                title = { Text(tr(R.string.l_usage_traffic_b83c32)) },
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
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (access.accountId == null || access.isAdmin || access.mustChangePassword)
                Text(tr(R.string.l_sign_in_with_a_regular_account_to_view_its_usage_ced9e8))
            else {
                Text(
                    tr(R.string.l_transfer_traffic_fb6ebc),
                    style = MaterialTheme.typography.titleSmall,
                )
                if (traffic.loading) LinearProgressIndicator(Modifier.fillMaxWidth())
                traffic.error?.let { Text(it.text(), color = MaterialTheme.colorScheme.error) }
                traffic.snapshot?.let { snapshot ->
                    Text(
                        if (snapshot.policy.enforcementEnabled)
                            tr(R.string.l_traffic_budget_enforced_7e95f8)
                        else tr(R.string.l_traffic_budget_enforcement_off_ea381f)
                    )
                    Text(
                        if (snapshot.policy.basis == "outbound")
                            tr(R.string.l_counts_downloads_038552)
                        else tr(R.string.l_counts_uploads_and_downloads_178969)
                    )
                    Text(
                        tr(
                            R.string.l_effective_account_budget_1_s_94c6c1,
                            (displayBytes(snapshot.usage.budgetBytes)),
                        )
                    )
                    Text(
                        tr(
                            R.string.l_charged_or_reserved_1_s_6b8b4a,
                            (displayBytes(snapshot.usage.chargedBytes)),
                        )
                    )
                    if (snapshot.state == "unavailable")
                        Text(
                            tr(
                                R.string
                                    .l_traffic_accounting_is_unavailable_retry_later_or_contact_the_admi_74c56e
                            )
                        )
                    else {
                        Text(
                            tr(
                                R.string.l_remaining_1_s_72b007,
                                (displayBytes(snapshot.usage.remainingBytes)),
                            )
                        )
                        if (snapshot.state == "exhausted")
                            Text(
                                tr(
                                    R.string
                                        .l_traffic_budget_reached_retry_after_the_cycle_resets_or_contact_th_c3263b
                                )
                            )
                    }
                    Text(
                        tr(R.string.l_cycle_resets_1_s_utc_af2c16, (snapshot.cycle.end)),
                        style = MaterialTheme.typography.bodySmall,
                    )
                    Text(
                        tr(
                            R.string
                                .l_application_payload_only_not_a_provider_bill_other_traffic_and_co_ca6d2c
                        ),
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                TextButton(onClick = trafficViewModel::refresh, enabled = !traffic.loading) {
                    Text(tr(R.string.l_refresh_traffic_status_f8d930))
                }
            }
        }
    }
}
