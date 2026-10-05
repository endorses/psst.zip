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
        assertFalse(message.contains("private.invalid"))
        assertFalse(message.contains("secret"))
        assertTrue(message.contains("refresh"))
        assertTrue(message.contains("files are still selected"))
    }

    @Test
    fun policyAndUnknownCapacityWarningsRemainSpecific() {
        val quota = "The server traffic budget is exhausted"
        assertEquals(quota, receiveCapacityError(TransferPolicyException(quota)))
        val unknown =
            "Receive capacity could not be checked. Refresh and try again; your files are still selected."
        assertEquals(unknown, receiveCapacityError(IllegalArgumentException(unknown)))
    }
}
