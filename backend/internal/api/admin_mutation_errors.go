package api

import (
	"errors"
	"net/http"

	"github.com/endorses/psst.zip/backend/internal/database"
)

// Preserve actionable authentication failures without changing unrelated
// storage, policy-validation, or missing-resource responses.
func rejectAdminMutation(w http.ResponseWriter, err error) bool {
	if errors.Is(err, database.ErrAdminAuthenticationChanged) || errors.Is(err, database.ErrAdminRecentRequired) {
		adminSecurityFailure(w, err)
		return true
	}
	return false
}
