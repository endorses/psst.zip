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
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.navigation.compose.rememberNavController
import zip.psst.android.ui.navigation.PsstNavGraph
import zip.psst.android.ui.navigation.Routes
import zip.psst.android.ui.theme.PsstTheme

class MainActivity : ComponentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        val app = application as PsstApplication
        val sharedUris = extractSharedUris(intent)

        setContent {
            PsstTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    val navController = rememberNavController()
                    val uris = remember { mutableStateListOf<Uri>().apply { addAll(sharedUris) } }

                    val startDestination = when {
                        !app.prefs.hasServerUrl() -> Routes.SERVER_CONFIG
                        sharedUris.isNotEmpty() -> Routes.SEND
                        else -> Routes.HOME
                    }

                    PsstNavGraph(
                        navController = navController,
                        startDestination = startDestination,
                        sharedUris = uris,
                    )
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        // For share intents arriving while the activity is already running,
        // we recreate to pick up the new URIs. A more sophisticated approach
        // would use a shared ViewModel or event bus.
        recreate()
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
