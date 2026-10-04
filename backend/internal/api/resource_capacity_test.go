package api_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

type diskPressureStore struct {
	store.FileStore
	q *database.Queries
	t *testing.T
}
type diskPressureReader struct {
	io.Reader
	q     *database.Queries
	t     *testing.T
	n     int64
	fired bool
}

func (r *diskPressureReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += int64(n)
	if !r.fired && r.n >= 1<<20 {
		r.fired = true
		policy, e := r.q.ResourcePolicy()
		if e != nil {
			r.t.Error(e)
			return n, e
		}
		policy.ReserveDiskBytes = 1 << 40
		if e = r.q.SetResourcePolicy(policy); e != nil {
			r.t.Error(e)
			return n, e
		}
	}
	return n, err
}
func (s diskPressureStore) SaveAt(key string, r io.Reader, offset int64) (int64, error) {
	return s.FileStore.SaveAt(key, &diskPressureReader{Reader: r, q: s.q, t: s.t}, offset)
}
func TestResourceDiskPressureStopsStreamingAndPreservesRecovery(t *testing.T) {
	env := setupAuthFixture(t, false)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	fs := fixtureStore(t, env)
	pressure := alternateServer(t, env, diskPressureStore{FileStore: fs, q: env.queries, t: t}, false)
	env.queries.SetCapacityPaths(env.dataDir+"/files", env.dataDir+"/test.db")
	// Reserve explicitly in the DB so the small alternate server's per-file
	// plaintext cap does not obscure the volume-pressure behavior being tested.
	fileID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if err := env.queries.CreateFile(fileID, id, 2<<20); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("PATCH", pressure.url("/api/v1/transfers/"+id+"/files/"+fileID), bytes.NewReader(make([]byte, 2<<20)))
	request.Header.Set("Tus-Resumable", "1.0.0")
	request.Header.Set("Upload-Offset", "0")
	request.Header.Set("Content-Type", "application/offset+octet-stream")
	response, err := pressure.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 507 || !strings.Contains(string(body), "disk_capacity") {
		t.Fatalf("pressure response %d %s", response.StatusCode, body)
	}
	file, err := env.queries.GetFile(fileID)
	if err != nil || file.UploadOffset != 1<<20 || file.UploadComplete {
		t.Fatalf("partial offset %+v %v", file, err)
	}
	actual, err := fs.Size(id + "/" + fileID)
	if err != nil || actual != file.UploadOffset {
		t.Fatalf("disk/DB progress diverged %d %v", actual, err)
	}
	usage, err := env.queries.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 2<<20 || usage.OccupiedBytes != 1<<20 {
		t.Fatalf("usage %+v %v", usage, err)
	}
	authRequest(t, env, "POST", "/transfers", env.userToken, nil, 507)
	authRequest(t, env, "GET", "/admin/resource-policy", env.authToken, nil, 200)
	authRequest(t, env, "DELETE", "/transfers/"+id, env.userToken, nil, 204)
	authRequest(t, env, "PATCH", "/admin/resource-policy", env.authToken, map[string]int64{"reserve_disk_bytes": 256 << 20}, 200)
	authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)
}

type noSpaceStore struct{ store.FileStore }

func (s noSpaceStore) SaveAt(key string, r io.Reader, offset int64) (int64, error) {
	n, err := s.FileStore.SaveAt(key, io.LimitReader(r, 256<<10), offset)
	if err != nil {
		return n, err
	}
	return n, syscall.ENOSPC
}
func TestResourceENOSPCKeepsPartialBytesReserved(t *testing.T) {
	env := setupAuthFixture(t, false)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	fileID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if err := env.queries.CreateFile(fileID, id, 1<<20); err != nil {
		t.Fatal(err)
	}
	fs := fixtureStore(t, env)
	limited := alternateServer(t, env, noSpaceStore{FileStore: fs}, false)
	request, _ := http.NewRequest("PATCH", limited.url("/api/v1/transfers/"+id+"/files/"+fileID), bytes.NewReader(make([]byte, 1<<20)))
	request.Header.Set("Tus-Resumable", "1.0.0")
	request.Header.Set("Upload-Offset", "0")
	request.Header.Set("Content-Type", "application/offset+octet-stream")
	response, err := limited.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 507 || !strings.Contains(string(body), "disk_capacity") {
		t.Fatalf("ENOSPC response %d %s", response.StatusCode, body)
	}
	file, err := env.queries.GetFile(fileID)
	if err != nil || file.UploadOffset != 256<<10 || file.UploadComplete {
		t.Fatalf("partial progress %+v %v", file, err)
	}
	usage, err := env.queries.ResourceUsage("")
	if err != nil || usage.ReservedBytes != 1<<20 || usage.OccupiedBytes != 256<<10 {
		t.Fatalf("ENOSPC lost reservation %+v %v", usage, err)
	}
	authRequest(t, env, "DELETE", "/transfers/"+id, env.userToken, nil, 204)
}
