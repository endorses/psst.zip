package zip.psst.android.ui.components

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.widthIn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import zip.psst.android.R
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.common.BitMatrix
import com.google.zxing.qrcode.QRCodeWriter
import com.google.zxing.qrcode.decoder.ErrorCorrectionLevel
import kotlin.math.floor

@Composable
fun QrCodeImage(data: String, modifier: Modifier = Modifier) {
    // Encode at module resolution: ZXing supplies the four-module quiet zone.
    // Rendering at whole physical pixels avoids interpolation and extra bitmap padding.
    val matrix = remember(data) { runCatching { brandedQrMatrix(data) }.getOrNull() } ?: return
    val symbol = painterResource(R.drawable.brand_qr_icon)
    val description = stringResource(R.string.qr_code)
    Canvas(
        modifier.widthIn(max = 400.dp).fillMaxWidth().aspectRatio(1f).semantics {
            contentDescription = description
            role = Role.Image
        }
    ) {
        drawRect(Color.White)
        val moduleSize = floor(size.minDimension / matrix.width)
        val origin = floor((size.minDimension - moduleSize * matrix.width) / 2f)
        for (y in 0 until matrix.height) {
            for (x in 0 until matrix.width) {
                if (matrix[x, y]) {
                    drawRect(
                        Color.Black,
                        topLeft = Offset(origin + x * moduleSize, origin + y * moduleSize),
                        size = Size(moduleSize, moduleSize),
                    )
                }
            }
        }
        val backing = brandedQrCoverModules(matrix.width) * moduleSize
        val center = origin + matrix.width * moduleSize / 2f
        drawRect(
            Color.White,
            Offset(center - backing / 2, center - backing / 2),
            Size(backing, backing),
        )
        val symbolSize = backing * 0.92f
        translate(center - symbolSize / 2, center - symbolSize / 2) {
            with(symbol) { draw(Size(symbolSize, symbolSize)) }
        }
    }
}

internal fun brandedQrMatrix(data: String): BitMatrix =
    QRCodeWriter()
        .encode(
            data,
            BarcodeFormat.QR_CODE,
            0,
            0,
            mapOf(
                EncodeHintType.MARGIN to 4,
                EncodeHintType.CHARACTER_SET to "UTF-8",
                EncodeHintType.ERROR_CORRECTION to ErrorCorrectionLevel.H,
            ),
        )

// White backing covers at most 15% of the active QR width, never its quiet zone.
internal fun brandedQrCoverModules(width: Int): Int = ((width - 8) * 0.15).toInt().coerceAtLeast(1)
