package zip.psst.android.ui.components

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Icon
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.input.KeyboardType
import zip.psst.android.R
import zip.psst.android.data.linkLimitError
import zip.psst.android.i18n.*

/** Owner-only creation control; the result/QR layout does not contain editable settings. */
@Composable
internal fun LinkLimits(
    enabled: Boolean,
    value: String,
    editable: Boolean,
    label: String,
    help: String,
    onEnabledChange: (Boolean) -> Unit,
    onValueChange: (String) -> Unit,
) {
    var expanded by rememberSaveable { mutableStateOf(false) }
    val error = linkLimitError(enabled, value)
    Column(Modifier.fillMaxWidth()) {
        TextButton(
            onClick = { expanded = !expanded },
            modifier =
                Modifier.fillMaxWidth().semantics {
                    stateDescription =
                        if (expanded) tr(R.string.l_expanded_6d1704)
                        else tr(R.string.l_collapsed_0084e8)
                },
        ) {
            Text(
                if (enabled)
                    tr(
                        R.string.l_link_limits_1_s_5991d3,
                        (value.ifBlank { tr(R.string.ui_set_limit) }),
                    )
                else tr(R.string.l_link_limits_7c69c0)
            )
            Icon(if (expanded) Icons.Default.ExpandLess else Icons.Default.ExpandMore, null)
        }
        if (expanded) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(
                    checked = enabled,
                    onCheckedChange = onEnabledChange,
                    enabled = editable,
                    modifier = Modifier.semantics { contentDescription = label },
                )
                Text(label)
            }
            if (enabled) {
                OutlinedTextField(
                    value = value,
                    onValueChange = onValueChange,
                    enabled = editable,
                    label = { Text(label) },
                    isError = error != null,
                    supportingText = {
                        Text(
                            error?.text() ?: help,
                            Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                        )
                    },
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            } else Text(tr(R.string.l_unlimited_existing_server_restrictions_still_apply_167a1a))
            if (!editable) Text(tr(R.string.l_the_selected_limit_is_fixed_for_this_link_a37d0a))
        }
    }
}
