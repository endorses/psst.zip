package api

import (
	"database/sql"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/testutil"
)

// The external api_test TestMain cleans this process-wide template as well.
func openFixture(path string) (*sql.DB, error) { return testutil.OpenDatabase(path, database.Open) }
