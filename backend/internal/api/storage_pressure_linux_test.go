//go:build linux

package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/cleanup"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
	"golang.org/x/sys/unix"
	sqlite3 "modernc.org/sqlite/lib"
)

// Only tools/test_storage_pressure.py enables this test. Refuse ordinary host
// paths, shared tmpfs directories, nonempty mounts and unbounded filesystems.
func isolatedPressureRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("PSST_STORAGE_PRESSURE_ROOT")
	if root == "" {
		t.Skip("requires the disposable size-capped Docker tmpfs gate")
	}
	if root != "/pressure" {
		t.Fatal("pressure gate requires its dedicated /pressure mount")
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(root, &fs); err != nil {
		t.Fatal(err)
	}
	total := int64(fs.Blocks) * fs.Bsize
	if fs.Type != unix.TMPFS_MAGIC || total < 32<<20 || total > 64<<20 {
		t.Fatal("refusing pressure outside a 32–64 MiB tmpfs filesystem")
	}
	var current, parent unix.Stat_t
	if err := unix.Stat(root, &current); err != nil {
		t.Fatal(err)
	}
	if err := unix.Stat("/", &parent); err != nil || current.Dev == parent.Dev {
		t.Fatal("pressure directory must be a separate mounted filesystem", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("pressure mount must start empty", err)
	}
	return root
}

func pressureAvailable(root string) (int64, error) {
	var fs unix.Statfs_t
	if err := unix.Statfs(root, &fs); err != nil {
		return 0, err
	}
	return int64(fs.Bavail) * fs.Bsize, nil
}

// Write actual allocated pages, not sparse truncation or injected ENOSPC.
func fillPressure(root, name string, leave int64) error {
	available, err := pressureAvailable(root)
	if err != nil || available < leave {
		return errors.Join(err, errors.New("insufficient pressure-fixture headroom"))
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.CopyN(f, zeroPressureReader{}, available-leave)
	return errors.Join(copyErr, f.Sync(), f.Close(), func() error {
		if n != available-leave {
			return errors.New("pressure fixture wrote an unexpected byte count")
		}
		return nil
	}())
}

type zeroPressureReader struct{}

func (zeroPressureReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// Consume real space after the production capacity check but before a storage
// write, modeling another process using the volume during an admitted upload.
type physicalPressureStore struct {
	store.FileStore
	root, filler string
	after, leave int64
	fired        atomic.Bool
	writeError   atomic.Pointer[pressureWriteError]
}

type pressureWriteError struct{ err error }

func (s *physicalPressureStore) SaveAt(key string, r io.Reader, offset int64) (int64, error) {
	n, err := s.FileStore.SaveAt(key, &physicalPressureReader{Reader: r, pressure: s}, offset)
	if err != nil {
		s.writeError.Store(&pressureWriteError{err})
	}
	return n, err
}

type physicalPressureReader struct {
	io.Reader
	pressure *physicalPressureStore
	n        int64
}

func (r *physicalPressureReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += int64(n)
	s := r.pressure
	if n > 0 && r.n >= s.after && s.fired.CompareAndSwap(false, true) {
		if e := fillPressure(s.root, s.filler, s.leave); e != nil {
			return 0, e
		}
	}
	return n, err
}

func pressureServer(t *testing.T, env *testEnv, fs store.FileStore) *testEnv {
	t.Helper()
	cfg := config.Config{
		MaxFileSize: 4 << 20, DefaultExpiry: time.Hour, AuthAllowInsecureHTTP: true,
		RateLimitGlobal: 1000, RateLimitBurst: 2000, RateLimitCreation: 1000, RateLimitCreationBurst: 2000,
	}
	srv := httptest.NewServer(api.NewServer(cfg, env.queries, fs).Router())
	t.Cleanup(srv.Close)
	result := *env
	result.server = srv
	return &result
}

func lowerPressureReserve(t *testing.T, q *database.Queries) {
	t.Helper()
	p, err := q.ResourcePolicy()
	if err != nil {
		t.Fatal(err)
	}
	// The production default 256 MiB reserve intentionally cannot admit writes
	// on this tiny test volume. Use the supported minimum, not a bypass.
	p.ReserveDiskBytes, p.ReserveDiskPercent = 1<<20, 1
	if err := q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
}

func pressureUpload(t *testing.T, env *testEnv, transfer string) string {
	t.Helper()
	r, err := http.NewRequest("POST", env.url("/api/v1/transfers/"+transfer+"/files"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+env.userToken)
	r.Header.Set("Tus-Resumable", "1.0.0")
	r.Header.Set("Upload-Length", strconv.Itoa(2<<20))
	response, err := env.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatal("pressure upload was not admitted", response.StatusCode)
	}
	return filepath.Base(response.Header.Get("Location"))
}

func pressurePatch(t *testing.T, env *testEnv, transfer, file string) {
	t.Helper()
	r, err := http.NewRequest("PATCH", env.url("/api/v1/transfers/"+transfer+"/files/"+file), bytes.NewReader(make([]byte, 2<<20)))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+env.userToken)
	r.Header.Set("Tus-Resumable", "1.0.0")
	r.Header.Set("Upload-Offset", "0")
	r.Header.Set("Content-Type", "application/offset+octet-stream")
	response, err := env.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 507 || !strings.Contains(string(body), "disk_capacity") {
		t.Fatal("physical pressure did not fail safely", response.StatusCode, string(body), err)
	}
}

func TestIsolatedPhysicalStoragePressure(t *testing.T) {
	root := isolatedPressureRoot(t)
	t.Run("shared-volume-reserve-and-cleanup", func(t *testing.T) {
		dir, err := os.MkdirTemp(root, "shared-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		env := setupAuthFixtureIn(t, false, dir)
		lowerPressureReserve(t, env.queries)
		fs := fixtureStore(t, env)
		pressure := &physicalPressureStore{FileStore: fs, root: root, filler: filepath.Join(dir, "filler"), after: 1 << 20, leave: 1536 << 10}
		live := pressureServer(t, env, pressure)
		env.queries.SetCapacityPaths(filepath.Join(dir, "files"), filepath.Join(dir, "test.db"))
		id := authRequest(t, live, "POST", "/transfers", live.userToken, nil, 201)["id"].(string)
		fileID := pressureUpload(t, live, id)
		pressurePatch(t, live, id, fileID)
		file, err := env.queries.GetFile(fileID)
		actual, inspectErr := fs.Size(id + "/" + fileID)
		if err != nil || inspectErr != nil || !pressure.fired.Load() || file.UploadComplete || file.UploadOffset != 1<<20 || actual != file.UploadOffset {
			t.Fatal("physical reserve lost safe partial progress", file, actual, err, inspectErr)
		}
		available, err := pressureAvailable(root)
		if err != nil || available <= 0 || available >= 2<<20 {
			t.Fatal("fixture did not produce a nearly full real volume", available, err)
		}
		authRequest(t, live, "POST", "/transfers", live.userToken, nil, 507)
		authRequest(t, live, "GET", "/admin/resource-policy", live.authToken, nil, 200)
		authRequest(t, live, "DELETE", "/transfers/"+id, live.userToken, nil, 204)
		usage, err := env.queries.ResourceUsage("")
		if err != nil || usage.ReservedBytes != 0 || usage.OccupiedBytes != 0 || usage.Files != 0 {
			t.Fatal("near-full cleanup did not release verified deleted capacity", usage, err)
		}
		if err := os.Remove(pressure.filler); err != nil {
			t.Fatal(err)
		}
		authRequest(t, live, "POST", "/transfers", live.userToken, nil, 201)
	})
	t.Run("separate-payload-volume-real-enospc", func(t *testing.T) {
		dir, err := os.MkdirTemp(root, "payload-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		// Metadata is on the container's separate bounded /tmp mount, so actual
		// payload ENOSPC can still persist resumable offsets and cleanup state.
		env := setupAuthFixture(t, false)
		lowerPressureReserve(t, env.queries)
		fs, err := store.NewDiskStore(filepath.Join(dir, "files"))
		if err != nil {
			t.Fatal(err)
		}
		pressure := &physicalPressureStore{FileStore: fs, root: root, filler: filepath.Join(dir, "filler"), after: 1, leave: 128 << 10}
		live := pressureServer(t, env, pressure)
		env.queries.SetCapacityPaths(filepath.Join(dir, "files"), filepath.Join(env.dataDir, "test.db"))
		id := authRequest(t, live, "POST", "/transfers", live.userToken, nil, 201)["id"].(string)
		fileID := pressureUpload(t, live, id)
		pressurePatch(t, live, id, fileID)
		failure := pressure.writeError.Load()
		if failure == nil || !errors.Is(failure.err, unix.ENOSPC) {
			t.Fatal("payload filesystem did not itself return ENOSPC", failure)
		}
		file, err := env.queries.GetFile(fileID)
		actual, inspectErr := fs.Size(id + "/" + fileID)
		available, capacityErr := pressureAvailable(root)
		if err != nil || inspectErr != nil || capacityErr != nil || available != 0 || file.UploadComplete || file.UploadOffset <= 0 || file.UploadOffset >= 1<<20 || actual != file.UploadOffset {
			t.Fatal("real ENOSPC lost safe partial progress", file, actual, available, err, inspectErr, capacityErr)
		}
		usage, err := env.queries.ResourceUsage("")
		if err != nil || usage.ReservedBytes != 2<<20 || usage.OccupiedBytes != actual || usage.Files != 1 {
			t.Fatal("real ENOSPC prematurely refunded reserved bytes", usage, err)
		}
		authRequest(t, live, "GET", "/admin/resource-policy", live.authToken, nil, 200)
		authRequest(t, live, "POST", "/transfers", live.userToken, nil, 507)
		authRequest(t, live, "DELETE", "/transfers/"+id, live.userToken, nil, 204)
		if err := os.Remove(pressure.filler); err != nil {
			t.Fatal(err)
		}
		authRequest(t, live, "POST", "/transfers", live.userToken, nil, 201)
	})
	t.Run("shared-volume-wal-full-restart-retry", func(t *testing.T) {
		dir, err := os.MkdirTemp(root, "wal-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		env := setupAuthFixtureIn(t, false, dir)
		env.db.SetMaxOpenConns(1)
		lowerPressureReserve(t, env.queries)
		id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
		fileID := pressureUpload(t, env, id)
		fs := fixtureStore(t, env)
		if err := fs.Save(id+"/"+fileID, io.LimitReader(zeroPressureReader{}, 1<<20)); err != nil {
			t.Fatal(err)
		}
		if err := env.queries.UpdateFileOffset(fileID, 1<<20, false); err != nil {
			t.Fatal(err)
		}
		if err := env.queries.RevokeTransfer(id); err != nil {
			t.Fatal(err)
		}
		// Persist prior state and remove reusable WAL capacity before consuming
		// all remaining real blocks. No page-count limit or SQL trigger is used.
		var busy, pages, checkpointed int
		if err := env.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &pages, &checkpointed); err != nil || busy != 0 {
			t.Fatal("fixture could not checkpoint its pre-pressure state", busy, err)
		}
		filler := filepath.Join(dir, "filler")
		if err := fillPressure(root, filler, 0); err != nil {
			t.Fatal(err)
		}
		err = cleanup.SweepPending(context.Background(), env.queries, fs)
		var coded interface{ Code() int }
		if !errors.As(err, &coded) || (coded.Code()&0xff != sqlite3.SQLITE_FULL && coded.Code()&0xff != sqlite3.SQLITE_IOERR) {
			t.Fatal("actual full filesystem did not reject the SQLite write", err)
		}
		if _, err := env.queries.GetTransfer(id); err != nil {
			t.Fatal("failed full-volume cleanup erased durable metadata", err)
		}
		usage, err := env.queries.ResourceUsage("")
		if err != nil || usage.ReservedBytes != 2<<20 || usage.Files != 1 {
			t.Fatal("failed full-volume cleanup refunded uncommitted work", usage, err)
		}
		authRequest(t, env, "GET", "/admin/resource-policy", env.authToken, nil, 200)
		// Reclaim external-process space before restarting, as an operator must
		// do when SQLite cannot persist any further writes, including diagnostics.
		if err := os.Remove(filler); err != nil {
			t.Fatal(err)
		}
		env.server.Close()
		if err := env.db.Close(); err != nil {
			t.Fatal(err)
		}
		restored, err := database.Open(filepath.Join(dir, "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		q := database.NewQueries(restored)
		state, err := q.ResourceCleanup("transfer", id)
		if err != nil || (state.State != "pending" && state.State != "failed") {
			t.Fatal("restart lost the pending cleanup", state, err)
		}
		usage, err = q.ResourceUsage("")
		if err != nil || usage.ReservedBytes != 2<<20 || usage.Files != 1 {
			t.Fatal("restart lost conservative capacity accounting", usage, err)
		}
		if err := q.RequestResourceCleanup("transfer", id); err != nil {
			t.Fatal(err)
		}
		if err := cleanup.SweepPending(context.Background(), q, fs); err != nil {
			t.Fatal(err)
		}
		if _, err := q.GetTransfer(id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("capacity recovery did not complete queued deletion", err)
		}
		usage, err = q.ResourceUsage("")
		if err != nil || usage.ReservedBytes != 0 || usage.OccupiedBytes != 0 || usage.Files != 0 {
			t.Fatal("successful retry did not release capacity", usage, err)
		}
	})
}
