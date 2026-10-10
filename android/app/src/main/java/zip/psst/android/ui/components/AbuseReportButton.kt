package zip.psst.android.ui.components

import android.content.ClipData
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
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import zip.psst.android.R
import zip.psst.android.i18n.*
import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.AbuseContact
import zip.psst.shared.model.AbuseReportReference

/** Fetches only public configuration, with a fresh anonymous client for this reference's origin. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AbuseReportButton(reference: AbuseReportReference?) {
    val origin = reference?.origin ?: return
    // Key every piece of transient UI by origin/reference; an old contact cannot flash on a new
    // link.
    var contact by remember(origin) { mutableStateOf<String?>(null) }
    var open by remember(reference.text) { mutableStateOf(false) }
    var notice by remember(reference.text) { mutableStateOf<UiText?>(null) }
    val context = LocalContext.current
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
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
        Text(tr(R.string.l_report_abuse_ef459e))
    }
    if (open)
        ModalBottomSheet(onDismissRequest = { open = false }) {
            Column(
                Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                Text(
                    tr(R.string.l_report_abuse_ef459e),
                    style = MaterialTheme.typography.headlineSmall,
                )
                Text(
                    tr(
                        R.string
                            .l_contact_this_server_s_operator_nothing_is_sent_until_you_send_an__d5e9c9,
                    ),
                )
                SelectionContainer { Text(email) }
                SelectionContainer { Text(reference.text) }
                Text(
                    tr(
                        R.string
                            .l_the_reference_contains_the_server_address_and_when_available_the__d426a8,
                    ),
                    style = MaterialTheme.typography.bodySmall,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextButton(
                        onClick = {
                            scope.launch {
                                clipboard.setClipEntry(ClipEntry(ClipData.newPlainText("", email)))
                                notice = message(R.string.l_contact_copied_fd82dc)
                            }
                        },
                    ) {
                        Text(tr(R.string.l_copy_contact_afe9a1))
                    }
                    TextButton(
                        onClick = {
                            scope.launch {
                                clipboard.setClipEntry(
                                    ClipEntry(ClipData.newPlainText("", reference.text)),
                                )
                                notice = message(R.string.l_reference_copied_d8ac62)
                            }
                        },
                    ) {
                        Text(tr(R.string.l_copy_reference_955753))
                    }
                }
                Button(
                    onClick = {
                        val subject = tr(R.string.l_psst_zip_abuse_report_c6b59c)
                        val body =
                            tr(
                                R.string
                                    .l_i_would_like_to_report_abuse_on_this_server_n_n_1_s_n_ndescriptio_2826ae,
                                (reference.text),
                            )
                        val mailto =
                            "mailto:${Uri.encode(email)}?subject=${Uri.encode(subject)}&body=${Uri.encode(body)}"
                        try {
                            context.startActivity(Intent(Intent.ACTION_SENDTO, Uri.parse(mailto)))
                            notice = null
                        } catch (_: Exception) {
                            notice =
                                message(
                                    R.string
                                        .l_no_email_app_is_available_copy_the_contact_and_reference_to_send__a3e1b1,
                                )
                        }
                    },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(tr(R.string.l_open_email_app_bc54c2))
                }
                notice?.let { Text(it.text(), style = MaterialTheme.typography.bodySmall) }
                TextButton(onClick = { open = false }) { Text(tr(R.string.l_close_bbfa77)) }
            }
        }
}
