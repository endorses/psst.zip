package zip.psst.android.ui.components

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import zip.psst.android.PsstApplication
import zip.psst.android.R

@Composable
fun AccountIndicator(modifier: Modifier = Modifier) {
    val prefs = (LocalContext.current.applicationContext as PsstApplication).prefs
    val access by prefs.historyAccess.collectAsState()
    Text(
        if (access.accountId == null) stringResource(R.string.not_signed_in)
        else "${prefs.getUsername()} · ${access.serverUrl}",
        modifier = modifier,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        maxLines = 2,
        overflow = TextOverflow.Ellipsis,
    )
}
