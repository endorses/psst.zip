package zip.psst.android.ui.components

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import zip.psst.android.R
import zip.psst.android.data.Appearance

@Composable
fun AppearancePicker(appearance: Appearance, onSelected: (Appearance) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    Box(modifier = Modifier.fillMaxWidth()) {
        OutlinedButton(onClick = { expanded = true }, modifier = Modifier.fillMaxWidth()) {
            Text(
                stringResource(R.string.appearance_value, appearanceLabel(appearance)),
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(8.dp))
            Icon(Icons.Default.ArrowDropDown, contentDescription = null)
        }
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            Appearance.entries.forEach { option ->
                DropdownMenuItem(
                    text = { Text(appearanceLabel(option)) },
                    onClick = {
                        onSelected(option)
                        expanded = false
                    },
                    modifier = Modifier.semantics { selected = appearance == option },
                    trailingIcon = {
                        if (appearance == option)
                            Icon(Icons.Default.Check, contentDescription = null)
                    },
                )
            }
        }
    }
}

@Composable
private fun appearanceLabel(appearance: Appearance): String =
    stringResource(
        when (appearance) {
            Appearance.SYSTEM -> R.string.appearance_system
            Appearance.LIGHT -> R.string.appearance_light
            Appearance.DARK -> R.string.appearance_dark
        }
    )
