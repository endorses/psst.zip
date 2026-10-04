package database

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func resourceFixture(t *testing.T) (*Queries, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resources.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewQueries(db), path
}
func policyForTest(t *testing.T, q *Queries) ResourcePolicy {
	t.Helper()
	p, err := q.ResourcePolicy()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestResourceDefaultsAndValidation(t *testing.T) {
	q, _ := resourceFixture(t)
	p := policyForTest(t, q)
	if p.ServerStorageBytes != 10<<30 || p.AccountStorageBytes != 2<<30 || p.ServerFiles != 10000 || p.AccountFiles != 1000 || p.ServerTransfers != 2000 || p.AccountTransfers != 200 || p.ServerSlots != 500 || p.AccountSlots != 50 || p.MaxRetentionSeconds != 7*86400 || p.PendingUploadSeconds != 86400 || p.ReserveDiskBytes != 256<<20 || p.ReserveDiskPercent != 5 {
		t.Fatalf("defaults: %+v", p)
	}
	for _, change := range []func(*ResourcePolicy){func(p *ResourcePolicy) { p.ServerStorageBytes = 0 }, func(p *ResourcePolicy) { p.AccountFiles = 0 }, func(p *ResourcePolicy) { p.ServerTransfers = 1000001 }, func(p *ResourcePolicy) { p.MaxRetentionSeconds = 59 }, func(p *ResourcePolicy) { p.PendingUploadSeconds = p.MaxRetentionSeconds + 1 }, func(p *ResourcePolicy) { p.ReserveDiskPercent = 0 }} {
		bad := p
		change(&bad)
		if q.SetResourcePolicy(bad) == nil {
			t.Fatalf("accepted invalid policy %+v", bad)
		}
	}
}
func TestResourceStorageConcurrentAcrossConnectionsAndRestart(t *testing.T) {
	q, path := resourceFixture(t)
	p := policyForTest(t, q)
	p.ServerStorageBytes = 2 << 20
	p.AccountStorageBytes = 2 << 20
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("t", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	other := NewQueries(second)
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			writer := q
			if i%2 == 0 {
				writer = other
			}
			results <- writer.CreateFile(fmt.Sprint(i), "t", 512<<10)
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrResourceLimit) {
			t.Fatal(err)
		}
	}
	if success != 4 {
		t.Fatalf("allocations=%d", success)
	}
	u, err := q.ResourceUsage("")
	if err != nil || u.ReservedBytes != 2<<20 || u.OccupiedBytes != 0 || u.Files != 4 {
		t.Fatalf("usage %+v %v", u, err)
	}
	if err := q.UpdateFileOffset("0", 0, false); err != nil {
		t.Fatal(err)
	} // May be an unallocated contender; no capacity is released.
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, err := NewQueries(reopened).ResourceUsage("")
	if err != nil || actual != u {
		t.Fatalf("restart usage %+v %v", actual, err)
	}
	loaded, err := NewQueries(reopened).ResourcePolicy()
	if err != nil || loaded != p {
		t.Fatalf("restart policy %+v %v", loaded, err)
	}
}
func TestResourceOwnerAttributionReplacementAndLowering(t *testing.T) {
	q, _ := resourceFixture(t)
	for _, id := range []string{"alice", "bob"} {
		if err := q.CreateUser(User{ID: id, Username: id, Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
			t.Fatal(err)
		}
	}
	p := policyForTest(t, q)
	p.AccountStorageBytes = 2 << 20
	p.AccountFiles = 2
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateReceiveSlot("inbox", time.Now().Add(time.Hour), nil, "alice", 2, "key", 0); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateSlotTransfer("inbox", "child", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("own", time.Now().Add(time.Hour), 0, nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFileWithQuota("payload", "child", 1<<20, 5<<30); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("own", make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("own", make([]byte, 1<<20)); err != nil {
		t.Fatal("replacement wrongly double charged", err)
	}
	if err := q.SaveManifest("own", make([]byte, (1<<20)+1)); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("growth %v", err)
	}
	if err := q.CreateFile("zero", "own", 0); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("too-many", "own", 0); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("empty object bypass %v", err)
	}
	if err := q.CreateTransfer("bob", time.Now().Add(time.Hour), 0, nil, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("bob-file", "bob", 1<<20); err != nil {
		t.Fatal("other account unexpectedly blocked", err)
	}
	p.AccountStorageBytes = 1 << 20
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("own", make([]byte, 512<<10)); err != nil {
		t.Fatal("shrinking denied", err)
	}
	if err := q.SaveManifest("own", make([]byte, (512<<10)+1)); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("growth after reduction %v", err)
	}
	u, err := q.ResourceUsage("alice")
	if err != nil || u.ReservedBytes != 1536<<10 || u.Files != 2 || u.Transfers != 2 || u.Slots != 1 {
		t.Fatalf("owner usage %+v %v", u, err)
	}
	if err := q.ReleasePayloads("child"); err != nil {
		t.Fatal(err)
	}
	u, err = q.ResourceUsage("alice")
	if err != nil || u.ReservedBytes != 512<<10 {
		t.Fatalf("released usage %+v %v", u, err)
	}
	slot, err := q.GetSlot("inbox")
	if err != nil || slot.ReservedBytes != 1<<20 || slot.ReservedFiles != 1 {
		t.Fatalf("cumulative refunded %+v %v", slot, err)
	}
}
func TestResourceObjectBudgetsAndRetention(t *testing.T) {
	q, _ := resourceFixture(t)
	p := policyForTest(t, q)
	p.ServerTransfers = 2
	p.AccountTransfers = 1
	p.ServerSlots = 1
	p.AccountSlots = 1
	p.MaxRetentionSeconds = 120
	p.PendingUploadSeconds = 60
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("long", time.Now().Add(121*time.Second), 0, nil); !errors.Is(err, ErrRetentionLimit) {
		t.Fatalf("retention %v", err)
	}
	if err := q.CreateTransfer("t", time.Now().Add(119*time.Second), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("extra", time.Now().Add(time.Minute), 0, nil); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("transfer count %v", err)
	}
	if err := q.CreateSlot("slot", time.Now().Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateReceiveSlot("extra-slot", time.Now().Add(time.Minute), nil, "", 2, "key", 0); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("slot count %v", err)
	}
	item, err := q.GetTransfer("t")
	if err != nil || !item.PendingExpiresAt.Valid || time.Until(item.PendingExpiresAt.Time) > 61*time.Second {
		t.Fatalf("deadline %+v %v", item, err)
	}
	if _, err := q.db.Exec(`UPDATE transfers SET pending_expires_at=datetime('now','-1 second') WHERE id='t'`); err != nil {
		t.Fatal(err)
	}
	ids, err := q.ExpiredTransferIDs()
	if err != nil || len(ids) != 1 || ids[0] != "t" {
		t.Fatalf("pending cleanup %v %v", ids, err)
	}
}
func TestResourceCapacityUsesDistinctVolumesAndOutstandingReservations(t *testing.T) {
	q, _ := resourceFixture(t)
	p := policyForTest(t, q)
	p.ReserveDiskBytes = 1 << 20
	p.ReserveDiskPercent = 1
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("t", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	q.capacity.paths = []capacityPath{{path: "data", payload: true}, {path: "db", database: true}}
	freeData := int64(5 << 20)
	freeDB := int64(3 << 20)
	q.capacity.probe = func(path string) (volumeCapacity, error) {
		if path == "data" {
			return volumeCapacity{1, 100 << 20, freeData}, nil
		}
		return volumeCapacity{2, 100 << 20, freeDB}, nil
	}
	if err := q.CreateFile("file", "t", 3<<20); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("over", "t", 1); !errors.Is(err, ErrDiskCapacity) {
		t.Fatalf("outstanding reservations overcommitted %v", err)
	}
	// File bytes do not get charged against the separate DB volume.
	if err := q.SaveManifest("t", make([]byte, (512<<10)+1)); !errors.Is(err, ErrDiskCapacity) {
		t.Fatalf("DB replacement reserve %v", err)
	}
	freeData = 1 << 20
	if err := q.CheckWriteCapacity(); !errors.Is(err, ErrDiskCapacity) {
		t.Fatalf("streaming pressure ignored %v", err)
	}
	freeData = 5 << 20
	freeDB = 1 << 20
	if err := q.CreateTransfer("another", time.Now().Add(time.Hour), 0, nil); !errors.Is(err, ErrDiskCapacity) {
		t.Fatalf("DB metadata pressure ignored %v", err)
	}
}

func TestDefaultResourceReserveOnSmallAndNearlyFullDisks(t *testing.T) {
	for _, test := range []struct {
		name             string
		total, available int64
		allowed          bool
	}{
		{"below minimum reserve", 128 << 20, 128 << 20, false},
		{"small usable volume", 512 << 20, 300 << 20, true},
		{"large volume below percentage reserve", 100 << 30, 3 << 30, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			q, _ := resourceFixture(t)
			q.capacity.paths = []capacityPath{{path: "data", payload: true, database: true}}
			q.capacity.probe = func(string) (volumeCapacity, error) {
				return volumeCapacity{1, test.total, test.available}, nil
			}
			err := q.CreateTransfer("admission", time.Now().Add(time.Hour), 0, nil)
			if test.allowed && err != nil {
				t.Fatal(err)
			}
			if !test.allowed && !errors.Is(err, ErrDiskCapacity) {
				t.Fatalf("default reserve was not enforced: %v", err)
			}
		})
	}
}
