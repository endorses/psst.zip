package database

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func completeDownloadFixture(t *testing.T, q *Queries, limit, files int) {
	t.Helper()
	if err := q.CreateTransfer("download", time.Now().Add(time.Hour), limit, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"}[:files] {
		if err := q.CreateFile(id, "download", 0); err != nil {
			t.Fatal(err)
		}
		if err := q.UpdateFileOffset(id, 0, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.CompleteTransfer("download"); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadReservationRejectsInvalidAccountingWithoutMutation(t *testing.T) {
	for _, statement := range []string{
		`UPDATE files SET download_count=9223372036854775807 WHERE id='first'`,
		`UPDATE files SET download_count=-1 WHERE id='first'`,
		`UPDATE files SET download_count=1.25 WHERE id='first'`,
		`UPDATE files SET download_count='invalid' WHERE id='first'`,
		`UPDATE files SET download_count=-1 WHERE id='second'`,
		`UPDATE files SET download_count=1.25 WHERE id='second'`,
		`UPDATE transfers SET max_downloads=-1`,
		`UPDATE transfers SET max_downloads=2147483648`,
		`UPDATE transfers SET max_downloads=1.25`,
	} {
		t.Run(statement, func(t *testing.T) {
			q, _ := resourceFixture(t)
			completeDownloadFixture(t, q, 0, 2)
			if _, err := q.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			var first, second any
			if err := q.db.QueryRow(`SELECT a.download_count,b.download_count FROM files a,files b WHERE a.id='first' AND b.id='second'`).Scan(&first, &second); err != nil {
				t.Fatal(err)
			}
			if allowed, err := q.ReserveFileDownload("download", "first"); allowed || !errors.Is(err, ErrDownloadCounter) {
				t.Fatal("invalid accounting authorized a response", allowed, err)
			}
			var afterFirst, afterSecond any
			var aggregate int64
			if err := q.db.QueryRow(`SELECT a.download_count,b.download_count,t.download_count FROM files a,files b,transfers t WHERE a.id='first' AND b.id='second' AND t.id='download'`).Scan(&afterFirst, &afterSecond, &aggregate); err != nil {
				t.Fatal(err)
			}
			if afterFirst != first || afterSecond != second || aggregate != 0 {
				t.Fatal("failed reservation changed counters", afterFirst, afterSecond, aggregate)
			}
		})
	}
}

func TestDownloadReservationFinalIntegerAttemptAcrossConnections(t *testing.T) {
	q, path := resourceFixture(t)
	completeDownloadFixture(t, q, 0, 1)
	if _, err := q.db.Exec(`UPDATE files SET download_count=?`, int64(math.MaxInt64-1)); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, other)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, allocator := range []*Queries{q, NewQueries(other)} {
		workers.Add(1)
		go func(allocator *Queries) {
			defer workers.Done()
			<-start
			allowed, err := allocator.ReserveFileDownload("download", "first")
			if err == nil && !allowed {
				err = errors.New("integer exhaustion was reported as a user download limit")
			}
			results <- err
		}(allocator)
	}
	close(start)
	workers.Wait()
	close(results)
	accepted, denied := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrDownloadCounter) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || denied != 1 {
		t.Fatal("concurrent requests exceeded integer headroom", accepted, denied)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, reopened)
	var fileCount, aggregate int64
	var kind string
	if err := reopened.QueryRow(`SELECT f.download_count,typeof(f.download_count),t.download_count FROM files f JOIN transfers t ON t.id=f.transfer_id WHERE f.id='first'`).Scan(&fileCount, &kind, &aggregate); err != nil {
		t.Fatal(err)
	}
	if fileCount != math.MaxInt64 || aggregate != math.MaxInt64 || kind != "integer" {
		t.Fatal("counter overflowed or changed storage type", fileCount, kind, aggregate)
	}
	if allowed, err := NewQueries(reopened).ReserveFileDownload("download", "first"); allowed || !errors.Is(err, ErrDownloadCounter) {
		t.Fatal("reopening replenished integer headroom", allowed, err)
	}
}

func TestDownloadReservationPreservesOrdinaryLimitDenials(t *testing.T) {
	q, _ := resourceFixture(t)
	completeDownloadFixture(t, q, 1, 2)
	if allowed, err := q.ReserveFileDownload("download", "first"); !allowed || err != nil {
		t.Fatal(allowed, err)
	}
	if allowed, err := q.ReserveFileDownload("download", "first"); allowed || err != nil {
		t.Fatal("normal exhaustion must stay distinct from invalid accounting", allowed, err)
	}
	if allowed, err := q.ReserveFileDownload("download", "second"); !allowed || err != nil {
		t.Fatal("one exhausted file blocked another", allowed, err)
	}
	if allowed, err := q.ReserveFileDownload("download", "missing"); allowed || err != nil {
		t.Fatal("missing file must not allocate an attempt", allowed, err)
	}
}
