package zip.psst.android.ui.components

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import zip.psst.android.R
import java.time.Instant
import java.time.format.DateTimeFormatter
import kotlinx.coroutines.delay

@Composable
fun ExpiryCountdown(expiresAt: String?, modifier: Modifier = Modifier) {
    if (expiresAt == null) return

    val expiryInstant =
        remember(expiresAt) {
            try {
                Instant.from(DateTimeFormatter.ISO_DATE_TIME.parse(expiresAt))
            } catch (_: Exception) {
                try {
                    Instant.parse(expiresAt)
                } catch (_: Exception) {
                    null
                }
            }
        } ?: return

    var remainingSeconds by remember { mutableLongStateOf(0L) }

    LaunchedEffect(expiryInstant) {
        while (true) {
            val now = Instant.now()
            val remaining = expiryInstant.epochSecond - now.epochSecond
            remainingSeconds = maxOf(0, remaining)
            if (remaining <= 0) break
            delay(1000L)
        }
    }

    val text =
        if (remainingSeconds <= 0) {
            stringResource(R.string.expired)
        } else {
            val hours = remainingSeconds / 3600
            val minutes = (remainingSeconds % 3600) / 60
            val seconds = remainingSeconds % 60
            if (hours > 0) {
                stringResource(R.string.expires_hours, hours)
            } else if (minutes > 0) {
                stringResource(R.string.expires_minutes, minutes)
            } else {
                stringResource(R.string.expires_seconds, seconds)
            }
        }

    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color =
            if (remainingSeconds <= 300) {
                MaterialTheme.colorScheme.error
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
        modifier = modifier,
    )
}
