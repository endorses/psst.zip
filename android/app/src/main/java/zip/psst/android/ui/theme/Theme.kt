package zip.psst.android.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

private val LightColorScheme =
    lightColorScheme(
        primary = Color(0xFF0F766E),
        onPrimary = Color.White,
        primaryContainer = Color(0xFFDDF4EC),
        onPrimaryContainer = Color(0xFF172B2A),
        secondary = Color(0xFF526561),
        onSecondary = Color.White,
        secondaryContainer = Color(0xFFDDF4EC),
        onSecondaryContainer = Color(0xFF172B2A),
        tertiary = Color(0xFF735600),
        onTertiary = Color.White,
        background = Color(0xFFF6F8F7),
        onBackground = Color(0xFF172B2A),
        surface = Color.White,
        onSurface = Color(0xFF172B2A),
        surfaceVariant = Color(0xFFE8EFEC),
        onSurfaceVariant = Color(0xFF526561),
        surfaceContainer = Color(0xFFF0F5F2),
        surfaceContainerHigh = Color(0xFFE8EFEC),
        outline = Color(0xFF62756F),
        outlineVariant = Color(0xFF62756F),
        error = Color(0xFFB3261E),
        onError = Color.White,
    )
private val DarkColorScheme =
    darkColorScheme(
        primary = Color(0xFF5EEAD4),
        onPrimary = Color(0xFF172B2A),
        primaryContainer = Color(0xFF21443E),
        onPrimaryContainer = Color(0xFFDDF4EC),
        secondary = Color(0xFFBDD1CA),
        onSecondary = Color(0xFF172B2A),
        secondaryContainer = Color(0xFF21443E),
        onSecondaryContainer = Color(0xFFDDF4EC),
        tertiary = Color(0xFFF5D280),
        onTertiary = Color(0xFF332600),
        background = Color(0xFF0B1816),
        onBackground = Color(0xFFE7F2ED),
        surface = Color(0xFF122321),
        onSurface = Color(0xFFE7F2ED),
        surfaceVariant = Color(0xFF233B35),
        onSurfaceVariant = Color(0xFFBDD1CA),
        surfaceContainer = Color(0xFF192E29),
        surfaceContainerHigh = Color(0xFF233B35),
        outline = Color(0xFF8AA59B),
        outlineVariant = Color(0xFF8AA59B),
        error = Color(0xFFFFB4AB),
        onError = Color(0xFF690005),
    )

@Composable
fun PsstTheme(darkTheme: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (darkTheme) DarkColorScheme else LightColorScheme,
        content = content,
    )
}
