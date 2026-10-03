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
import zip.psst.android.PsstApplication
import zip.psst.android.ui.screens.HistoryScreen
import zip.psst.android.ui.screens.HomeScreen
import zip.psst.android.ui.screens.ReceiveScreen
import zip.psst.android.ui.screens.ScanScreen
import zip.psst.android.ui.screens.SendScreen
import zip.psst.android.ui.screens.ServerConfigScreen
import zip.psst.android.ui.screens.TransferDetailScreen
import zip.psst.android.viewmodel.ScanViewModel

object Routes {
    const val SERVER_CONFIG = "server_config"
    const val HOME = "home"
    const val SEND = "send"
    const val RECEIVE = "receive"
    const val TRANSFER_DETAIL = "transfer_detail/{transferId}/{encryptionKey}/{type}"
    const val HISTORY = "history"
    const val SCAN = "scan"
    const val LOCAL_RECEIVED = "local_received"

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
    val prefs = (LocalContext.current.applicationContext as PsstApplication).prefs
    var intendedRoute by remember {
        mutableStateOf(if (sharedUris.isNotEmpty()) Routes.SEND else Routes.HOME)
    }
    var intendedAccess by remember { mutableStateOf(prefs.historyAccess.value) }
    fun authenticatedRoute(route: String): String {
        intendedRoute = route
        intendedAccess = prefs.historyAccess.value
        return if (prefs.getSessionToken() != null) route else Routes.SERVER_CONFIG
    }
    fun signInFor(route: String) {
        intendedRoute = route
        navController.navigate(Routes.SERVER_CONFIG)
    }
    val entry by navController.currentBackStackEntryAsState()
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
                onScan = { navController.navigate(Routes.SCAN) },
                onLocalReceived = { navController.navigate(Routes.LOCAL_RECEIVED) },
                onBack = { if (!navController.popBackStack()) navController.navigate(Routes.HOME) },
                onSignedOut = {
                    intendedRoute = Routes.HOME
                    intendedAccess = zip.psst.android.data.HistoryAccess()
                    navController.navigate(Routes.SERVER_CONFIG) {
                        popUpTo(navController.graph.id) { inclusive = true }
                        launchSingleTop = true
                    }
                },
                onConfigured = {
                    val now = prefs.historyAccess.value
                    val sameAccount =
                        intendedAccess.accountId == null ||
                            (intendedAccess.accountId == now.accountId &&
                                intendedAccess.serverUrl == now.serverUrl)
                    val destination = if (sameAccount) intendedRoute else Routes.HOME
                    if (
                        sameAccount &&
                            navController.previousBackStackEntry?.destination?.route == destination
                    )
                        navController.popBackStack()
                    else
                        navController.navigate(destination) {
                            popUpTo(navController.graph.id) { inclusive = true }
                            launchSingleTop = true
                        }
                    intendedAccess = now
                },
            )
        }

        composable(Routes.HOME) {
            HomeScreen(
                onScan = { navController.navigate(Routes.SCAN) },
                onShareFiles = { navController.navigate(authenticatedRoute(Routes.SEND)) },
                onReceiveFiles = { navController.navigate(authenticatedRoute(Routes.RECEIVE)) },
                onHistory = { navController.navigate(Routes.HISTORY) },
                onSettings = {
                    intendedRoute = Routes.HOME
                    intendedAccess = prefs.historyAccess.value
                    navController.navigate(Routes.SERVER_CONFIG)
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
                    onBack = { navController.popBackStack() },
                )
        }

        composable(Routes.SCAN) {
            ScanScreen(onBack = { navController.popBackStack() }, viewModel = guestDownloads)
        }
        composable(Routes.LOCAL_RECEIVED) {
            ScanScreen(
                onBack = { navController.popBackStack() },
                showHistoryInitially = true,
                viewModel = guestDownloads,
            )
        }

        composable(Routes.HISTORY) {
            HistoryScreen(
                onLocalReceived = { navController.navigate(Routes.LOCAL_RECEIVED) },
                onTransferClick = { entity ->
                    navController.navigate(
                        Routes.transferDetail(entity.id, entity.encryptionKey, entity.type)
                    )
                },
                onBack = { navController.popBackStack() },
            )
        }
    }
}
