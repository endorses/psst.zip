package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func guestCapacityFixture(t *testing.T) *Queries {
	t.Helper()
	q, _ := resourceFixture(t)
	if err := q.CreateUser(User{ID: "owner", Username: "private-owner", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateReceiveSlot("slot", time.Now().Add(time.Hour), nil, "owner", 2, "public-receive-key", 0); err != nil {
		t.Fatal(err)
	}
	p := policyForTest(t, q)
	p.ServerStorageBytes = 64 << 20
	p.AccountStorageBytes = 32 << 20
	p.ReserveDiskBytes = 1 << 20
	p.ReserveDiskPercent = 1
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	q.capacity.paths = []capacityPath{{path: "shared", payload: true, database: true}}
	q.capacity.probe = func(string) (volumeCapacity, error) { return volumeCapacity{1, 100 << 20, 100 << 20}, nil }
	return q
}
func guestSnapshot(t *testing.T, q *Queries, l GuestCapacityLimits) GuestUploadCapacity {
	t.Helper()
	s, err := q.GuestSlotCapacity(context.Background(), "slot", l)
	if err != nil {
		t.Fatal(err)
	}
	return s.Capacity
}
func assertGuestCapacity(t *testing.T, c GuestUploadCapacity, state, reason string, bytes, files int64) {
	t.Helper()
	if c.State != state || c.Reason != reason {
		t.Fatalf("capacity %+v, want %s/%s", c, state, reason)
	}
	if state == "unknown" {
		if c.AvailableWireBytes != nil || c.AvailableFiles != nil {
			t.Fatal("unknown must not expose numeric allowances", c)
		}
		return
	}
	if c.AvailableWireBytes == nil || *c.AvailableWireBytes != bytes || c.AvailableFiles == nil || *c.AvailableFiles != files {
		t.Fatalf("capacity %+v bytes=%v files=%v want %d/%d", c, c.AvailableWireBytes, c.AvailableFiles, bytes, files)
	}
}
func TestGuestCapacityCanonicalScopesAndManifestReserve(t *testing.T) {
	q := guestCapacityFixture(t)
	if err := q.CreateTransfer("existing", time.Now().Add(time.Hour), 0, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("payload", "existing", 3<<20); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveManifest("existing", []byte(strings.Repeat("m", 1024))); err != nil {
		t.Fatal(err)
	}
	// Derived summaries and unrelated payload issues cannot control admission.
	if err := q.RecordFileReconciliationIssue(context.Background(), "payload", "failed", "inspect_failed"); err != nil {
		t.Fatal(err)
	}
	c := guestSnapshot(t, q, GuestCapacityLimits{})
	assertGuestCapacity(t, c, "ready", "", (28<<20)-1024, 100)
	if c.CheckedAt.Location() != time.UTC || c.ManifestReserveBytes != MaxManifestBytes {
		t.Fatal(c)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"owner", "usage", "policy", "transfers", "slots", "scope", "existing", "payload"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private capacity metadata exposed", string(raw))
		}
	}
	// Reducing a global quota also limits the same owner snapshot.
	p := policyForTest(t, q)
	p.ServerStorageBytes = 8 << 20
	if err = q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "ready", "", (4<<20)-1024, 100)
	// Cumulative link counters never refill when canonical payload usage falls.
	if _, err = q.db.Exec(`UPDATE slots SET reserved_bytes=?,reserved_files=2,max_files=5,upload_count=1 WHERE id='slot'`, int64(1<<20)); err != nil {
		t.Fatal(err)
	}
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{SlotBytes: 3 << 20, SlotTransfers: 2}), "ready", "", 1<<20, 3)
}

func TestGuestCapacityKnownLimitsPrecedeUnknownDisk(t *testing.T) {
	tests := []struct {
		name, sql, reason string
		limits            GuestCapacityLimits
	}{
		{"link-bytes", `UPDATE slots SET reserved_bytes=5*1024*1024*1024`, "link_limit", GuestCapacityLimits{}},
		{"manifest-link", `SELECT 1`, "link_limit", GuestCapacityLimits{SlotBytes: MaxManifestBytes + 59}},
		{"link-files", `UPDATE slots SET max_files=1,reserved_files=1`, "link_limit", GuestCapacityLimits{}},
		{"link-transfers", `UPDATE slots SET upload_count=20`, "link_limit", GuestCapacityLimits{}},
		{"server-bytes", `UPDATE resource_policy SET server_storage_bytes=1048576`, "capacity_limit", GuestCapacityLimits{}},
		{"account-bytes", `UPDATE resource_policy SET account_storage_bytes=1048576`, "capacity_limit", GuestCapacityLimits{}},
		{"server-files", `UPDATE resource_policy SET server_files=0`, "capacity_limit", GuestCapacityLimits{}},
		{"account-files", `UPDATE resource_policy SET account_files=0`, "capacity_limit", GuestCapacityLimits{}},
		{"server-transfers", `UPDATE resource_policy SET server_transfers=0`, "capacity_limit", GuestCapacityLimits{}},
		{"account-transfers", `UPDATE resource_policy SET account_transfers=0`, "capacity_limit", GuestCapacityLimits{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			q := guestCapacityFixture(t)
			// Object limits are positive in production; create one canonical object when
			// testing exhausted object headroom instead of changing schema constraints.
			query := test.sql
			if strings.Contains(query, "files=0") || strings.Contains(query, "transfers=0") {
				if err := q.CreateTransfer("used", time.Now().Add(time.Hour), 0, nil, "owner"); err != nil {
					t.Fatal(err)
				}
				if err := q.CreateFile("used", "used", 0); err != nil {
					t.Fatal(err)
				}
				query = strings.ReplaceAll(query, "=0", "=1")
			}
			if _, err := q.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			q.capacity.probe = func(string) (volumeCapacity, error) { return volumeCapacity{}, errors.New("private probe path") }
			assertGuestCapacity(t, guestSnapshot(t, q, test.limits), "blocked", test.reason, 0, 0)
		})
	}
	q := guestCapacityFixture(t)
	q.capacity.probe = func(string) (volumeCapacity, error) { return volumeCapacity{}, errors.New("offline") }
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "unknown", "capacity_unavailable", 0, 0)
}

func TestGuestCapacityDiskOutstandingAndSeparateDatabaseRoom(t *testing.T) {
	q := guestCapacityFixture(t)
	if err := q.CreateTransfer("existing", time.Now().Add(time.Hour), 0, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateFile("existing", "existing", 4<<20); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateFileOffset("existing", 1<<20, false); err != nil {
		t.Fatal(err)
	}
	q.capacity.probe = func(string) (volumeCapacity, error) { return volumeCapacity{1, 100 << 20, 12 << 20}, nil }
	// 12MiB free -1 reserve -1 metadata -3 outstanding -2 manifest/WAL.
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "ready", "", 5<<20, 100)
	q.capacity.paths = []capacityPath{{path: "data", payload: true}, {path: "db", database: true}}
	q.capacity.probe = func(path string) (volumeCapacity, error) {
		if path == "data" {
			return volumeCapacity{1, 100 << 20, 12 << 20}, nil
		}
		return volumeCapacity{2, 100 << 20, 3 << 20}, nil
	}
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "blocked", "capacity_limit", 0, 0)
	q.capacity.probe = func(path string) (volumeCapacity, error) {
		if path == "data" {
			return volumeCapacity{1, 100 << 20, 12 << 20}, nil
		}
		return volumeCapacity{2, 100 << 20, 4 << 20}, nil
	}
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "ready", "", 6<<20, 100)
}

func TestGuestCapacityEmptyWireBoundsAndConfigurationClamping(t *testing.T) {
	q := guestCapacityFixture(t)
	for _, test := range []struct{ wire, files int64 }{{59, 0}, {60, 1}, {119, 1}, {120, 2}, {6000, 100}} {
		c := guestSnapshot(t, q, GuestCapacityLimits{SlotBytes: MaxManifestBytes + test.wire, FilesPerTransfer: 999, ManifestBytes: 2 * MaxManifestBytes})
		state, reason := "ready", ""
		wire := test.wire
		if test.files == 0 {
			state = "blocked"
			reason = "link_limit"
			wire = 0
		}
		assertGuestCapacity(t, c, state, reason, wire, test.files)
	}
	c := guestSnapshot(t, q, GuestCapacityLimits{SlotBytes: 1024, ManifestBytes: 128, FilesPerTransfer: 3})
	assertGuestCapacity(t, c, "ready", "", 896, 3)
	if c.ManifestReserveBytes != 128 {
		t.Fatal(c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.GuestSlotCapacity(ctx, "slot", GuestCapacityLimits{}); !errors.Is(err, context.Canceled) {
		t.Fatal("query ignored canceled context", err)
	}
}

func TestGuestCapacityStaleSnapshotCannotBypassConcurrentAllocation(t *testing.T) {
	q := guestCapacityFixture(t)
	p := policyForTest(t, q)
	p.AccountTransfers = 1
	p.ServerTransfers = 1
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "ready", "", 31<<20, 100)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			results <- q.CreateSlotTransfer("slot", id, time.Now().Add(time.Hour), 0, nil)
		}(id)
	}
	close(start)
	wg.Wait()
	close(results)
	success, rejected := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrResourceLimit) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatal("stale estimate bypassed admission", success, rejected)
	}
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "blocked", "capacity_limit", 0, 0)
}

func TestGuestCapacityKnownDiskBlockWinsOverOtherProbeFailure(t *testing.T) {
	q := guestCapacityFixture(t)
	q.capacity.paths = []capacityPath{{path: "data", payload: true}, {path: "db", database: true}}
	q.capacity.probe = func(path string) (volumeCapacity, error) {
		if path == "data" {
			return volumeCapacity{1, 100 << 20, 1 << 20}, nil
		}
		return volumeCapacity{}, errors.New("database disk unavailable")
	}
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "blocked", "capacity_limit", 0, 0)
	q.capacity.probe = func(path string) (volumeCapacity, error) {
		if path == "data" {
			return volumeCapacity{1, 100 << 20, 100 << 20}, nil
		}
		return volumeCapacity{}, errors.New("database disk unavailable")
	}
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "unknown", "capacity_unavailable", 0, 0)
	q.capacity.paths = []capacityPath{{path: "data", payload: true}}
	assertGuestCapacity(t, guestSnapshot(t, q, GuestCapacityLimits{}), "unknown", "capacity_unavailable", 0, 0)
}
