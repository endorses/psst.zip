package database

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTrafficRetentionMigrationSeedsLifetimeOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, stmt := range migrations {
		if strings.Contains(stmt, "CREATE TABLE traffic_retention") {
			break
		}
		if _, err = db.Exec(stmt); err != nil {
			t.Fatal(i, err)
		}
		if i > 0 {
			if _, err = db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := time.Now().UTC()
	for _, date := range []string{now.AddDate(-2, 0, 0).Format("2006-01-02"), now.Format("2006-01-02")} {
		if _, err = db.Exec(`INSERT INTO traffic_days(date,uploaded_bytes,downloaded_bytes,files_uploaded,files_delivered,standalone_files_uploaded,received_files_uploaded) VALUES(?,11,17,5,3,2,3)`, date); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	for i := 0; i < 2; i++ {
		db, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		q := NewQueries(db)
		h, e := q.TrafficHistory(now)
		if e != nil {
			t.Fatal(e)
		}
		if h.Lifetime != (TrafficTotals{UploadedBytes: 22, DownloadedBytes: 34, TotalBytes: 56, FilesUploaded: 10, FilesDelivered: 6, StandaloneFilesUploaded: 4, ReceivedFilesUploaded: 6}) || len(h.Days) != 1 {
			t.Fatalf("%+v", h)
		}
		if _, e = q.PruneTrafficHistory(now); e != nil {
			t.Fatal(e)
		}
		db.Close()
	}
}

func TestTrafficRetentionBoundedBatchesLifetimeAndLateLeases(t *testing.T) {
	q, _ := resourceFixture(t)
	now := time.Date(2030, 3, 31, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 1200; i++ {
		date := now.AddDate(0, 0, -i)
		if err := q.AddTraffic(date, TrafficTotals{UploadedBytes: 1, DownloadedBytes: 2, FilesUploaded: 3, FilesDelivered: 1, StandaloneFilesUploaded: 1, ReceivedFilesUploaded: 2}); err != nil {
			t.Fatal(err)
		}
		for _, owner := range []string{"one", "two"} {
			if _, err := q.db.Exec(`INSERT INTO traffic_owner_days(owner,date,observed_up,observed_down,conservative_up,conservative_down) VALUES(?,?,1,2,3,4)`, owner, date.Format("2006-01-02")); err != nil {
				t.Fatal(err)
			}
		}
	}
	old := now.AddDate(0, 0, -1199)
	live, err := q.ReserveTraffic("one", false, 17, old)
	if err != nil {
		t.Fatal(err)
	}
	crash, err := q.ReserveTraffic("two", true, 19, old)
	if err != nil {
		t.Fatal(err)
	}
	if n, e := q.PruneTrafficHistory(now); e != nil || n != 1024 {
		t.Fatal(n, e)
	}
	h, err := q.TrafficHistory(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Days) != 400 || h.Lifetime.TotalBytes != 3600 || h.Lifetime.FilesUploaded != 3600 || h.RetainedFrom != now.AddDate(0, 0, -399).Format("2006-01-02") {
		t.Fatalf("%+v", h)
	}
	for i := 0; i < 8; i++ {
		n, e := q.PruneTrafficHistory(now)
		if e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			break
		}
		if n > 1024 {
			t.Fatal("unbounded maintenance", n)
		}
	}
	for table, want := range map[string]int{"traffic_days": 400, "traffic_owner_days": 800, "traffic_leases": 2} {
		var n int
		if e := q.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); e != nil || n != want {
			t.Fatal(table, n, e)
		}
	}
	if err = q.SettleTraffic(live.ID, 7); err != nil {
		t.Fatal(err)
	}
	if err = q.RecoverTrafficLeases(); err != nil {
		t.Fatal(err)
	}
	if err = q.RecoverTrafficLeases(); err != nil {
		t.Fatal(err)
	}
	_ = crash
	var up, down, cu, cd int64
	if err = q.db.QueryRow(`SELECT archived_observed_up,archived_observed_down,archived_conservative_up,archived_conservative_down FROM traffic_retention`).Scan(&up, &down, &cu, &cd); err != nil {
		t.Fatal(err)
	}
	if up != 1600 || down != 3207 || cu != 4819 || cd != 6400 {
		t.Fatal(up, down, cu, cd)
	}
	if err = q.AddTraffic(old, TrafficTotals{DownloadedBytes: 11}); err != nil {
		t.Fatal(err)
	}
	h, err = q.TrafficHistory(now)
	if err != nil || len(h.Days) != 400 || h.Lifetime.TotalBytes != 3611 {
		t.Fatal(h, err)
	}
	var n int
	if err = q.db.QueryRow(`SELECT COUNT(*) FROM traffic_owner_days WHERE date<?`, h.RetainedFrom).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err = q.db.QueryRow(`SELECT COUNT(*) FROM traffic_days WHERE date<?`, h.RetainedFrom).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestTrafficRetentionPreservesEveryBillingDayAndPreviousCycle(t *testing.T) {
	q, _ := resourceFixture(t)
	now := time.Date(2028, 3, 30, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 450; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		if _, err := q.db.Exec(`INSERT INTO traffic_owner_days(owner,date,observed_up,observed_down,conservative_up,conservative_down) VALUES('owner',?,?,?,3,4)`, date, i+1, i+2); err != nil {
			t.Fatal(err)
		}
	}
	p := DefaultTrafficPolicy()
	type key struct {
		day      int
		previous bool
		global   bool
	}
	before := map[key]TrafficBudgetUsage{}
	collect := func() map[key]TrafficBudgetUsage {
		t.Helper()
		got := map[key]TrafficBudgetUsage{}
		for day := 1; day <= 31; day++ {
			p.CycleStartDay = day
			cycle := TrafficBudgetCycle(now, day)
			for _, previous := range []bool{false, true} {
				if previous {
					cycle = TrafficBudgetCycle(cycle.Start.Add(-time.Second), day)
				}
				for _, global := range []bool{false, true} {
					u, _, err := trafficUsage(q.db, "owner", global, p, cycle)
					if err != nil {
						t.Fatal(err)
					}
					got[key{day, previous, global}] = u
				}
			}
		}
		return got
	}
	before = collect()
	if _, err := q.PruneTrafficHistory(now); err != nil {
		t.Fatal(err)
	}
	if after := collect(); !reflect.DeepEqual(before, after) {
		t.Fatal("retention changed cycle charges")
	}
}

func TestTrafficRetentionClockRollbackFailsClosedAndWatermarkMonotonic(t *testing.T) {
	q, _ := resourceFixture(t)
	now := time.Date(2030, 10, 4, 12, 0, 0, 0, time.UTC)
	if _, err := q.PruneTrafficHistory(now); err != nil {
		t.Fatal(err)
	}
	old := now.AddDate(-2, 0, 0)
	if _, err := q.PruneTrafficHistory(old); err != nil {
		t.Fatal(err)
	}
	floor, err := trafficRetainedFrom(q.db)
	if err != nil || floor != trafficRetentionStart(now) {
		t.Fatal(floor, err)
	}
	if _, err = q.ReserveTraffic("owner", false, 1, old); !errors.Is(err, ErrTrafficAccounting) {
		t.Fatal("rollback permitted traffic", err)
	}
	if _, err = q.TrafficBudgetSnapshot("owner", old); !errors.Is(err, ErrTrafficAccounting) {
		t.Fatal("rollback showed refunded cycle", err)
	}
	if _, err = q.TrafficHistory(old); err == nil {
		t.Fatal("rollback returned missing historical data as zero")
	}
}

func TestTrafficRetentionOverflowIsAtomic(t *testing.T) {
	q, _ := resourceFixture(t)
	now := time.Now().UTC()
	old := now.AddDate(-2, 0, 0).Format("2006-01-02")
	if _, err := q.db.Exec(`INSERT INTO traffic_owner_days(owner,date,observed_up) VALUES('owner',?,1)`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE traffic_retention SET archived_observed_up=?`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.PruneTrafficHistory(now); err == nil {
		t.Fatal("accepted overflowing rollup")
	}
	var n int
	var floor string
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM traffic_owner_days WHERE date=?`, old).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := q.db.QueryRow(`SELECT retained_from FROM traffic_retention`).Scan(&floor); err != nil || floor != "" {
		t.Fatal(floor, err)
	}
	if _, err := q.db.Exec(`UPDATE traffic_retention SET uploaded_bytes=?`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if err := q.AddTraffic(now, TrafficTotals{UploadedBytes: 1}); err == nil {
		t.Fatal("accepted overflowing lifetime counter")
	}
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM traffic_days`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestTrafficRetentionConcurrentSnapshotAndPrune(t *testing.T) {
	q, path := resourceFixture(t)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other := NewQueries(db)
	now := time.Now().UTC()
	old := now.AddDate(-2, 0, 0)
	for i := 0; i < 20; i++ {
		if err = q.AddTraffic(old, TrafficTotals{UploadedBytes: 1}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 40; i++ {
			if _, e := other.PruneTrafficHistory(now); e != nil {
				done <- e
				return
			}
			if e := other.AddTraffic(now, TrafficTotals{UploadedBytes: 1}); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 40; i++ {
		h, e := q.TrafficHistory(now)
		if e != nil {
			t.Fatal(e)
		}
		var daily int64
		for _, d := range h.Days {
			daily += d.UploadedBytes
		}
		if h.Lifetime.UploadedBytes != daily+20 {
			t.Fatal(fmt.Sprintf("torn snapshot: lifetime %d daily %d", h.Lifetime.UploadedBytes, daily))
		}
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
