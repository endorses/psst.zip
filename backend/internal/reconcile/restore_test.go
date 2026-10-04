package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/cleanup"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

// These fixtures are stopped checkpoints, not live copies of a WAL database.
// Every restore uses fresh database and payload paths and the production startup
// reset/recovery APIs, then combines the three independently bounded workers.
type restoreCheckpoint struct {
	database, payloads string
	lifetime           restoreLifetime
}
type restoreLifetime struct {
	SlotBytes, SlotFiles, Submissions                                              int64
	TransferDownloads, FileDownloads                                               int64
	Uploaded, Downloaded, FilesUploaded, FilesDelivered, Received                  int64
	ObservedUp, ObservedDown, ConservativeUp, ConservativeDown, LeaseUp, LeaseDown int64
}

func restoreCounters(t *testing.T, f *fixture) restoreLifetime {
	t.Helper()
	var s restoreLifetime
	if err := f.db.QueryRow(`SELECT reserved_bytes,reserved_files,upload_count FROM slots WHERE id='inbox'`).Scan(&s.SlotBytes, &s.SlotFiles, &s.Submissions); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT t.download_count,f.download_count FROM transfers t JOIN files f ON f.transfer_id=t.id WHERE f.id='counted'`).Scan(&s.TransferDownloads, &s.FileDownloads); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT uploaded_bytes,downloaded_bytes,files_uploaded,files_delivered,received_files_uploaded FROM traffic_retention WHERE id=1`).Scan(&s.Uploaded, &s.Downloaded, &s.FilesUploaded, &s.FilesDelivered, &s.Received); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COALESCE(SUM(observed_up),0),COALESCE(SUM(observed_down),0),COALESCE(SUM(conservative_up),0),COALESCE(SUM(conservative_down),0) FROM traffic_owner_days`).Scan(&s.ObservedUp, &s.ObservedDown, &s.ConservativeUp, &s.ConservativeDown); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COALESCE(SUM(CASE WHEN direction='up' THEN bytes ELSE 0 END),0),COALESCE(SUM(CASE WHEN direction='down' THEN bytes ELSE 0 END),0) FROM traffic_leases`).Scan(&s.LeaseUp, &s.LeaseDown); err != nil {
		t.Fatal(err)
	}
	return s
}
func (s restoreLifetime) recovered() restoreLifetime {
	s.ConservativeUp += s.LeaseUp
	s.ConservativeDown += s.LeaseDown
	s.LeaseUp = 0
	s.LeaseDown = 0
	return s
}
func copyRestoreTree(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(to, 0700); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, relative)
		if d.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		if !d.Type().IsRegular() {
			return errors.New("restore fixture contains unsupported entry")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0600)
	}); err != nil {
		t.Fatal(err)
	}
}
func takeRestoreCheckpoint(t *testing.T, f *fixture) restoreCheckpoint {
	t.Helper()
	saved := restoreCheckpoint{lifetime: restoreCounters(t, f)}
	var busy, log, checkpointed int
	if err := f.db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil || busy != 0 || log != 0 {
		t.Fatal("offline checkpoint did not drain WAL", busy, log, checkpointed, err)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	backup := t.TempDir()
	saved.database = filepath.Join(backup, "snapshot.db")
	saved.payloads = filepath.Join(backup, "payloads")
	data, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(saved.database, data, 0600); err != nil {
		t.Fatal(err)
	}
	copyRestoreTree(t, filepath.Join(filepath.Dir(f.path), "payloads"), saved.payloads)
	f.db, err = database.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.q = database.NewQueries(f.db)
	return saved
}
func restoreStartup(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	for _, reset := range []func(context.Context) error{f.q.ResetReconciliationScan, f.q.ResetCounterRebuild, f.q.ResetOrphanScan} {
		if err := reset(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.q.RecoverTrafficLeases(); err != nil {
		t.Fatal(err)
	}
	f.q.SetCapacityPaths(filepath.Join(filepath.Dir(f.path), "payloads"), f.path)
	payload, err := f.q.ReconciliationStatus(ctx)
	if err != nil || payload.LastScanCompletedAt != nil || !payload.ScanPending {
		t.Fatal("payload coverage trusted restored checkpoint", payload, err)
	}
	counter, err := f.q.CounterRebuildStatus(ctx)
	if err != nil || counter.LastScanCompletedAt != nil || !counter.ScanPending {
		t.Fatal("counter coverage trusted restored checkpoint", counter, err)
	}
	orphan, err := f.q.OrphanScanStatus(ctx)
	if err != nil || orphan.LastScanCompletedAt != nil || !orphan.ScanPending {
		t.Fatal("orphan coverage trusted restored checkpoint", orphan, err)
	}
}
func openRestore(t *testing.T, db, payload restoreCheckpoint) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	data, err := os.ReadFile(db.database)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	copyRestoreTree(t, payload.payloads, filepath.Join(dir, "payloads"))
	conn, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := store.NewDiskStore(filepath.Join(dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{q: database.NewQueries(conn), disk: disk, db: conn, path: path}
	t.Cleanup(func() { f.db.Close() })
	restoreStartup(t, f)
	return f
}
func restoreWorkers(t *testing.T, f *fixture, now time.Time, steps int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < steps; i++ {
		if err := Sweep(ctx, f.q, f.disk); err != nil && !errors.Is(err, ErrPayloadUnavailable) {
			t.Fatal(err)
		}
		if err := SweepOrphansAt(ctx, f.q, f.disk, now.Add(time.Duration(i)*time.Second)); err != nil && !errors.Is(err, store.ErrInventoryChanged) && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err := f.q.RebuildCounterBatch(ctx, 64); err != nil {
			t.Fatal(err)
		}
	}
}
func restorePayload(t *testing.T, f *fixture, key string) string {
	t.Helper()
	r, err := f.disk.Load(key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func restoreChild(t *testing.T, f *fixture, id, file string, size, offset int64, body string, complete bool) {
	t.Helper()
	if err := f.q.CreateSlotTransfer("inbox", id, time.Now().Add(time.Hour), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.q.CreateFileWithQuota(file, id, size, 5<<30); err != nil {
		t.Fatal(err)
	}
	if err := f.disk.Save(id+"/"+file, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := f.q.UpdateFileOffset(file, offset, complete); err != nil {
		t.Fatal(err)
	}
	if complete {
		if err := f.q.SaveManifest(id, []byte("opaque-encrypted-manifest")); err != nil {
			t.Fatal(err)
		}
		if err := f.q.CompleteTransfer(id); err != nil {
			t.Fatal(err)
		}
	}
}
func seedRestoreCheckpoints(t *testing.T) (restoreCheckpoint, restoreCheckpoint) {
	t.Helper()
	f := setup(t)
	now := time.Now()
	if err := f.q.CreateUser(database.User{ID: "owner", Username: "owner", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	p, err := f.q.ResourcePolicy()
	if err != nil {
		t.Fatal(err)
	}
	p.AccountFiles = 4
	p.ServerFiles = 8
	p.AccountStorageBytes = 16 << 20
	p.ServerStorageBytes = 32 << 20
	p.ReserveDiskBytes = 1 << 20
	p.ReserveDiskPercent = 1
	if err = f.q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	if err = f.q.CreateReceiveSlot("inbox", now.Add(time.Hour), nil, "owner", 2, "public-key", 10); err != nil {
		t.Fatal(err)
	}
	restoreChild(t, f, "published", "published-file", 8, 8, "original", true)
	restoreChild(t, f, "pending", "partial", 8, 3, "abc", false)
	if err = f.q.CreateTransfer("download", now.Add(time.Hour), 3, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	if err = f.q.CreateFile("counted", "download", 4); err != nil {
		t.Fatal(err)
	}
	if err = f.disk.Save("download/counted", strings.NewReader("keep")); err != nil {
		t.Fatal(err)
	}
	if err = f.q.UpdateFileOffset("counted", 4, true); err != nil {
		t.Fatal(err)
	}
	if err = f.q.CompleteTransfer("download"); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.q.ReserveFileDownload("download", "counted"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err = f.q.AddTraffic(now, database.TrafficTotals{UploadedBytes: 100, DownloadedBytes: 40}); err != nil {
		t.Fatal(err)
	}
	lease, err := f.q.ReserveTraffic("owner", false, 40, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.q.SettleTraffic(lease.ID, 40); err != nil {
		t.Fatal(err)
	}
	if _, err = f.q.ReserveTraffic("owner", true, 64, now); err != nil {
		t.Fatal(err)
	}
	restoreWorkers(t, f, now, 20)
	older := takeRestoreCheckpoint(t, f)
	// Continue the original instance after the checkpoint: these events cannot be
	// inferred later if its older database is restored with these newer payloads.
	restoreChild(t, f, "newer", "newer-file", 4, 4, "late", true)
	if err = f.disk.Save("pending/partial", strings.NewReader("abcdef")); err != nil {
		t.Fatal(err)
	}
	if err = f.q.UpdateFileOffset("partial", 6, false); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.q.ReserveFileDownload("download", "counted"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err = f.q.AddTraffic(now, database.TrafficTotals{UploadedBytes: 33, DownloadedBytes: 11}); err != nil {
		t.Fatal(err)
	}
	lease, err = f.q.ReserveTraffic("owner", false, 11, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.q.SettleTraffic(lease.ID, 11); err != nil {
		t.Fatal(err)
	}
	restoreWorkers(t, f, now, 20)
	return older, takeRestoreCheckpoint(t, f)
}

func TestOfflineRestoreCheckpointMatrix(t *testing.T) {
	older, newer := seedRestoreCheckpoints(t)
	tests := []struct {
		name        string
		db, payload restoreCheckpoint
		offset      int64
		unavailable int64
		orphan      bool
		capacity    string
	}{
		{"matching_checkpoint", newer, newer, 6, 0, false, "blocked"},
		{"older_database_newer_payloads", older, newer, 3, 0, true, "ready"},
		{"newer_database_older_payloads", newer, older, 3, 2, false, "blocked"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := openRestore(t, test.db, test.payload)
			ctx := context.Background()
			now := time.Now()
			expected := test.db.lifetime.recovered()
			if got := restoreCounters(t, f); got != expected {
				t.Fatal("startup lost or invented lifetime consumption", got, expected)
			}
			owner, err := f.q.Owner("transfer", "published")
			if err != nil || owner != "owner" {
				t.Fatal("restored child owner changed", owner, err)
			}
			parents, err := f.q.TransferSlotIDs("published")
			if err != nil || !reflect.DeepEqual(parents, []string{"inbox"}) {
				t.Fatal("restored inbox association changed", parents, err)
			}
			before, err := f.q.ResourceUsage("owner")
			if err != nil {
				t.Fatal(err)
			}
			if test.unavailable > 0 {
				if err = f.disk.Truncate("published/published-file", 2); err != nil {
					t.Fatal(err)
				}
			}
			// Rebuild absent/stale derived rows from this restored canonical database.
			if _, err = f.db.Exec(`DELETE FROM admin_resource_totals WHERE kind='transfer' AND resource_id='pending'`); err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec(`UPDATE admin_resource_totals SET file_count=999,reserved_bytes=999,occupied_bytes=999 WHERE kind='slot' AND resource_id='inbox'`); err != nil {
				t.Fatal(err)
			}
			restoreWorkers(t, f, now, 30)
			if test.orphan {
				if info, _ := f.disk.Inspect("newer/newer-file"); !info.Exists {
					t.Fatal("orphan removed before grace")
				}
			}
			restoreWorkers(t, f, now.Add(2*time.Hour), 30)
			restoreWorkers(t, f, now.Add(4*time.Hour), 30)
			// Enumeration runs continuously. End at a completed pass rather than
			// assuming a fixed worker count lands between passes.
			for i := 0; i < 16; i++ {
				s, err := f.q.OrphanScanStatus(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !s.ScanPending {
					break
				}
				restoreWorkers(t, f, now.Add(4*time.Hour+time.Minute), 1)
			}
			partial, err := f.q.GetFile("partial")
			if err != nil || partial.UploadOffset != test.offset || partial.Size != 8 || partial.UploadComplete {
				t.Fatal("pending reservation or committed prefix incorrect", partial, err)
			}
			if got := restorePayload(t, f, "pending/partial"); got != "abcdef"[:test.offset] {
				t.Fatal("pending bytes incorrect", got)
			}
			if got := restorePayload(t, f, "download/counted"); got != "keep" {
				t.Fatal("healthy published sibling changed", got)
			}
			if test.unavailable == 0 && restorePayload(t, f, "published/published-file") != "original" {
				t.Fatal("published bytes changed")
			}
			manifest, err := f.q.GetManifest("published")
			if err != nil || string(manifest) != "opaque-encrypted-manifest" {
				t.Fatal("restored manifest changed", string(manifest), err)
			}
			if test.unavailable > 0 {
				for id, code := range map[string]string{"published-file": "payload_size_mismatch", "newer-file": "payload_missing"} {
					issue, err := f.q.FileReconciliationIssue(ctx, id)
					if err != nil || issue.Code != code {
						t.Fatal("published restore damage not identified", id, issue, err)
					}
					file, err := f.q.GetFile(id)
					if err != nil || !file.UploadComplete || file.UploadOffset != file.Size || file.PayloadDeleted {
						t.Fatal("published restore damage rewound immutable metadata", file, err)
					}
				}
			}
			payload, err := f.q.ReconciliationStatus(ctx)
			if err != nil || payload.UnavailableCount != test.unavailable || payload.LastScanCompletedAt == nil {
				t.Fatal("wrong payload recovery status", payload, err)
			}
			counter, err := f.q.CounterRebuildStatus(ctx)
			if err != nil || counter.ScanPending {
				t.Fatal("derived reconstruction incomplete", counter, err)
			}
			orphan, err := f.q.OrphanScanStatus(ctx)
			if err != nil || orphan.ScanPending {
				t.Fatal("disk inventory incomplete", orphan, err)
			}
			after, err := f.q.ResourceUsage("owner")
			if err != nil || after.ReservedBytes != before.ReservedBytes || after.Files != before.Files {
				t.Fatal("repair/orphans refunded canonical reservations", before, after, err)
			}
			var files, reserved, occupied int64
			if err = f.db.QueryRow(`SELECT file_count,reserved_bytes,occupied_bytes FROM admin_resource_totals WHERE kind='slot' AND resource_id='inbox'`).Scan(&files, &reserved, &occupied); err != nil {
				t.Fatal(err)
			}
			var expectedFiles, expectedReserved, expectedOccupied int64
			if err = f.db.QueryRow(`SELECT COUNT(f.id),COALESCE(SUM(f.size),0)+(SELECT COALESCE(SUM(length(m.data)),0) FROM manifests m JOIN slot_transfers st ON st.transfer_id=m.transfer_id WHERE st.slot_id='inbox'),COALESCE(SUM(f.upload_offset),0)+(SELECT COALESCE(SUM(length(m.data)),0) FROM manifests m JOIN slot_transfers st ON st.transfer_id=m.transfer_id WHERE st.slot_id='inbox') FROM files f JOIN slot_transfers st ON st.transfer_id=f.transfer_id WHERE st.slot_id='inbox'`).Scan(&expectedFiles, &expectedReserved, &expectedOccupied); err != nil {
				t.Fatal(err)
			}
			if files != expectedFiles || reserved != expectedReserved || occupied != expectedOccupied {
				t.Fatal("derived summary differs from restored canonical rows", files, reserved, occupied, expectedFiles, expectedReserved, expectedOccupied)
			}
			capacity, err := f.q.GuestSlotCapacity(ctx, "inbox", database.GuestCapacityLimits{})
			if err != nil || capacity.Capacity.State != test.capacity {
				t.Fatal("capacity not based on retained canonical reservations", capacity.Capacity, err)
			}
			if got := restoreCounters(t, f); !reflect.DeepEqual(got, expected) {
				t.Fatal("workers reconstructed or refunded lifetime consumption", got, expected)
			}
			if test.orphan {
				if info, _ := f.disk.Inspect("newer/newer-file"); info.Exists {
					t.Fatal("post-checkpoint orphan retained after grace")
				}
				if _, err = f.q.GetTransfer("newer"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("orphan bytes invented canonical transfer", err)
				}
				if expected.Submissions >= newer.lifetime.Submissions || expected.FileDownloads >= newer.lifetime.FileDownloads || expected.Uploaded >= newer.lifetime.Uploaded {
					t.Fatal("fixture failed to demonstrate irrecoverable historical rollback")
				}
			}
			if test.unavailable > 0 {
				restoreCleanupAfterMismatch(t, f, expected, before.ReservedBytes)
			}
		})
	}
}

type restoreDeleteFailure struct{ *store.DiskStore }

func (f restoreDeleteFailure) DeleteAllBounded(ctx context.Context, prefix string, budget int) (bool, error) {
	done, err := f.DiskStore.DeleteAllBounded(ctx, prefix, budget)
	if err != nil {
		return done, err
	}
	return false, errors.New("injected post-removal durability uncertainty")
}
func restoreCleanupAfterMismatch(t *testing.T, f *fixture, expected restoreLifetime, reservedBefore int64) {
	t.Helper()
	ctx := context.Background()
	reader, err := store.AcquireReader("newer")
	if err != nil {
		t.Fatal(err)
	}
	if err = cleanup.TryRemoveTransfer(f.q, f.disk, "newer"); !errors.Is(err, store.ErrResourceBusy) {
		t.Fatal("cleanup ignored restored reader", err)
	}
	reader()
	if err = cleanup.TryRemoveTransfer(f.q, restoreDeleteFailure{f.disk}, "newer"); err == nil {
		t.Fatal("uncertain deletion acknowledged")
	}
	usage, err := f.q.ResourceUsage("owner")
	if err != nil || usage.ReservedBytes != reservedBefore {
		t.Fatal("uncertain cleanup released reservation", usage, err)
	}
	if err = f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db, err = database.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.q = database.NewQueries(f.db)
	restoreStartup(t, f)
	if got := restoreCounters(t, f); got != expected {
		t.Fatal("second startup charged interrupted leases twice", got, expected)
	}
	status, err := f.q.ResourceCleanup("transfer", "newer")
	if err != nil || status.State != "failed" {
		t.Fatal("cleanup intent lost across restart", status, err)
	}
	if err = cleanup.TryRemoveTransfer(f.q, f.disk, "newer"); err != nil {
		t.Fatal(err)
	}
	usage, err = f.q.ResourceUsage("owner")
	if err != nil || usage.ReservedBytes >= reservedBefore || usage.Files != 3 {
		t.Fatal("durable cleanup did not release current reservations", usage, err)
	}
	if _, err = f.q.GetTransfer("newer"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cleaned metadata remained", err)
	}
	capacity, err := f.q.GuestSlotCapacity(ctx, "inbox", database.GuestCapacityLimits{})
	if err != nil || capacity.Capacity.State != "ready" {
		t.Fatal("cleanup did not restore current capacity", capacity.Capacity, err)
	}
	if got := restoreCounters(t, f); got != expected {
		t.Fatal("cleanup refunded cumulative lifetime consumption", got, expected)
	}
	if restorePayload(t, f, "published/published-file") != "or" {
		t.Fatal("cleanup changed unavailable sibling")
	}
}
