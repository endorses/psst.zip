package database

import (
	"errors"
	"testing"
	"time"
)

func TestCapacitySnapshotUsesBothQuotasAndOutstandingDiskReservations(t *testing.T) {
	q, _ := resourceFixture(t)
	for _, id := range []string{"alice", "bob"} {
		if err := q.CreateUser(User{ID: id, Username: id, Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
			t.Fatal(err)
		}
		if err := q.CreateTransfer(id, time.Now().Add(time.Hour), 0, nil, id); err != nil {
			t.Fatal(err)
		}
	}
	for id, size := range map[string]int64{"alice": 3 << 20, "bob": 8 << 20} {
		if err := q.CreateFile(id, id, size); err != nil {
			t.Fatal(err)
		}
	}
	p := policyForTest(t, q)
	p.ServerStorageBytes = 64 << 20
	p.AccountStorageBytes = 16 << 20
	p.ReserveDiskBytes = 1 << 20
	p.ReserveDiskPercent = 1
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	q.capacity.paths = []capacityPath{{path: "data", payload: true}, {path: "db", database: true}}
	q.capacity.probe = func(path string) (volumeCapacity, error) {
		if path == "data" {
			return volumeCapacity{1, 100 << 20, 20 << 20}, nil
		}
		return volumeCapacity{2, 100 << 20, 10 << 20}, nil
	}
	snapshot, err := q.ResourceSnapshot("alice")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Capacity.State != "ready" || snapshot.Capacity.AvailableWireBytes == nil || *snapshot.Capacity.AvailableWireBytes != 7<<20 || snapshot.Usage.ReservedBytes != 3<<20 {
		t.Fatalf("wrong scoped headroom: %+v", snapshot)
	}
	if err := q.CreateFile("disk-over", "alice", (7<<20)+1); !errors.Is(err, ErrDiskCapacity) {
		t.Fatalf("headroom disagrees with admission: %v", err)
	}
	p.AccountStorageBytes = 4 << 20
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	snapshot, err = q.ResourceSnapshot("alice")
	if err != nil || *snapshot.Capacity.AvailableWireBytes != 1<<20 {
		t.Fatalf("account quota: %+v %v", snapshot, err)
	}
	p.ServerStorageBytes = 11 << 20
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	snapshot, err = q.ResourceSnapshot("alice")
	if err != nil || snapshot.Capacity.State != "blocked" || snapshot.Capacity.Reason != "resource_limit" || *snapshot.Capacity.AvailableWireBytes != 0 {
		t.Fatalf("global quota: %+v %v", snapshot, err)
	}
}

func TestCapacitySnapshotDistinguishesUnknownFromDiskAndObjectExhaustion(t *testing.T) {
	q, _ := resourceFixture(t)
	snapshot, err := q.ResourceSnapshot("")
	if err != nil || snapshot.Capacity.State != "unknown" || snapshot.Capacity.AvailableWireBytes != nil {
		t.Fatalf("unconfigured probe: %+v %v", snapshot, err)
	}
	q.capacity.paths = []capacityPath{{path: "data", payload: true, database: true}}
	q.capacity.probe = func(string) (volumeCapacity, error) { return volumeCapacity{}, errors.New("offline") }
	snapshot, err = q.ResourceSnapshot("")
	if err != nil || snapshot.Capacity.State != "unknown" || snapshot.Capacity.AvailableWireBytes != nil {
		t.Fatalf("failed probe: %+v %v", snapshot, err)
	}
	q.capacity.probe = func(string) (volumeCapacity, error) { return volumeCapacity{1, 1 << 30, 1 << 20}, nil }
	snapshot, err = q.ResourceSnapshot("")
	if err != nil || snapshot.Capacity.State != "blocked" || snapshot.Capacity.Reason != "disk_capacity" || *snapshot.Capacity.AvailableWireBytes != 0 {
		t.Fatalf("disk exhaustion: %+v %v", snapshot, err)
	}
	q.capacity.probe = func(string) (volumeCapacity, error) { return volumeCapacity{1, 1 << 30, 1 << 30}, nil }
	p := policyForTest(t, q)
	p.ServerFiles = 1
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateTransfer("t", time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("empty", "t", 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err = q.ResourceSnapshot("")
	if err != nil || snapshot.Capacity.State != "blocked" || snapshot.Capacity.AvailableFiles != 0 || *snapshot.Capacity.AvailableWireBytes != 0 {
		t.Fatalf("empty file exhausts object headroom: %+v %v", snapshot, err)
	}
}
