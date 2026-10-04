package zip.psst.android.data

import zip.psst.shared.api.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test

class TrafficFailureTest {
    @Test
    fun remoteInterruptionMakesOneControlProbeAndPreservesPolicyIdentity() = runBlocking {
        var calls = 0
        val error =
            classifyTrafficFailure(TransferDownloadInterruptedException()) {
                calls++
                TransferTrafficStatus("exhausted", "2026-11-01T00:00:00Z")
            }
        assertTrue(error is TrafficBudgetExhaustedException)
        assertEquals(1, calls)
        assertEquals("2026-11-01T00:00:00Z", (error as TrafficBudgetExhaustedException).retryAt)
    }

    @Test
    fun localFailuresCancellationAndKnownPoliciesNeverProbe() = runBlocking {
        var calls = 0
        for (error in
            listOf(
                java.io.IOException("disk full"),
                java.io.FileNotFoundException(),
                javax.crypto.AEADBadTagException(),
                IllegalArgumentException("invalid manifest"),
                CancellationException(),
                PublicTransfersPausedException(),
                TrafficAccountingUnavailableException(),
            )) {
            assertNull(
                classifyTrafficFailure(error) {
                    calls++
                    TransferTrafficStatus("exhausted")
                }
            )
        }
        assertEquals(0, calls)
    }

    @Test
    fun failedOrReadyProbeNeverCausesPayloadRetryOrInventsBudgetFailure() = runBlocking {
        assertNull(
            classifyTrafficFailure(java.net.SocketException()) {
                throw java.io.IOException("offline")
            }
        )
        assertNull(
            classifyTrafficFailure(TransferDownloadInterruptedException()) {
                TransferTrafficStatus("ready")
            }
        )
    }
}
