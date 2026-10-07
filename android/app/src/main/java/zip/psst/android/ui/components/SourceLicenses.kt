package zip.psst.android.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import zip.psst.android.BuildConfig
import zip.psst.android.R
import zip.psst.android.data.ReleaseSource
import zip.psst.android.i18n.tr

@Composable
fun SourceLicenses(serverURL: String) {
    val context = LocalContext.current
    val uriHandler = LocalUriHandler.current
    var release by remember(serverURL) { mutableStateOf<ReleaseSource?>(null) }
    var loading by remember(serverURL) { mutableStateOf(true) }
    var notice by remember { mutableStateOf<String?>(null) }
    var noticeTitle by remember { mutableStateOf("") }
    LaunchedEffect(serverURL) {
        release =
            withContext(Dispatchers.IO) {
                runCatching { ReleaseSource.fetch(serverURL) }.getOrNull()
            }
        loading = false
    }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(tr(R.string.legal_title), style = MaterialTheme.typography.titleSmall)
        Text(tr(R.string.legal_client_version, BuildConfig.VERSION_NAME))
        TextButton(onClick = { uriHandler.openUri("https://github.com/endorses/psst.zip") }) {
            Text(tr(R.string.legal_project_source))
        }
        if (BuildConfig.SOURCE_REVISION.matches(Regex("[a-f0-9]{40}"))) {
            Text(BuildConfig.SOURCE_REVISION, style = MaterialTheme.typography.bodySmall)
            TextButton(
                onClick = {
                    uriHandler.openUri(
                        "https://github.com/endorses/psst.zip/archive/${BuildConfig.SOURCE_REVISION}.tar.gz"
                    )
                }
            ) {
                Text(tr(R.string.legal_client_source))
            }
        }
        for ((file, title) in
            listOf(
                "AGPL-3.0-only.txt" to R.string.legal_license,
                "THIRD_PARTY_NOTICES.txt" to R.string.legal_notices,
            )) {
            val caption = tr(title)
            val unavailable = tr(R.string.legal_notices_unavailable)
            TextButton(
                onClick = {
                    noticeTitle = caption
                    notice =
                        runCatching {
                                context.assets.open("licenses/$file").bufferedReader().use {
                                    it.readText()
                                }
                            }
                            .getOrDefault(unavailable)
                }
            ) {
                Text(caption)
            }
        }
        Text(tr(R.string.legal_hosted_version), style = MaterialTheme.typography.titleSmall)
        if (loading) LinearProgressIndicator()
        else if (release == null) Text(tr(R.string.legal_unavailable))
        release?.let { info ->
            Text(info.version)
            Text(info.revision, style = MaterialTheme.typography.bodySmall)
            TextButton(onClick = { uriHandler.openUri(info.sourceArchive) }) {
                Text(tr(R.string.legal_exact_source))
            }
        }
        ReleaseSource.metadataURL(serverURL)?.removeSuffix("licenses/release.json")?.let { origin ->
            TextButton(onClick = { uriHandler.openUri("${origin}legal") }) {
                Text(tr(R.string.legal_server_licenses))
            }
        }
    }
    notice?.let { body ->
        AlertDialog(
            onDismissRequest = { notice = null },
            title = { Text(noticeTitle) },
            text = {
                androidx.compose.foundation.text.selection.SelectionContainer {
                    Text(
                        body,
                        modifier =
                            Modifier.heightIn(max = 400.dp).verticalScroll(rememberScrollState()),
                    )
                }
            },
            confirmButton = {
                TextButton(onClick = { notice = null }) { Text(tr(R.string.l_done_e9b450)) }
            },
        )
    }
}
