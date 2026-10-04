package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

const dateLayout = "2006-01-02"

type trafficRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type trafficCycle struct {
	Start string `json:"start"`
	End   string `json:"end"` // exclusive
	database.TrafficTotals
	CountedBytes   int64  `json:"counted_bytes"`
	RemainingBytes *int64 `json:"remaining_bytes"`
}
type trafficReport struct {
	RecordingStartedAt string                   `json:"recording_started_at"`
	UpdatedAt          string                   `json:"updated_at"`
	Status             string                   `json:"status"`
	Timezone           string                   `json:"timezone"`
	Settings           database.TrafficSettings `json:"settings"`
	Today              database.TrafficTotals   `json:"today"`
	Month              database.TrafficTotals   `json:"month"`
	Lifetime           database.TrafficTotals   `json:"lifetime"`
	Range              trafficRange             `json:"range"`
	Totals             database.TrafficTotals   `json:"totals"`
	Days               []database.TrafficDay    `json:"days"`
	Cycle              trafficCycle             `json:"cycle"`
}

func calendarStart(now time.Time, day int) time.Time {
	now = now.UTC()
	last := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(now.Year(), now.Month(), day, 0, 0, 0, 0, time.UTC)
}
func billingCycle(now time.Time, day int) (time.Time, time.Time) {
	now = now.UTC()
	start := calendarStart(now, day)
	if now.Before(start) {
		previous := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
		return calendarStart(previous, day), start
	}
	next := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return start, calendarStart(next, day)
}
func (s *Server) trafficReport(now time.Time, from, to string) (trafficReport, error) {
	var report trafficReport
	state, err := s.queries.TrafficState()
	if err != nil {
		return report, err
	}
	days, err := s.queries.TrafficDays()
	if err != nil {
		return report, err
	}
	today := now.UTC().Format(dateLayout)
	month := calendarStart(now, 1).Format(dateLayout)
	start, end := billingCycle(now, state.Settings.CycleStartDay)
	report = trafficReport{
		RecordingStartedAt: state.RecordingStartedAt, UpdatedAt: state.UpdatedAt, Status: "ok", Timezone: "UTC",
		Settings: state.Settings, Range: trafficRange{from, to}, Days: []database.TrafficDay{},
		Cycle: trafficCycle{Start: start.Format(dateLayout), End: end.Format(dateLayout)},
	}
	if state.Degraded || s.trafficDegraded.Load() {
		report.Status = "degraded"
	}
	byDate := make(map[string]database.TrafficTotals, len(days))
	for _, d := range days {
		byDate[d.Date] = d.TrafficTotals
		targets := []*database.TrafficTotals{&report.Lifetime}
		if d.Date == today {
			targets = append(targets, &report.Today)
		}
		if d.Date >= month && d.Date <= today {
			targets = append(targets, &report.Month)
		}
		if d.Date >= report.Cycle.Start && d.Date < report.Cycle.End {
			targets = append(targets, &report.Cycle.TrafficTotals)
		}
		if d.Date >= from && d.Date <= to {
			targets = append(targets, &report.Totals)
		}
		for _, target := range targets {
			if err = target.Add(d.TrafficTotals); err != nil {
				return report, err
			}
		}
	}
	first, _ := time.Parse(dateLayout, from)
	last, _ := time.Parse(dateLayout, to)
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		date := day.Format(dateLayout)
		report.Days = append(report.Days, database.TrafficDay{Date: date, TrafficTotals: byDate[date]})
	}
	report.Cycle.CountedBytes = report.Cycle.DownloadedBytes
	if report.Settings.Basis == "combined" {
		report.Cycle.CountedBytes = report.Cycle.TotalBytes
	}
	if report.Settings.AllowanceBytes != nil {
		remaining := *report.Settings.AllowanceBytes - report.Cycle.CountedBytes
		if remaining < 0 {
			remaining = 0
		}
		report.Cycle.RemainingBytes = &remaining
	}
	return report, nil
}
func (s *Server) getTraffic(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if from == "" {
		from = calendarStart(now, 1).Format(dateLayout)
	}
	if to == "" {
		to = now.Format(dateLayout)
	}
	first, e1 := time.Parse(dateLayout, from)
	last, e2 := time.Parse(dateLayout, to)
	// Limit only chart response size. Measured lifetime totals remain unbounded.
	if e1 != nil || e2 != nil || last.Before(first) || last.Sub(first) > 366*24*time.Hour {
		writeError(w, 400, "choose a valid UTC date range of at most 367 days")
		return
	}
	report, err := s.trafficReport(now, from, to)
	if err != nil {
		writeError(w, 503, "traffic accounting is unavailable")
		return
	}
	writeJSON(w, 200, report)
}
func (s *Server) updateTrafficSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Allowance json.RawMessage `json:"allowance_bytes"`
		Day       *int            `json:"cycle_start_day"`
		Basis     *string         `json:"basis"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || len(input.Allowance) == 0 || input.Day == nil || input.Basis == nil {
		writeError(w, 400, "provide allowance_bytes, cycle_start_day, and basis")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, 400, "invalid traffic settings")
		return
	}
	settings := database.TrafficSettings{CycleStartDay: *input.Day, Basis: *input.Basis}
	if err := json.Unmarshal(input.Allowance, &settings.AllowanceBytes); err != nil {
		writeError(w, 400, "allowance must be a positive integer byte count or null")
		return
	}
	if settings.CycleStartDay < 1 || settings.CycleStartDay > 31 || (settings.Basis != "outbound" && settings.Basis != "combined") ||
		(settings.AllowanceBytes != nil && (*settings.AllowanceBytes <= 0 || *settings.AllowanceBytes > 9007199254740991)) {
		writeError(w, 400, "use day 1–31, outbound or combined basis, and a positive safe integer byte allowance or null")
		return
	}
	if err := s.queries.SetTrafficSettings(settings); err != nil {
		writeError(w, 503, "traffic settings could not be saved")
		return
	}
	writeJSON(w, 200, settings)
}
func (s *Server) getOverview(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	traffic, err := s.trafficReport(now, calendarStart(now, 1).Format(dateLayout), now.Format(dateLayout))
	if err != nil {
		writeError(w, 503, "administration metrics are unavailable")
		return
	}
	users, transfers, slots, stored, keys, err := s.queries.Overview(now)
	if err != nil {
		writeError(w, 503, "administration metrics are unavailable")
		return
	}
	for _, key := range keys {
		size, e := s.fileStore.Size(key)
		if e != nil || size < 0 || stored > math.MaxInt64-size {
			writeError(w, 503, "storage measurement is unavailable")
			return
		}
		stored += size
	}
	writeJSON(w, 200, map[string]any{
		"recording_started_at": traffic.RecordingStartedAt, "enabled_users": users,
		"active_transfers": transfers, "active_receive_links": slots, "stored_bytes": stored,
		"files_uploaded": traffic.Lifetime.FilesUploaded, "files_delivered": traffic.Lifetime.FilesDelivered,
		"standalone_files_uploaded": traffic.Lifetime.StandaloneFilesUploaded, "received_files_uploaded": traffic.Lifetime.ReceivedFilesUploaded,
		"traffic": traffic,
	})
}
