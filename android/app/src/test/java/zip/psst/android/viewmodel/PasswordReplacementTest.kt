package zip.psst.android.viewmodel

import org.junit.Assert.*
import org.junit.Test

class PasswordReplacementTest {
    @Test
    fun requiresMatchingDifferentReplacementWithoutNormalizingPasswords() {
        assertNotNull(passwordReplacementError("temporary", "new", "different"))
        assertNotNull(passwordReplacementError("temporary", "temporary", "temporary"))
        assertNotNull(passwordReplacementError("", "new", "new"))
        assertNull(passwordReplacementError("temporary", " new password ", " new password "))
        assertNotNull(passwordReplacementError("temporary", " new password ", "new password"))
    }
}
