package cleanup

import (
	"context"
	"database/sql"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
	"time"
)

// Run starts the production cleanup worker and blocks until ctx is cancelled.
func Run(ctx context.Context, db *sql.DB, fs store.FileStore, interval time.Duration) {
	NewWorker(database.NewQueries(db), fs, interval).Run(ctx)
}
