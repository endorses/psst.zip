package zip.psst.android.ui

import zip.psst.android.data.*
import zip.psst.android.ui.components.*
import zip.psst.android.viewmodel.SendUiState
import com.google.zxing.BarcodeFormat
import com.google.zxing.qrcode.QRCodeWriter
import org.junit.Assert.*
import org.junit.Test

class NavigationHistoryTest {
    private fun owned(
        id: String = "same",
        type: String = "sent",
        server: String = "https://one.test",
        time: Long = 100,
    ) = TransferHistoryEntity(id, type, 1, 10, server, "key", "complete", time, accountId = "owner")

    private fun downloaded(origin: String = "https://one.test", time: Long = 150) =
        GuestDownload(origin, origin, "same", time, complete = true, receiptPending = true)

    @Test
    fun restoredShareQueuePreservesPendingAuthenticationButNeverReplaysConsumedLaunch() {
        val launch = listOf("content://sender/one", "content://sender/two")
        assertEquals(launch, restorePendingShares(null, launch))
        assertEquals(launch, restorePendingShares(launch, emptyList()))
        assertTrue(restorePendingShares(emptyList<String>(), launch).isEmpty())
        assertEquals(
            listOf("content://sender/new"),
            restorePendingShares(listOf("content://sender/new"), launch),
        )
    }

    @Test
    fun completedNavigationIsConsumedWithoutLosingTransfer() {
        val uploading = SendUiState(isUploading = true, transferId = "id", encryptionKey = "key")
        assertNull(uploading.pendingCompletion())
        val completed = uploading.copy(isUploading = false)
        assertEquals("id" to "key", completed.pendingCompletion())
        val consumed = completed.copy(completionConsumed = true)
        assertNull(consumed.pendingCompletion())
        assertNull(
            consumed
                .copy(error = zip.psst.android.i18n.userText("status refresh failed"))
                .pendingCompletion()
        )
        assertEquals("id", consumed.transferId)
        assertEquals("key", consumed.encryptionKey)
    }

    @Test
    fun mixedHistoryKeepsMeaningOriginIdentityAndStableOrdering() {
        val rows =
            unifiedHistory(
                listOf(owned(), owned(type = "received", time = 200)),
                listOf(downloaded(), downloaded("https://two.test")),
                "all",
            )
        assertEquals(4, rows.size)
        assertEquals(4, rows.map { it.key }.distinct().size)
        assertEquals(200L, rows.first().createdAt)
        assertEquals(
            rows,
            unifiedHistory(
                listOf(owned(type = "received", time = 200), owned()),
                listOf(downloaded("https://two.test"), downloaded()),
                "all",
            ),
        )
        assertTrue((rows[1] as HistoryRow.Downloaded).value.receiptPending)
    }

    @Test
    fun filtersAndSignOutNeverHideDownloadedRecords() {
        val owned = listOf(owned(), owned(type = "received", server = "https://two.test"))
        val local = listOf(downloaded())
        assertEquals(1, unifiedHistory(owned, local, "sent").size)
        assertEquals(1, unifiedHistory(owned, local, "received").size)
        assertEquals(1, unifiedHistory(owned, local, "downloaded").size)
        val signedOut = owned.filter(HistoryAccess()::permits)
        assertTrue(unifiedHistory(signedOut, local, "all").single() is HistoryRow.Downloaded)
        val switched = owned.filter(HistoryAccess("https://two.test", "owner")::permits)
        assertEquals(2, unifiedHistory(switched, local, "all").size)
    }

    @Test
    fun imageDecoderAcceptsQrWithoutNetworkOrCamera() {
        val expected = "https://example.test/d/00000000-0000-4000-8000-000000000000#key"
        val matrix = QRCodeWriter().encode(expected, BarcodeFormat.QR_CODE, 400, 400)
        val pixels =
            IntArray(400 * 400) {
                if (matrix[it % 400, it / 400]) 0xff000000.toInt() else 0xffffffff.toInt()
            }
        assertEquals(expected, decodeQrPixels(400, 400, pixels))
    }

    @Test
    fun brandedQrDecodesRepresentativeTransfersAndPairingWithCoveredCenter() {
        val origin = "https://" + "a".repeat(63) + "." + "b".repeat(63) + ".example.test:8443"
        val id = "00000000-0000-4000-8000-000000000000"
        val key = "A".repeat(43)
        val values =
            listOf(
                "$origin/d/$id#$key",
                "$origin/u/$id#$key",
                """{"type":"psst-pairing","version":1,"server":"$origin","code":"$key"}""",
            )
        for (value in values) {
            val matrix = brandedQrMatrix(value)
            val scale = 6
            val width = matrix.width * scale
            val cover = brandedQrCoverModules(matrix.width) * scale
            // Opaque checker covers the complete backing, a stricter occlusion than our symbol.
            val pixels =
                IntArray(width * width) { index ->
                    val x = index % width
                    val y = index / width
                    val covered =
                        kotlin.math.abs(x - width / 2) <= cover / 2 &&
                            kotlin.math.abs(y - width / 2) <= cover / 2
                    if (if (covered) (x / 3 + y / 3) % 2 == 0 else matrix[x / scale, y / scale])
                        0xff000000.toInt()
                    else 0xffffffff.toInt()
                }
            assertEquals(value, decodeQrPixels(width, width, pixels))
        }
    }

    @Test(expected = Exception::class)
    fun imageDecoderRejectsBlankImages() {
        decodeQrPixels(200, 200, IntArray(40000) { 0xffffffff.toInt() })
    }

    @Test(expected = IllegalArgumentException::class)
    fun imageDecoderRejectsAmbiguousImages() {
        val first = QRCodeWriter().encode("first", BarcodeFormat.QR_CODE, 250, 250)
        val second = QRCodeWriter().encode("second", BarcodeFormat.QR_CODE, 250, 250)
        val pixels =
            IntArray(500 * 250) {
                val x = it % 500
                val y = it / 500
                if ((if (x < 250) first[x, y] else second[x - 250, y])) 0xff000000.toInt()
                else 0xffffffff.toInt()
            }
        decodeQrPixels(500, 250, pixels)
    }
}
