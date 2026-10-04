package database

import (
	"database/sql"
	"errors"
	"math"
	"time"
)

type TrafficTotals struct {
	UploadedBytes           int64 `json:"uploaded_bytes"`
	DownloadedBytes         int64 `json:"downloaded_bytes"`
	TotalBytes              int64 `json:"total_bytes"`
	FilesUploaded           int64 `json:"files_uploaded"`
	FilesDelivered          int64 `json:"files_delivered"`
	StandaloneFilesUploaded int64 `json:"standalone_files_uploaded"`
	ReceivedFilesUploaded   int64 `json:"received_files_uploaded"`
}
type TrafficDay struct {
	Date string `json:"date"`
	TrafficTotals
}
type TrafficSettings struct {
	AllowanceBytes *int64 `json:"allowance_bytes"`
	CycleStartDay  int    `json:"cycle_start_day"`
	Basis          string `json:"basis"`
}
type TrafficState struct {
	RecordingStartedAt string
	UpdatedAt          string
	Degraded           bool
	Settings           TrafficSettings
}

func (t *TrafficTotals) Add(other TrafficTotals) error {
	dst := []*int64{&t.UploadedBytes, &t.DownloadedBytes, &t.FilesUploaded, &t.FilesDelivered, &t.StandaloneFilesUploaded, &t.ReceivedFilesUploaded}
	src := []int64{other.UploadedBytes, other.DownloadedBytes, other.FilesUploaded, other.FilesDelivered, other.StandaloneFilesUploaded, other.ReceivedFilesUploaded}
	for i, n := range src {
		if n < 0 || *dst[i] > math.MaxInt64-n {
			return errors.New("traffic counter overflow")
		}
		*dst[i] += n
	}
	if t.UploadedBytes > math.MaxInt64-t.DownloadedBytes {
		return errors.New("traffic counter overflow")
	}
	t.TotalBytes = t.UploadedBytes + t.DownloadedBytes
	return nil
}

type sqlExecutor interface {
	Exec(string, ...any) (sql.Result, error)
}

// All counters use integer CHECK constraints: SQLite must reject rather than
// silently promote overflowing integer additions to approximate floating point.
func addTraffic(tx sqlExecutor, at time.Time, t TrafficTotals) error {
	if err := new(TrafficTotals).Add(t); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE traffic_retention SET uploaded_bytes=uploaded_bytes+?,downloaded_bytes=downloaded_bytes+?,files_uploaded=files_uploaded+?,files_delivered=files_delivered+?,standalone_files_uploaded=standalone_files_uploaded+?,received_files_uploaded=received_files_uploaded+? WHERE id=1`, t.UploadedBytes, t.DownloadedBytes, t.FilesUploaded, t.FilesDelivered, t.StandaloneFilesUploaded, t.ReceivedFilesUploaded)
	if err != nil {
		return err
	}
	if n, e := result.RowsAffected(); e != nil {
		return e
	} else if n != 1 {
		return ErrTrafficAccounting
	}
	_, err = tx.Exec(`INSERT INTO traffic_days
 (date,uploaded_bytes,downloaded_bytes,files_uploaded,files_delivered,standalone_files_uploaded,received_files_uploaded)
 SELECT ?,?,?,?,?,?,? WHERE ? >= (SELECT retained_from FROM traffic_retention WHERE id=1) ON CONFLICT(date) DO UPDATE SET
 uploaded_bytes=uploaded_bytes+excluded.uploaded_bytes,
 downloaded_bytes=downloaded_bytes+excluded.downloaded_bytes,
 files_uploaded=files_uploaded+excluded.files_uploaded,
 files_delivered=files_delivered+excluded.files_delivered,
 standalone_files_uploaded=standalone_files_uploaded+excluded.standalone_files_uploaded,
 received_files_uploaded=received_files_uploaded+excluded.received_files_uploaded`,
		at.UTC().Format("2006-01-02"), t.UploadedBytes, t.DownloadedBytes, t.FilesUploaded, t.FilesDelivered, t.StandaloneFilesUploaded, t.ReceivedFilesUploaded, at.UTC().Format("2006-01-02"))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE traffic_state SET updated_at=? WHERE id=1`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (q *Queries) AddTraffic(at time.Time, t TrafficTotals) error {
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = addTraffic(tx, at, t); err != nil {
		return err
	}
	return tx.Commit()
}
func (q *Queries) MarkTrafficDegraded() error {
	_, err := q.db.Exec(`UPDATE traffic_state SET degraded=1 WHERE id=1`)
	return err
}
func (q *Queries) TrafficState() (TrafficState, error) {
	var s TrafficState
	var allowance sql.NullInt64
	err := q.db.QueryRow(`SELECT recording_started_at,updated_at,degraded,allowance_bytes,cycle_start_day,basis FROM traffic_state WHERE id=1`).Scan(&s.RecordingStartedAt, &s.UpdatedAt, &s.Degraded, &allowance, &s.Settings.CycleStartDay, &s.Settings.Basis)
	if allowance.Valid {
		s.Settings.AllowanceBytes = &allowance.Int64
	}
	return s, err
}
func (q *Queries) SetTrafficSettings(s TrafficSettings, actors ...*AdminActor) error {
	if s.CycleStartDay < 1 || s.CycleStartDay > 31 || (s.Basis != "outbound" && s.Basis != "combined") || (s.AllowanceBytes != nil && (*s.AllowanceBytes <= 0 || *s.AllowanceBytes > 9007199254740991)) {
		return errors.New("invalid traffic settings")
	}
	tx, err := q.beginAdminMutation(actors)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE traffic_state SET allowance_bytes=?,cycle_start_day=?,basis=? WHERE id=1`, s.AllowanceBytes, s.CycleStartDay, s.Basis)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (q *Queries) TrafficDays() ([]TrafficDay, error) {
	rows, err := q.db.Query(`SELECT date,uploaded_bytes,downloaded_bytes,files_uploaded,files_delivered,standalone_files_uploaded,received_files_uploaded FROM traffic_days WHERE date >= (SELECT retained_from FROM traffic_retention WHERE id=1) ORDER BY date LIMIT 400`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []TrafficDay{}
	for rows.Next() {
		var d TrafficDay
		if err = rows.Scan(&d.Date, &d.UploadedBytes, &d.DownloadedBytes, &d.FilesUploaded, &d.FilesDelivered, &d.StandaloneFilesUploaded, &d.ReceivedFilesUploaded); err != nil {
			return nil, err
		}
		if err = d.Add(TrafficTotals{}); err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}

// Overview returns current gauges separately from durable lifetime counters.
// Actual blob sizes are measured by the storage layer, including partial files.
func (q *Queries) Overview(now time.Time) (users, transfers, slots int, manifestBytes int64, blobKeys []string, err error) {
	err = q.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='user' AND disabled=0`).Scan(&users)
	if err != nil {
		return
	}
	for _, kind := range []string{"transfers", "slots"} {
		var rows *sql.Rows
		rows, err = q.db.Query("SELECT expires_at FROM " + kind + " WHERE status!='revoked'")
		if err != nil {
			return
		}
		for rows.Next() {
			var expires time.Time
			err = rows.Scan(&expires)
			if err != nil {
				rows.Close()
				return
			}
			if now.Before(expires) {
				if kind == "transfers" {
					transfers++
				} else {
					slots++
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return
		}
	}
	err = q.db.QueryRow(`SELECT COALESCE(SUM(length(data)),0) FROM manifests`).Scan(&manifestBytes)
	if err != nil {
		return
	}
	var rows *sql.Rows
	rows, err = q.db.Query(`SELECT transfer_id,id FROM files`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var transfer, id string
		err = rows.Scan(&transfer, &id)
		if err != nil {
			return
		}
		blobKeys = append(blobKeys, transfer+"/"+id)
	}
	err = rows.Err()
	return
}
