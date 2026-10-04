package zip.psst.android.ui

import zip.psst.android.ui.components.selectScannerCamera
import org.junit.Assert.*
import org.junit.Test

class ScannerSelectionTest {
    @Test
    fun prefersRearCameraRegardlessOfEnumerationOrder() {
        assertEquals(4, selectScannerCamera(listOf(0 to false, 4 to true, 5 to true)))
    }

    @Test
    fun fallsBackToAvailableFrontCamera() {
        assertEquals(2, selectScannerCamera(listOf(2 to false)))
    }

    @Test
    fun explicitlyReportsUnavailableCamera() {
        assertNull(selectScannerCamera(emptyList()))
    }
}
