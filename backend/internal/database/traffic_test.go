package database

import (
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTrafficConcurrentDurabilityAndOverflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueries(db)
	state, err := q.TrafficState()
	if err != nil || state.RecordingStartedAt == "" {
		t.Fatalf("%+v %v", state, err)
	}
	at := time.Date(2026, 10, 4, 23, 59, 59, 0, time.UTC)
	var wg sync.WaitGroup
	failures := make(chan error, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := q.AddTraffic(at, TrafficTotals{UploadedBytes: 7, DownloadedBytes: 11}); e != nil {
				failures <- e
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	if err = q.AddTraffic(at.Add(time.Second), TrafficTotals{DownloadedBytes: 17}); err != nil {
		t.Fatal(err)
	}
	allowance := int64(1000)
	if err = q.SetTrafficSettings(TrafficSettings{&allowance, 31, "combined"}); err != nil {
		t.Fatal(err)
	}
	if err = q.MarkTrafficDegraded(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q = NewQueries(db)
	got, err := q.TrafficState()
	if err != nil || !got.Degraded || got.RecordingStartedAt != state.RecordingStartedAt || *got.Settings.AllowanceBytes != 1000 || got.Settings.CycleStartDay != 31 || got.Settings.Basis != "combined" {
		t.Fatalf("%+v %v", got, err)
	}
	days, err := q.TrafficDays()
	if err != nil || len(days) != 2 {
		t.Fatalf("%+v %v", days, err)
	}
	if days[0].UploadedBytes != 224 || days[0].DownloadedBytes != 352 || days[1].DownloadedBytes != 17 {
		t.Fatal(days)
	}
	if err = q.AddTraffic(at, TrafficTotals{UploadedBytes: math.MaxInt64}); err == nil {
		t.Fatal("overflow was accepted")
	}
	if err = q.AddTraffic(at, TrafficTotals{UploadedBytes: -1}); err == nil {
		t.Fatal("negative traffic accepted")
	}
	days, _ = q.TrafficDays()
	if days[0].UploadedBytes != 224 {
		t.Fatal("failed write modified stored counter")
	}
	total := TrafficTotals{UploadedBytes: math.MaxInt64}
	if total.Add(TrafficTotals{DownloadedBytes: 1}) == nil {
		t.Fatal("sum overflow")
	}
}

func TestTrafficFileEventsSurviveDeletionAndReceiptRetry(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := NewQueries(db)
	until := time.Now().Add(time.Hour)
	if err = q.CreateSlot("slot", until, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sent", "received"} {
		if err = q.CreateTransfer(id, until, 0, nil); err != nil {
			t.Fatal(err)
		}
		if id == "received" {
			if err = q.LinkSlotTransfer("slot", id); err != nil {
				t.Fatal(err)
			}
		}
		if err = q.CreateFile(id+"-file", id, 9); err != nil {
			t.Fatal(err)
		}
		if q.CompleteTransfer(id) == nil {
			t.Fatal("finalized incomplete file")
		}
		if err = q.UpdateFileOffset(id+"-file", 9, true); err != nil {
			t.Fatal(err)
		}
		if err = q.CompleteTransfer(id); err != nil {
			t.Fatal(err)
		}
		if q.CompleteTransfer(id) == nil {
			t.Fatal("duplicate completion")
		}
		if ok, e := q.AcknowledgeDownload(id, time.Now()); e != nil || ok {
			t.Fatalf("premature ack %v %v", ok, e)
		}
		if ok, e := q.ReserveFileDownload(id, id+"-file"); e != nil || !ok {
			t.Fatalf("reserve %v %v", ok, e)
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				if ok, e := q.AcknowledgeDownload(id, time.Now()); e != nil || !ok {
					t.Errorf("ack %v %v", ok, e)
				}
			}(id)
		}
		wg.Wait()
		if err = q.DeleteTransfer(id); err != nil {
			t.Fatal(err)
		}
	}
	if err = q.DeleteSlot("slot"); err != nil {
		t.Fatal(err)
	}
	days, err := q.TrafficDays()
	if err != nil || len(days) != 1 {
		t.Fatalf("%+v %v", days, err)
	}
	got := days[0]
	if got.FilesUploaded != 2 || got.FilesDelivered != 2 || got.StandaloneFilesUploaded != 1 || got.ReceivedFilesUploaded != 1 || got.TotalBytes != 0 {
		t.Fatal(got)
	}
}
