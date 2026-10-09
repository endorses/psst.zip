package zip.psst.android.data

import java.net.Inet6Address
import java.net.InetAddress
import java.net.URI
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.SynchronousQueue
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull

// DNS may ignore interrupts. Keep at most two in-flight lookups, with no queued work,
// and let the caller stop waiting even when the platform resolver is still blocked.
private val numericIpv4Host = Regex("[0-9.]+")

private val serverResolver =
    ThreadPoolExecutor(
        0,
        2,
        30,
        TimeUnit.SECONDS,
        SynchronousQueue(),
        { task -> Thread(task, "psst-server-dns").apply { isDaemon = true } },
    )

internal suspend fun resolveServerAddresses(
    host: String,
    lookup: (String) -> List<InetAddress> = { InetAddress.getAllByName(it).toList() },
): List<InetAddress> = suspendCancellableCoroutine { continuation ->
    try {
        val future = serverResolver.submit {
            val addresses = runCatching { lookup(host) }.getOrDefault(emptyList())
            continuation.resume(addresses)
        }
        continuation.invokeOnCancellation { future.cancel(true) }
    } catch (_: RejectedExecutionException) {
        continuation.resume(emptyList())
    }
}

/** Resolve only an operator-selected server, never a transfer key or pairing payload. */
internal suspend fun usesLocalNetwork(
    origin: String,
    resolutionTimeoutMillis: Long = 1_500,
    resolve: suspend (String) -> List<InetAddress> = { resolveServerAddresses(it) },
): Boolean {
    val host =
        runCatching {
            URI(origin.trim()).host?.removePrefix("[")?.removeSuffix("]")?.trimEnd('.')
        }
            .getOrNull() ?: return false
    if (host.endsWith(".local", ignoreCase = true)) return true
    if (host.contains(':') || numericIpv4Host.matches(host)) {
        val address = runCatching { InetAddress.getByName(host) }.getOrNull() ?: return false
        return hasLocalNetworkAddress(listOf(address))
    }
    val addresses = withTimeoutOrNull(resolutionTimeoutMillis) { resolve(host) } ?: return false
    return hasLocalNetworkAddress(addresses)
}

internal fun hasLocalNetworkAddress(addresses: List<InetAddress>): Boolean =
    addresses.any { address ->
        !address.isLoopbackAddress &&
            (address.isSiteLocalAddress ||
                address.isLinkLocalAddress ||
                address.isMulticastAddress ||
                (address is Inet6Address && (address.address[0].toInt() and 0xfe) == 0xfc))
    }
