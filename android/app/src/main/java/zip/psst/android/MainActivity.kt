package zip.psst.android

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.ui.Modifier
import androidx.navigation.compose.rememberNavController
import zip.psst.android.ui.navigation.PsstNavGraph
import zip.psst.android.ui.navigation.Routes
import zip.psst.android.ui.theme.PsstTheme

class MainActivity : ComponentActivity() {

    private val incomingUris = mutableStateListOf<Uri>()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        val app = application as PsstApplication
        val sharedUris = extractSharedUris(intent)
        incomingUris.addAll(sharedUris)

        setContent {
            PsstTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    val navController = rememberNavController()
                    val uris = incomingUris

                    val startDestination =
                        when {
                            !app.prefs.hasServerUrl() -> Routes.SERVER_CONFIG
                            sharedUris.isNotEmpty() && app.prefs.getSessionToken() == null ->
                                Routes.SERVER_CONFIG
                            sharedUris.isNotEmpty() -> Routes.SEND
                            else -> Routes.HOME
                        }

                    PsstNavGraph(
                        navController = navController,
                        startDestination = startDestination,
                        sharedUris = uris.toList(),
                        onSharedUrisConsumed = { incomingUris.clear() },
                    )
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        // Keep live jobs and their ViewModels intact when another share intent arrives.
        incomingUris.addAll(extractSharedUris(intent))
    }

    private fun extractSharedUris(intent: Intent?): List<Uri> {
        if (intent == null) return emptyList()

        return when (intent.action) {
            Intent.ACTION_SEND -> {
                val uri = intent.getParcelableExtra<Uri>(Intent.EXTRA_STREAM)
                listOfNotNull(uri)
            }
            Intent.ACTION_SEND_MULTIPLE -> {
                intent.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM) ?: emptyList()
            }
            else -> emptyList()
        }
    }
}
