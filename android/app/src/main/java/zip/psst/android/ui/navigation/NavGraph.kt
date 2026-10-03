package zip.psst.android.ui.navigation

import android.net.Uri
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.navArgument
import zip.psst.android.PsstApplication
import zip.psst.android.ui.screens.HistoryScreen
import zip.psst.android.ui.screens.HomeScreen
import zip.psst.android.ui.screens.ReceiveScreen
import zip.psst.android.ui.screens.SendScreen
import zip.psst.android.ui.screens.ServerConfigScreen
import zip.psst.android.ui.screens.TransferDetailScreen

object Routes {
    const val SERVER_CONFIG = "server_config"
    const val HOME = "home"
    const val SEND = "send"
    const val RECEIVE = "receive"
    const val TRANSFER_DETAIL = "transfer_detail/{transferId}/{encryptionKey}/{type}"
    const val HISTORY = "history"

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
    modifier: Modifier = Modifier,
) {
    val prefs = (LocalContext.current.applicationContext as PsstApplication).prefs
    fun authenticatedRoute(route: String): String =
        if (prefs.getSessionToken() != null) route else Routes.SERVER_CONFIG
    NavHost(
        navController = navController,
        startDestination = startDestination,
        modifier = modifier,
    ) {
        composable(Routes.SERVER_CONFIG) {
            ServerConfigScreen(
                onSignedOut = {
                    navController.navigate(Routes.SERVER_CONFIG) {
                        popUpTo(navController.graph.id) { inclusive = true }
                        launchSingleTop = true
                    }
                },
                onConfigured = {
                    navController.navigate(Routes.HOME) {
                        popUpTo(navController.graph.id) { inclusive = true }
                        launchSingleTop = true
                    }
                },
            )
        }

        composable(Routes.HOME) {
            HomeScreen(
                onShareFiles = { navController.navigate(authenticatedRoute(Routes.SEND)) },
                onReceiveFiles = { navController.navigate(authenticatedRoute(Routes.RECEIVE)) },
                onHistory = { navController.navigate(Routes.HISTORY) },
                onSettings = { navController.navigate(Routes.SERVER_CONFIG) },
            )
        }

        composable(Routes.SEND) {
            SendScreen(
                sharedUris = sharedUris,
                onSignIn = { navController.navigate(Routes.SERVER_CONFIG) },
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
                onSignIn = { navController.navigate(Routes.SERVER_CONFIG) },
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

            TransferDetailScreen(
                transferId = transferId,
                encryptionKey = encryptionKey,
                type = type,
                onBack = { navController.popBackStack() },
            )
        }

        composable(Routes.HISTORY) {
            HistoryScreen(
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
