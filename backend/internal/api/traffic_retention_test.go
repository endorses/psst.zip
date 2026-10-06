package api

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestTrafficReportClockRollbackNeverShowsPartialCycle(t *testing.T) {
	db, err := openFixture(filepath.Join(t.TempDir(), "traffic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	q := database.NewQueries(db)
	s := &Server{queries: q}
	// Pruning on this future date puts the watermark at 2030-03-15. Rolling
	// back to March 20 leaves today's range covered but loses part of the month.
	floor := time.Date(2030, 3, 15, 0, 0, 0, 0, time.UTC)
	if _, err = q.PruneTrafficHistory(floor.AddDate(0, 0, database.TrafficHistoryRetentionDays-1)); err != nil {
		t.Fatal(err)
	}
	now := floor.AddDate(0, 0, 5)
	date := now.Format(dateLayout)
	if _, err = s.trafficReport(now, date, date); !errors.Is(err, database.ErrTrafficAccounting) {
		t.Fatal("partial month appeared complete", err)
	}
	// April calendar totals are covered, but a day-10 cycle includes pruned March.
	now = time.Date(2030, 4, 2, 0, 0, 0, 0, time.UTC)
	date = now.Format(dateLayout)
	if err = q.SetTrafficSettings(database.TrafficSettings{CycleStartDay: 10, Basis: "outbound"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.trafficReport(now, date, date); !errors.Is(err, database.ErrTrafficAccounting) {
		t.Fatal("partial cycle appeared complete", err)
	}
	if err = q.SetTrafficSettings(database.TrafficSettings{CycleStartDay: 20, Basis: "outbound"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.trafficReport(now, date, date); err != nil {
		t.Fatal("covered month and cycle were rejected", err)
	}
}
