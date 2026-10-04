package zip.psst.android.ui.components

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.autofill.AutofillNode
import androidx.compose.ui.autofill.AutofillType
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalAutofill
import androidx.compose.ui.platform.LocalAutofillTree

/** Uses the autofill API supported by the installed Compose version. */
@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun Modifier.loginAutofill(password: Boolean, onFill: (String) -> Unit): Modifier =
    accountAutofill(if (password) AutofillType.Password else AutofillType.Username, onFill)

@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun Modifier.accountAutofill(type: AutofillType, onFill: (String) -> Unit): Modifier {
    val autofill = LocalAutofill.current
    val tree = LocalAutofillTree.current
    val node =
        remember(type, onFill) { AutofillNode(autofillTypes = listOf(type), onFill = onFill) }
    DisposableEffect(node, tree) {
        tree += node
        onDispose { tree.children.remove(node.id) }
    }
    return onGloballyPositioned { node.boundingBox = it.boundsInWindow() }
        .onFocusChanged {
            if (it.isFocused) autofill?.requestAutofillForNode(node)
            else autofill?.cancelAutofillForNode(node)
        }
}
