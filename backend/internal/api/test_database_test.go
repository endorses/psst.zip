package api_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Run(m)) }

func openFixture(path string) (*sql.DB, error) { return testutil.OpenDatabase(path, database.Open) }
