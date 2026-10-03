package zip.psst.android.ui.components

import android.content.Intent
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import zip.psst.android.R

@Composable
fun LinkPanel(url: String, details: @Composable () -> Unit = {}) {
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    var copied by remember(url) { mutableStateOf(false) }
    var expanded by remember(url) { mutableStateOf(false) }
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        QrCodeImage(url, size = 180.dp)
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            OutlinedButton(
                onClick = {
                    clipboard.setText(AnnotatedString(url))
                    copied = true
                },
                modifier = Modifier.weight(1f),
            ) {
                Text(stringResource(R.string.copy_link))
            }
            FilledTonalButton(
                onClick = {
                    context.startActivity(
                        Intent.createChooser(
                            Intent(Intent.ACTION_SEND).apply {
                                type = "text/plain"
                                putExtra(Intent.EXTRA_TEXT, url)
                            },
                            context.getString(R.string.share),
                        )
                    )
                },
                modifier = Modifier.weight(1f),
            ) {
                Text(stringResource(R.string.share))
            }
        }
        if (copied)
            Text(
                stringResource(R.string.link_copied),
                Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                style = MaterialTheme.typography.bodySmall,
            )
        TextButton(onClick = { expanded = !expanded }) {
            Text(stringResource(if (expanded) R.string.hide_details else R.string.link_details))
        }
        if (expanded) {
            SelectionContainer { Text(url, style = MaterialTheme.typography.bodySmall) }
            details()
        }
    }
}
