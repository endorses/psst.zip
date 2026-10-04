package zip.psst.android.ui.components

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Flag
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.AbuseContact
import zip.psst.shared.model.AbuseReportReference
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeout

/** Fetches only public configuration, with a fresh anonymous client for this reference's origin. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AbuseReportButton(reference: AbuseReportReference?) {
    val origin = reference?.origin ?: return
    // Key every piece of transient UI by origin/reference; an old contact cannot flash on a new
    // link.
    var contact by remember(origin) { mutableStateOf<String?>(null) }
    var open by remember(reference.text) { mutableStateOf(false) }
    var notice by remember(reference.text) { mutableStateOf<String?>(null) }
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    LaunchedEffect(origin) {
        val client = ApiClient.anonymous(origin)
        try {
            contact =
                withTimeout(8_000) { AbuseContact.normalize(client.limits.get().abuseContactEmail) }
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            contact = null
        } finally {
            client.close()
        }
    }
    val email = contact ?: return
    TextButton(onClick = { open = true }) {
        Icon(Icons.Outlined.Flag, contentDescription = null)
        Spacer(Modifier.width(8.dp))
        Text("Report abuse")
    }
    if (open)
        ModalBottomSheet(onDismissRequest = { open = false }) {
            Column(
                Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                Text("Report abuse", style = MaterialTheme.typography.headlineSmall)
                Text(
                    "Contact this server’s operator. Nothing is sent until you send an email yourself."
                )
                SelectionContainer { Text(email) }
                SelectionContainer { Text(reference.text) }
                Text(
                    "The reference contains the server address and, when available, the resource ID. Do not add the full link, encryption key, filenames, or file contents.",
                    style = MaterialTheme.typography.bodySmall,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextButton(
                        onClick = {
                            clipboard.setText(AnnotatedString(email))
                            notice = "Contact copied"
                        }
                    ) {
                        Text("Copy contact")
                    }
                    TextButton(
                        onClick = {
                            clipboard.setText(AnnotatedString(reference.text))
                            notice = "Reference copied"
                        }
                    ) {
                        Text("Copy reference")
                    }
                }
                Button(
                    onClick = {
                        val subject = "psst.zip abuse report"
                        val body =
                            "I would like to report abuse on this server.\n\n${reference.text}\n\nDescription (do not include encryption keys or file contents):\n"
                        val mailto =
                            "mailto:${Uri.encode(email)}?subject=${Uri.encode(subject)}&body=${Uri.encode(body)}"
                        try {
                            context.startActivity(Intent(Intent.ACTION_SENDTO, Uri.parse(mailto)))
                            notice = null
                        } catch (_: Exception) {
                            notice =
                                "No email app is available. Copy the contact and reference to send your report."
                        }
                    },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("Open email app")
                }
                notice?.let { Text(it, style = MaterialTheme.typography.bodySmall) }
                TextButton(onClick = { open = false }) { Text("Close") }
            }
        }
}
