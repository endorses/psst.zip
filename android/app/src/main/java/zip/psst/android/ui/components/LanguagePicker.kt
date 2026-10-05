package zip.psst.android.ui.components

import androidx.appcompat.app.AppCompatDelegate
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.res.stringResource
import androidx.core.os.LocaleListCompat
import zip.psst.android.R
import zip.psst.android.data.AppLanguage

@Composable
fun LanguagePicker() {
    // Observe configuration so choices made in the OS per-app settings are reflected here too.
    val configuration = LocalConfiguration.current
    val language = AppLanguage.fromTag(AppCompatDelegate.getApplicationLocales().toLanguageTags())
    var expanded by remember { mutableStateOf(false) }
    val system = stringResource(R.string.language_system)
    fun label(value: AppLanguage) =
        when (value) {
            AppLanguage.SYSTEM -> system
            AppLanguage.ENGLISH -> "English"
            AppLanguage.GERMAN -> "Deutsch"
        }
    Box(Modifier.fillMaxWidth()) {
        OutlinedButton(onClick = { expanded = true }, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.language_value, label(language)))
        }
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            AppLanguage.entries.forEach { option ->
                DropdownMenuItem(
                    text = { Text(label(option)) },
                    onClick = {
                        AppCompatDelegate.setApplicationLocales(
                            LocaleListCompat.forLanguageTags(option.tag)
                        )
                        expanded = false
                    },
                )
            }
        }
    }
}
