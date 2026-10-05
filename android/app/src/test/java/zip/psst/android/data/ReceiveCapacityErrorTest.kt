package zip.psst.android.data

import zip.psst.shared.api.TransferPolicyException
import java.io.IOException
import org.junit.Assert.*
import org.junit.Test

class ReceiveCapacityErrorTest {
    @Test
    fun connectionFailureOmitsRawUrlAndKeepsActionableRecovery() {
        val message =
            receiveCapacityError(
                IOException("unexpected end of stream on https://private.invalid/u/id#secret")
            )
        assertFalse(message.english()!!.contains("private.invalid"))
        assertFalse(message.english()!!.contains("secret"))
        assertTrue(message.english()!!.contains("refresh"))
        assertTrue(message.english()!!.contains("files are still selected"))
    }

    @Test
    fun policyAndUnknownCapacityWarningsRemainSpecific() {
        val quota =
            TransferPolicyException("operator diagnostic", code = "traffic_budget_exhausted")
        assertEquals(
            zip.psst.android.R.string.failure_traffic_budget_exhausted,
            receiveCapacityError(quota).resource,
        )
        // Arbitrary English server/legacy prose never selects a localized operational outcome.
        val unknown = IllegalArgumentException("<html>secret link#fragment</html>")
        assertEquals(
            zip.psst.android.R.string.failure_unknown,
            receiveCapacityError(unknown).resource,
        )
    }
}
