package zip.psst.android.ui

import zip.psst.android.data.HistoryAccess
import zip.psst.android.viewmodel.AccountHistoryPageState
import zip.psst.shared.api.AuthResourceTransfer
import zip.psst.shared.api.AuthResources
import org.junit.Assert.*
import org.junit.Test

class HistoryHydrationTest {
    private val access = HistoryAccess("https://one.test", "owner")
    private val cachedRows =
        AuthResources(transfers = listOf(AuthResourceTransfer("cached", "complete")))

    @Test
    fun coldAndWarmOpenNeverClaimEmptyWhileCacheOrRowsAreHydrating() {
        // A fresh navigation entry always begins with no disk answer, including a warm reopen.
        repeat(2) {
            assertFalse(AccountHistoryPageState().isKnownEmptyFor(access))
            assertFalse(AccountHistoryPageState(access, loading = true).isKnownEmptyFor(access))
            // Cache facts have arrived, but the private-enrichment history flow still emits [].
            assertFalse(AccountHistoryPageState(access, page = cachedRows).isKnownEmptyFor(access))
        }
    }

    @Test
    fun onlyAResolvedEmptyPageForTheCurrentScopeShowsAnEmptyState() {
        val empty = AccountHistoryPageState(access, page = AuthResources())
        assertTrue(empty.isKnownEmptyFor(access))
        assertTrue(
            empty.copy(loading = true).isKnownEmptyFor(access)
        ) // Quiet refresh retains facts.
        assertFalse(empty.isKnownEmptyFor(access.copy(accountId = "other")))
        assertFalse(empty.isKnownEmptyFor(access.copy(serverUrl = "https://two.test")))
        assertFalse(
            empty
                .copy(access = access.copy(isAdmin = true))
                .isKnownEmptyFor(access.copy(isAdmin = true))
        )
    }
}
