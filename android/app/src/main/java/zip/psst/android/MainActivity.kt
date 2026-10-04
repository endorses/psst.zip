package zip.psst.android

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.rememberNavController
import zip.psst.android.data.restorePendingShares
import zip.psst.android.ui.navigation.PsstNavGraph
import zip.psst.android.ui.navigation.Routes
import zip.psst.android.ui.theme.PsstTheme

class MainActivity : ComponentActivity() {

    private val incomingUris = mutableStateListOf<Uri>()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        val app = application as PsstApplication
        val restoredUris =
            savedInstanceState?.getStringArrayList("pending-share-uris")?.map(Uri::parse)
        incomingUris.addAll(restorePendingShares(restoredUris, extractSharedUris(intent)))
        clearShareIntent()

        setContent {
            val appearance by app.prefs.appearance.collectAsStateWithLifecycle()
            val darkTheme = appearance.isDark(isSystemInDarkTheme())
            SideEffect {
                val barStyle =
                    SystemBarStyle.auto(
                        android.graphics.Color.TRANSPARENT,
                        android.graphics.Color.TRANSPARENT,
                    ) {
                        darkTheme
                    }
                enableEdgeToEdge(statusBarStyle = barStyle, navigationBarStyle = barStyle)
            }
            PsstTheme(darkTheme = darkTheme) {
                Surface(modifier = Modifier.fillMaxSize()) {
                    val navController = rememberNavController()
                    val uris = incomingUris

                    val startDestination = Routes.HOME

                    PsstNavGraph(
                        navController = navController,
                        startDestination = startDestination,
                        sharedUris = uris.toList(),
                        onSharedUrisConsumed = {
                            incomingUris.clear()
                            clearShareIntent()
                        },
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
        clearShareIntent()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        outState.putStringArrayList(
            "pending-share-uris",
            ArrayList(incomingUris.map(Uri::toString)),
        )
        super.onSaveInstanceState(outState)
    }

    private fun clearShareIntent() {
        // URI permissions remain granted to the activity; the payload must not be delivered twice.
        setIntent(
            Intent(intent).apply {
                removeExtra(Intent.EXTRA_STREAM)
                clipData = null
                action = Intent.ACTION_MAIN
                data = null
            }
        )
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
