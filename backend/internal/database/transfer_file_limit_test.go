package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestTransferFileListBoundsHistoricalMetadata(t *testing.T) {
	for _, count := range []int{0, 100, 101, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			q, _ := resourceFixture(t)
			if err := q.CreateTransfer("legacy", time.Now().Add(time.Hour), 3, nil); err != nil {
				t.Fatal(err)
			}
			// Model pre-limit/restored metadata without passing through HTTP creation's
			// current configurable per-transfer cap.
			seedHistoricalTransferFileRows(t, q, "legacy", "legacy-%04d", count, 7, 7, true, 2)
			files, err := q.ListFilesContext(context.Background(), "legacy")
			if count > MaxTransferFiles {
				if !errors.Is(err, ErrTransferFileLimit) || files != nil {
					t.Fatal("oversized transfer returned partial metadata", len(files), err)
				}
				return
			}
			if err != nil || len(files) != count {
				t.Fatal(len(files), err)
			}
			for i, file := range files {
				if file.ID != fmt.Sprintf("legacy-%04d", i) || file.Size != 7 || file.UploadOffset != 7 || !file.UploadComplete || file.DownloadCount != 2 {
					t.Fatal("supported transfer metadata changed", file)
				}
			}
		})
	}
}
func TestTransferFileListObservesCancellationAndIndexedScope(t *testing.T) {
	q, _ := resourceFixture(t)
	for _, id := range []string{"small", "large"} {
		if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.CreateFile("only", "small", 1); err != nil {
		t.Fatal(err)
	}
	seedTransferFileRows(t, q, "large", "%d", 101, 1, 0, false, 0)
	files, err := q.ListFiles("small")
	if err != nil || len(files) != 1 || files[0].ID != "only" {
		t.Fatal(files, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	files, err = q.ListFilesContext(ctx, "small")
	if !errors.Is(err, context.Canceled) || files != nil {
		t.Fatal("file query ignored request cancellation", files, err)
	}
}

func TestTransferFileAllocationSerializesProtocolCeiling(t *testing.T) {
	q, path := resourceFixture(t)
	until := time.Now().Add(time.Hour)
	if err := q.CreateReceiveSlot("slot", until, nil, "", 2, "key", 0); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlotTransfer("slot", "child", until, 0, nil); err != nil {
		t.Fatal(err)
	}
	seedTransferFileRows(t, q, "child", "%d", MaxTransferFiles-1, 60, 0, false, 0)
	otherDB, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, otherDB)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i, allocator := range []*Queries{q, NewQueries(otherDB)} {
		go func(i int, allocator *Queries) {
			<-start
			results <- allocator.CreateFileWithQuota(fmt.Sprintf("new-%d", i), "child", 60, 1<<20, 500)
		}(i, allocator)
	}
	close(start)
	success, denied := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrTransferFileQuota) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || denied != 1 {
		t.Fatal("concurrent allocation exceeded protocol ceiling", success, denied)
	}
	files, err := q.ListFiles("child")
	if err != nil || len(files) != MaxTransferFiles {
		t.Fatal(len(files), err)
	}
	var bytes, count int64
	if err := q.db.QueryRow(`SELECT reserved_bytes,reserved_files FROM slots WHERE id='slot'`).Scan(&bytes, &count); err != nil {
		t.Fatal(err)
	}
	if bytes != 60 || count != 1 {
		t.Fatal("denied allocation consumed lifetime allowances", bytes, count)
	}
}
