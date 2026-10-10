package zip.psst.android.ui.components

import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import zip.psst.android.R
import zip.psst.android.data.usesLocalNetwork
import zip.psst.android.i18n.tr

// Use the wire name so this gate still compiles with older platform stubs.
private const val LOCAL_NETWORK_PERMISSION = "android.permission.ACCESS_LOCAL_NETWORK"

class LocalNetworkAccess
internal constructor(
    private val onRequest: (String, () -> Unit, () -> Unit) -> Unit,
) {
    fun request(origin: String, allowed: () -> Unit) = onRequest(origin, allowed, {})

    fun request(origin: String, allowed: () -> Unit, rejected: () -> Unit) =
        onRequest(origin, allowed, rejected)
}

/** Permission is requested only for an explicitly selected LAN server on Android 17+. */
@Composable
fun rememberLocalNetworkAccess(): LocalNetworkAccess {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var pending by remember { mutableStateOf<Pair<() -> Unit, () -> Unit>?>(null) }
    var denied by remember { mutableStateOf(false) }
    var rationale by remember { mutableStateOf(false) }
    var resolving by remember { mutableStateOf<Job?>(null) }
    val launcher =
        rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
            val action = pending
            pending = null
            if (granted) action?.first?.invoke()
            else {
                denied = true
                action?.second?.invoke()
            }
        }
    DisposableEffect(Unit) {
        onDispose {
            resolving?.cancel()
            pending = null
        }
    }
    if (rationale) {
        fun cancel() {
            rationale = false
            val action = pending
            pending = null
            action?.second?.invoke()
        }
        AlertDialog(
            onDismissRequest = ::cancel,
            title = { Text(tr(R.string.local_network_permission_title)) },
            text = { Text(tr(R.string.local_network_permission_reason)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        rationale = false
                        launcher.launch(LOCAL_NETWORK_PERMISSION)
                    },
                ) {
                    Text(tr(R.string.local_network_allow))
                }
            },
            dismissButton = {
                TextButton(onClick = ::cancel) { Text(tr(R.string.l_cancel_77dfd2)) }
            },
        )
    }
    if (denied) {
        AlertDialog(
            onDismissRequest = { denied = false },
            title = { Text(tr(R.string.local_network_permission_title)) },
            text = { Text(tr(R.string.local_network_permission_denied)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        denied = false
                        context.startActivity(
                            Intent(
                                Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                                Uri.parse("package:" + context.packageName),
                            ),
                        )
                    },
                ) {
                    Text(tr(R.string.local_network_open_settings))
                }
            },
            dismissButton = {
                TextButton(onClick = { denied = false }) { Text(tr(R.string.l_cancel_77dfd2)) }
            },
        )
    }
    return remember(context, scope, launcher) {
        LocalNetworkAccess { origin, allowed, rejected ->
            if (
                Build.VERSION.SDK_INT < 37 ||
                    ContextCompat.checkSelfPermission(context, LOCAL_NETWORK_PERMISSION) ==
                        PackageManager.PERMISSION_GRANTED
            ) {
                allowed()
            } else if (resolving?.isActive != true && pending == null) {
                resolving = scope.launch {
                    if (usesLocalNetwork(origin)) {
                        pending = allowed to rejected
                        rationale = true
                    } else {
                        resolving = null
                        allowed()
                    }
                }
            }
        }
    }
}
