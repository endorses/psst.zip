package zip.psst.android.ui.navigation

import android.net.Uri
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.navArgument
import kotlinx.coroutines.CancellationException
import zip.psst.android.PsstApplication
import zip.psst.android.ui.components.rememberLocalNetworkAccess
import zip.psst.android.ui.screens.HistoryScreen
import zip.psst.android.ui.screens.HomeScreen
import zip.psst.android.ui.screens.ReceiveScreen
import zip.psst.android.ui.screens.ScanScreen
import zip.psst.android.ui.screens.SendScreen
import zip.psst.android.ui.screens.ServerConfigScreen
import zip.psst.android.ui.screens.SettingsScreen
import zip.psst.android.ui.screens.TrafficUsageScreen
import zip.psst.android.ui.screens.TransferDetailScreen
import zip.psst.android.viewmodel.HistoryViewModel
import zip.psst.android.viewmodel.ScanViewModel
import zip.psst.shared.api.ApiClient
import zip.psst.shared.model.ServerConfig

object Routes {
    const val SERVER_CONFIG = "server_config"
    const val HOME = "home"
    const val SEND = "send"
    const val RECEIVE = "receive"
    const val TRANSFER_DETAIL = "transfer_detail/{transferId}/{encryptionKey}/{type}"
    const val HISTORY = "history"
    const val SCAN = "scan"
    const val DOWNLOAD_DETAIL = "download_detail"
    const val SETTINGS = "settings"
    const val USAGE = "usage"
    const val DOWNLOADED_HISTORY = "history_downloaded"

    fun transferDetail(transferId: String, encryptionKey: String, type: String): String {
        val encodedKey = Uri.encode(encryptionKey)
        return "transfer_detail/$transferId/$encodedKey/$type"
    }
}

@Composable
fun PsstNavGraph(
    navController: NavHostController,
    startDestination: String,
    sharedUris: List<Uri>,
    onSharedUrisConsumed: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val guestDownloads: ScanViewModel = viewModel()
    // Keep hydrated presentation across popped History destinations. Disk reads and
    // private-row enrichment start here; HTTP remains tied to HistoryScreen visibility.
    val serverHistory: HistoryViewModel = viewModel(key = "serverHistory")
    val downloadedHistory: HistoryViewModel = viewModel(key = "downloadedHistory")
    val prefs = (LocalContext.current.applicationContext as PsstApplication).prefs
    val networkAccess = rememberLocalNetworkAccess()
    var intendedRoute by remember {
        mutableStateOf(if (sharedUris.isNotEmpty()) Routes.SEND else Routes.HOME)
    }
    var intendedAccess by remember { mutableStateOf(prefs.historyAccess.value) }
    fun authenticatedRoute(route: String): String {
        intendedRoute = route
        intendedAccess = prefs.historyAccess.value
        return if (
            prefs.getSessionToken() != null &&
                !prefs.historyAccess.value.isAdmin &&
                !prefs.historyAccess.value.mustChangePassword
        )
            route
        else Routes.SERVER_CONFIG
    }
    fun signInFor(route: String) {
        intendedRoute = route
        intendedAccess = prefs.historyAccess.value
        navController.navigate(Routes.SERVER_CONFIG)
    }
    val entry by navController.currentBackStackEntryAsState()
    LaunchedEffect(Unit) {
        if (prefs.historyAccess.value.isAdmin) {
            navController.navigate(Routes.SERVER_CONFIG) { launchSingleTop = true }
        } else {
            val token = prefs.getSessionToken()
            if (token != null) {
                val client = ApiClient(ServerConfig(prefs.getServerUrl()), sessionToken = token)
                try {
                    val user = client.auth.me()
                    if (
                        prefs.getSessionToken() == token &&
                            (user.role == "admin" || user.mustChangePassword)
                    )
                        navController.navigate(Routes.SERVER_CONFIG) { launchSingleTop = true }
                } catch (error: CancellationException) {
                    throw error
                } catch (_: Exception) {
                    /* Offline guest access stays available; authenticated writes recheck state. */
                } finally {
                    client.close()
                }
            }
        }
    }
    LaunchedEffect(sharedUris) {
        if (
            sharedUris.isNotEmpty() &&
                entry?.destination?.route !in listOf(Routes.SEND, Routes.SERVER_CONFIG)
        ) {
            navController.navigate(authenticatedRoute(Routes.SEND))
        }
    }
    NavHost(
        navController = navController,
        startDestination = startDestination,
        modifier = modifier,
    ) {
        composable(Routes.SERVER_CONFIG) {
            ServerConfigScreen(
                onAccountCleared = {
                    onSharedUrisConsumed()
                    intendedRoute = Routes.HOME
                    navController.popBackStack(Routes.HOME, false)
                },
                onBack = { if (!navController.popBackStack()) navController.navigate(Routes.HOME) },
                onConfigured = {
                    val now = prefs.historyAccess.value
                    val sameAccount =
                        intendedAccess.accountId == null ||
                            (intendedAccess.accountId == now.accountId &&
                                intendedAccess.serverUrl == now.serverUrl)
                    val destination = if (sameAccount) intendedRoute else Routes.HOME
                    if (!sameAccount) onSharedUrisConsumed()
                    if (
                        sameAccount &&
                            navController.previousBackStackEntry?.destination?.route == destination
                    )
                        navController.popBackStack()
                    else {
                        navController.popBackStack(Routes.HOME, false)
                        if (destination != Routes.HOME)
                            navController.navigate(destination) { launchSingleTop = true }
                    }
                    intendedAccess = now
                },
            )
        }

        composable(Routes.USAGE) { TrafficUsageScreen(onBack = { navController.popBackStack() }) }

        composable(Routes.SETTINGS) {
            SettingsScreen(
                onUsage = { navController.navigate(Routes.USAGE) },
                onBack = { navController.popBackStack() },
                onAccount = {
                    intendedRoute = Routes.HOME
                    intendedAccess = prefs.historyAccess.value
                    navController.navigate(Routes.SERVER_CONFIG)
                },
                onSignedOut = {
                    onSharedUrisConsumed()
                    intendedRoute = Routes.HOME
                    intendedAccess = prefs.historyAccess.value
                    navController.popBackStack(Routes.HOME, false)
                },
            )
        }

        composable(Routes.HOME) {
            HomeScreen(
                onScan = {
                    guestDownloads.clear()
                    navController.navigate(Routes.SCAN)
                },
                onShareFiles = { navController.navigate(authenticatedRoute(Routes.SEND)) },
                onReceiveFiles = { navController.navigate(authenticatedRoute(Routes.RECEIVE)) },
                onHistory = { navController.navigate(Routes.HISTORY) },
                onSettings = {
                    intendedRoute = Routes.HOME
                    intendedAccess = prefs.historyAccess.value
                    navController.navigate(Routes.SETTINGS)
                },
            )
        }

        composable(Routes.SEND) {
            SendScreen(
                sharedUris = sharedUris,
                onSharedUrisConsumed = onSharedUrisConsumed,
                onSignIn = { signInFor(Routes.SEND) },
                onTransferCreated = { transferId, key, type ->
                    navController.navigate(Routes.transferDetail(transferId, key, type)) {
                        popUpTo(Routes.HOME)
                    }
                },
                onBack = { navController.popBackStack() },
            )
        }

        composable(Routes.RECEIVE) {
            ReceiveScreen(
                onSignIn = { signInFor(Routes.RECEIVE) },
                onSlotCreated = { slotId, key ->
                    navController.navigate(Routes.transferDetail(slotId, key, "receive")) {
                        popUpTo(Routes.HOME)
                    }
                },
                onBack = { navController.popBackStack() },
            )
        }

        composable(
            route = Routes.TRANSFER_DETAIL,
            arguments =
                listOf(
                    navArgument("transferId") { type = NavType.StringType },
                    navArgument("encryptionKey") { type = NavType.StringType },
                    navArgument("type") { type = NavType.StringType },
                ),
        ) { backStackEntry ->
            val transferId = backStackEntry.arguments?.getString("transferId") ?: ""
            val encryptionKey =
                Uri.decode(backStackEntry.arguments?.getString("encryptionKey") ?: "")
            val type = backStackEntry.arguments?.getString("type") ?: "send"

            if (type == "receive" || type == "received")
                ReceiveScreen(
                    existingId = transferId,
                    onSlotCreated = { _, _ -> },
                    onSignIn = {
                        signInFor(Routes.transferDetail(transferId, encryptionKey, type))
                    },
                    onBack = { navController.popBackStack() },
                )
            else
                TransferDetailScreen(
                    transferId = transferId,
                    encryptionKey = encryptionKey,
                    type = type,
                    onCreateReplacement = {
                        navController.navigate(authenticatedRoute(Routes.SEND))
                    },
                    onBack = { navController.popBackStack() },
                )
        }

        composable(Routes.SCAN) {
            ScanScreen(
                onBack = { navController.popBackStack() },
                onHistory = {
                    navController.navigate(Routes.DOWNLOADED_HISTORY) {
                        popUpTo(Routes.SCAN) { inclusive = true }
                    }
                },
                viewModel = guestDownloads,
            )
        }
        composable(Routes.DOWNLOAD_DETAIL) {
            ScanScreen(
                onBack = { navController.popBackStack() },
                historical = true,
                onHistory = { navController.popBackStack() },
                viewModel = guestDownloads,
            )
        }
        listOf(Routes.HISTORY, Routes.DOWNLOADED_HISTORY).forEach { route ->
            composable(route) {
                HistoryScreen(
                    onAccount = { signInFor(route) },
                    guest = guestDownloads,
                    viewModel =
                        if (route == Routes.DOWNLOADED_HISTORY) downloadedHistory
                        else serverHistory,
                    initialFilter = if (route == Routes.DOWNLOADED_HISTORY) "downloaded" else "all",
                    onDownloadClick = { record ->
                        networkAccess.request(record.origin) {
                            if (guestDownloads.open(record))
                                navController.navigate(Routes.DOWNLOAD_DETAIL)
                        }
                    },
                    onTransferClick = { entity ->
                        navController.navigate(
                            Routes.transferDetail(entity.id, entity.encryptionKey, entity.type),
                        )
                    },
                    onBack = { navController.popBackStack() },
                )
            }
        }
    }
}
