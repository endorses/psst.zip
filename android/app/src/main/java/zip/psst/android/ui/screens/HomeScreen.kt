package zip.psst.android.ui.screens

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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import zip.psst.android.PsstApplication
import zip.psst.android.R
import zip.psst.android.i18n.*

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(
    onShareFiles: () -> Unit,
    onReceiveFiles: () -> Unit,
    onHistory: () -> Unit,
    onSettings: () -> Unit,
    onScan: () -> Unit,
) {
    val prefs = (LocalContext.current.applicationContext as PsstApplication).prefs
    val access by prefs.historyAccess.collectAsState()
    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        Icon(
                            painterResource(R.drawable.brand_symbol),
                            null,
                            Modifier.size(32.dp),
                            tint = androidx.compose.ui.graphics.Color.Unspecified,
                        )
                        Text(stringResource(R.string.app_name))
                    }
                },
                actions = {
                    IconButton(onClick = onSettings) {
                        Icon(
                            Icons.Default.Settings,
                            contentDescription = stringResource(R.string.settings),
                        )
                    }
                },
            )
        }
    ) { padding ->
        Column(
            modifier =
                Modifier.fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(padding)
                    .padding(24.dp),
            verticalArrangement = Arrangement.Center,
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                text =
                    if (access.accountId != null) "${prefs.getUsername()} · ${access.serverUrl}"
                    else stringResource(R.string.not_signed_in),
                style = MaterialTheme.typography.headlineSmall,
            )

            Spacer(Modifier.height(32.dp))

            ActionCard(
                Icons.Default.QrCodeScanner,
                tr(R.string.l_scan_qr_code_e7d8c3),
                tr(R.string.l_scan_or_paste_a_link_to_save_files_without_signing_in_62a0be),
                onScan,
            )
            Spacer(Modifier.height(16.dp))

            ActionCard(
                icon = Icons.AutoMirrored.Filled.Send,
                title = stringResource(R.string.send_files),
                description = stringResource(R.string.send_description),
                onClick = onShareFiles,
            )

            Spacer(Modifier.height(16.dp))

            ActionCard(
                icon = Icons.Default.Download,
                title = stringResource(R.string.receive_link),
                description = stringResource(R.string.receive_description),
                onClick = onReceiveFiles,
            )
            Spacer(Modifier.height(16.dp))
            ActionCard(
                Icons.Default.History,
                stringResource(R.string.history),
                stringResource(R.string.history_description),
                onHistory,
            )
        }
    }
}

@Composable
private fun ActionCard(icon: ImageVector, title: String, description: String, onClick: () -> Unit) {
    Card(
        onClick = onClick,
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(20.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                modifier = Modifier.size(40.dp),
                tint = MaterialTheme.colorScheme.primary,
            )

            Spacer(Modifier.width(16.dp))

            Column(modifier = Modifier.weight(1f)) {
                Text(text = title, style = MaterialTheme.typography.titleMedium)
                Text(
                    text = description,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}
