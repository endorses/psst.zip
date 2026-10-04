package database

import (
	"database/sql"
	"errors"
	"time"
)

const TrafficHistoryRetentionDays = 400
const trafficRetentionBatch = 512

// Historical chart detail is finite; lifetime counters are independent of rows
// that maintenance removes. The watermark never moves backwards after pruning.
func trafficRetentionMigration() string {
	return `CREATE TABLE traffic_retention (
 id INTEGER PRIMARY KEY CHECK(id=1), retained_from TEXT NOT NULL DEFAULT '',
 uploaded_bytes INTEGER NOT NULL DEFAULT 0 CHECK(typeof(uploaded_bytes)='integer' AND uploaded_bytes>=0),
 downloaded_bytes INTEGER NOT NULL DEFAULT 0 CHECK(typeof(downloaded_bytes)='integer' AND downloaded_bytes>=0),
 files_uploaded INTEGER NOT NULL DEFAULT 0 CHECK(typeof(files_uploaded)='integer' AND files_uploaded>=0),
 files_delivered INTEGER NOT NULL DEFAULT 0 CHECK(typeof(files_delivered)='integer' AND files_delivered>=0),
 standalone_files_uploaded INTEGER NOT NULL DEFAULT 0 CHECK(typeof(standalone_files_uploaded)='integer' AND standalone_files_uploaded>=0),
 received_files_uploaded INTEGER NOT NULL DEFAULT 0 CHECK(typeof(received_files_uploaded)='integer' AND received_files_uploaded>=0),
 archived_observed_up INTEGER NOT NULL DEFAULT 0 CHECK(typeof(archived_observed_up)='integer' AND archived_observed_up>=0),
 archived_observed_down INTEGER NOT NULL DEFAULT 0 CHECK(typeof(archived_observed_down)='integer' AND archived_observed_down>=0),
 archived_conservative_up INTEGER NOT NULL DEFAULT 0 CHECK(typeof(archived_conservative_up)='integer' AND archived_conservative_up>=0),
 archived_conservative_down INTEGER NOT NULL DEFAULT 0 CHECK(typeof(archived_conservative_down)='integer' AND archived_conservative_down>=0)
 );
 INSERT INTO traffic_retention(id,uploaded_bytes,downloaded_bytes,files_uploaded,files_delivered,standalone_files_uploaded,received_files_uploaded)
 SELECT 1,COALESCE(SUM(uploaded_bytes),0),COALESCE(SUM(downloaded_bytes),0),COALESCE(SUM(files_uploaded),0),COALESCE(SUM(files_delivered),0),COALESCE(SUM(standalone_files_uploaded),0),COALESCE(SUM(received_files_uploaded),0) FROM traffic_days;`
}

func trafficRetentionStart(now time.Time) string {
	return now.UTC().AddDate(0, 0, 1-TrafficHistoryRetentionDays).Format("2006-01-02")
}

func trafficRetainedFrom(q trafficQuerier) (string, error) {
	var floor string
	err := q.QueryRow(`SELECT retained_from FROM traffic_retention WHERE id=1`).Scan(&floor)
	return floor, err
}

// PruneTrafficHistory advances the coverage watermark and removes a fixed batch
// of expired detail in one transaction. Leases are never deleted here: a live
// response may still settle them, or startup will conservatively recover them.
// Repeated calls drain pre-upgrade backlog without unbounded writer transactions.
func (q *Queries) PruneTrafficHistory(now time.Time) (int64, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE traffic_retention SET retained_from=MAX(retained_from,?) WHERE id=1`, trafficRetentionStart(now)); err != nil {
		return 0, err
	}
	floor, err := trafficRetainedFrom(tx)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(`SELECT owner,date,observed_up,observed_down,conservative_up,conservative_down FROM traffic_owner_days WHERE date<? ORDER BY date,owner LIMIT ?`, floor, trafficRetentionBatch)
	if err != nil {
		return 0, err
	}
	type entry struct {
		owner, date      string
		up, down, cu, cd int64
	}
	batch := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.owner, &e.date, &e.up, &e.down, &e.cu, &e.cd); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, e := range batch {
		if err = archiveBudgetTraffic(tx, e.up, e.down, e.cu, e.cd); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(`DELETE FROM traffic_owner_days WHERE owner=? AND date=?`, e.owner, e.date); err != nil {
			return 0, err
		}
	}
	result, err := tx.Exec(`DELETE FROM traffic_days WHERE date IN (SELECT date FROM traffic_days WHERE date<? ORDER BY date LIMIT ?)`, floor, trafficRetentionBatch)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return n + int64(len(batch)), nil
}

func archiveBudgetTraffic(tx sqlExecutor, up, down, cu, cd int64) error {
	_, err := tx.Exec(`UPDATE traffic_retention SET archived_observed_up=archived_observed_up+?,archived_observed_down=archived_observed_down+?,archived_conservative_up=archived_conservative_up+?,archived_conservative_down=archived_conservative_down+? WHERE id=1`, up, down, cu, cd)
	return err
}

type TrafficHistorySnapshot struct {
	State        TrafficState
	Lifetime     TrafficTotals
	Days         []TrafficDay
	RetainedFrom string
}

// TrafficHistory reads counters and retained detail under one snapshot so a
// concurrent maintenance pass cannot lose or double-count a lifetime total.
func (q *Queries) TrafficHistory(now time.Time) (TrafficHistorySnapshot, error) {
	var h TrafficHistorySnapshot
	tx, err := q.db.Begin()
	if err != nil {
		return h, err
	}
	defer tx.Rollback()
	var allowance sql.NullInt64
	if err = tx.QueryRow(`SELECT recording_started_at,updated_at,degraded,allowance_bytes,cycle_start_day,basis FROM traffic_state WHERE id=1`).Scan(&h.State.RecordingStartedAt, &h.State.UpdatedAt, &h.State.Degraded, &allowance, &h.State.Settings.CycleStartDay, &h.State.Settings.Basis); err != nil {
		return h, err
	}
	if allowance.Valid {
		h.State.Settings.AllowanceBytes = &allowance.Int64
	}
	if err = tx.QueryRow(`SELECT retained_from,uploaded_bytes,downloaded_bytes,files_uploaded,files_delivered,standalone_files_uploaded,received_files_uploaded FROM traffic_retention WHERE id=1`).Scan(&h.RetainedFrom, &h.Lifetime.UploadedBytes, &h.Lifetime.DownloadedBytes, &h.Lifetime.FilesUploaded, &h.Lifetime.FilesDelivered, &h.Lifetime.StandaloneFilesUploaded, &h.Lifetime.ReceivedFilesUploaded); err != nil {
		return h, err
	}
	h.RetainedFrom = max(h.RetainedFrom, trafficRetentionStart(now))
	if now.UTC().Format("2006-01-02") < h.RetainedFrom {
		return h, errors.New("traffic history clock is outside retained coverage")
	}
	if err = h.Lifetime.Add(TrafficTotals{}); err != nil {
		return h, err
	}
	rows, err := tx.Query(`SELECT date,uploaded_bytes,downloaded_bytes,files_uploaded,files_delivered,standalone_files_uploaded,received_files_uploaded FROM traffic_days WHERE date>=? AND date<=? ORDER BY date LIMIT ?`, h.RetainedFrom, now.UTC().Format("2006-01-02"), TrafficHistoryRetentionDays)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	h.Days = []TrafficDay{}
	for rows.Next() {
		var d TrafficDay
		if err = rows.Scan(&d.Date, &d.UploadedBytes, &d.DownloadedBytes, &d.FilesUploaded, &d.FilesDelivered, &d.StandaloneFilesUploaded, &d.ReceivedFilesUploaded); err != nil {
			return h, err
		}
		if err = d.Add(TrafficTotals{}); err != nil {
			return h, err
		}
		h.Days = append(h.Days, d)
	}
	return h, rows.Err()
}
