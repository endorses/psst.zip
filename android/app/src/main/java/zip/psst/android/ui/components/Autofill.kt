package zip.psst.android.ui.components

import androidx.compose.ui.Modifier
import androidx.compose.ui.autofill.ContentType
import androidx.compose.ui.semantics.contentType
import androidx.compose.ui.semantics.semantics

/** Material text fields deliver autofilled values through their existing onValueChange. */
fun Modifier.loginAutofill(password: Boolean): Modifier =
    accountAutofill(if (password) ContentType.Password else ContentType.Username)

fun Modifier.accountAutofill(type: ContentType): Modifier = semantics { contentType = type }
