package zip.psst.android.data

import java.net.InetAddress
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LocalNetworkOriginTest {
    @Test
    fun blockedPlatformDnsDoesNotDelayCallerCancellation() = runBlocking {
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val lookup =
            launch(Dispatchers.Default) {
                resolveServerAddresses("example.test") {
                    entered.countDown()
                    var released = false
                    while (!released) {
                        try {
                            release.await()
                            released = true
                        } catch (_: InterruptedException) {
                            // Simulate a platform resolver which ignores interruption.
                        }
                    }
                    emptyList()
                }
            }
        try {
            assertTrue(entered.await(2, TimeUnit.SECONDS))
            withTimeout(1_000) { lookup.cancelAndJoin() }
        } finally {
            release.countDown()
            lookup.cancelAndJoin()
        }
    }

    @Test
    fun unavailableDnsDoesNotBlockOpeningOfflineHistory() = runBlocking {
        assertFalse(
            usesLocalNetwork("https://example.test", resolutionTimeoutMillis = 10) {
                awaitCancellation()
            },
        )
    }

    @Test
    fun multicastDnsOriginRequiresAccessBeforeDiscovery() = runBlocking {
        assertTrue(usesLocalNetwork(" https://Psst.LOCAL.:8443 "))
        assertFalse(usesLocalNetwork("not a server URL"))
    }

    @Test
    fun publicAndSameProfileLoopbackDoNotRequestNearbyDeviceAccess() {
        for (literal in listOf("1.1.1.1", "2001:4860:4860::8888", "127.0.0.1", "::1")) {
            assertFalse(literal, hasLocalNetworkAddress(listOf(InetAddress.getByName(literal))))
        }
    }

    @Test
    fun privateLinkLocalUniqueLocalAndMixedDnsAnswersRequireAccess() {
        for (literal in
            listOf("10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.1.1", "fe80::1", "fd00::1")) {
            assertTrue(
                literal,
                hasLocalNetworkAddress(
                    listOf(InetAddress.getByName("1.1.1.1"), InetAddress.getByName(literal)),
                ),
            )
        }
    }
}
