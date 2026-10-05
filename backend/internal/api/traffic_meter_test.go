package api

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

type partialTrafficReader struct{ read bool }

func (r *partialTrafficReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	copy(p, "abc")
	return 3, io.ErrUnexpectedEOF
}
func (*partialTrafficReader) Close() error { return nil }

type failingTrafficWriter struct{ header http.Header }

func (w *failingTrafficWriter) Header() http.Header       { return w.header }
func (w *failingTrafficWriter) WriteHeader(int)           {}
func (w *failingTrafficWriter) Write([]byte) (int, error) { return 2, io.ErrClosedPipe }

func TestTrafficIOCountsPartialFailuresAndExcludesErrors(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "meter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := &Server{queries: database.NewQueries(db)}
	m := s.newTrafficMeter()
	body := trafficBody{&partialTrafficReader{}, m}
	n, err := body.Read(make([]byte, 8))
	if n != 3 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(n, err)
	}
	writer := &trafficWriter{ResponseWriter: &failingTrafficWriter{header: make(http.Header)}, meter: m}
	n, err = writer.Write([]byte("abcdef"))
	if n != 2 || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(n, err)
	}
	bad := &trafficWriter{ResponseWriter: httptest.NewRecorder(), meter: m}
	bad.WriteHeader(404)
	_, _ = bad.Write([]byte("error body"))
	m.close()
	days, err := s.queries.TrafficDays()
	if err != nil || len(days) != 1 || days[0].UploadedBytes != 3 || days[0].DownloadedBytes != 2 {
		t.Fatalf("%+v %v", days, err)
	}
}

func TestTrafficFlushBoundedAndFailureVisible(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "flush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := &Server{queries: database.NewQueries(db)}
	m := s.newTrafficMeter()
	defer m.close()
	m.add(1<<20, true)
	days, err := s.queries.TrafficDays()
	if err != nil || len(days) != 1 || days[0].UploadedBytes != 1<<20 {
		t.Fatalf("threshold did not flush %+v %v", days, err)
	}
	m.add(9, false)
	deadline := time.Now().Add(3 * time.Second)
	for {
		days, err = s.queries.TrafficDays()
		if err != nil {
			t.Fatal(err)
		}
		if days[0].DownloadedBytes == 9 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("periodic flush did not persist stalled stream")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = db.Exec("DROP TABLE traffic_days"); err != nil {
		t.Fatal(err)
	}
	m.add(1<<20, true)
	state, err := s.queries.TrafficState()
	if err != nil || !state.Degraded || !s.trafficDegraded.Load() {
		t.Fatalf("failure hidden %+v %v", state, err)
	}
}

func TestBillingCycleClampsShortMonthsAndUTC(t *testing.T) {
	for _, test := range []struct {
		now, start, end string
		day             int
	}{
		{"2026-02-27T12:00:00Z", "2026-01-31", "2026-02-28", 31},
		{"2026-02-28T00:00:00Z", "2026-02-28", "2026-03-31", 31},
		{"2028-02-29T00:00:00Z", "2028-02-29", "2028-03-31", 31},
		{"2026-12-31T23:59:00Z", "2026-12-31", "2027-01-31", 31},
		{"2026-03-01T00:00:00Z", "2026-03-01", "2026-04-01", 1},
	} {
		now, _ := time.Parse(time.RFC3339, test.now)
		start, end := billingCycle(now, test.day)
		if start.Format(dateLayout) != test.start || end.Format(dateLayout) != test.end {
			t.Fatalf("%+v got %v %v", test, start, end)
		}
	}
}
