package api_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

const guestCapacitySlot = "d138f5fe-3125-41a8-990a-6e21d659e7ca"

func guestCapacityAPI(t *testing.T, configuredFileLimits ...int) (http.Handler, *database.Queries, *sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	storage := filepath.Join(dir, "payloads")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := database.NewQueries(db)
	if err = q.CreateUser(database.User{ID: "private-owner", Username: "private-name", Role: "user", PasswordHash: []byte("hash")}, false); err != nil {
		t.Fatal(err)
	}
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if err = q.CreateReceiveSlot(guestCapacitySlot, time.Now().Add(time.Hour), nil, "private-owner", 2, key, 0); err != nil {
		t.Fatal(err)
	}
	p, err := q.ResourcePolicy()
	if err != nil {
		t.Fatal(err)
	}
	p.ServerStorageBytes = 8 << 20
	p.AccountStorageBytes = 4 << 20
	p.ReserveDiskBytes = 1 << 20
	p.ReserveDiskPercent = 1
	if err = q.SetResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	fs, err := store.NewDiskStore(storage)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{StoragePath: storage, DBPath: dbPath, MaxFilesPerTransfer: 7, MaxManifestSize: 1024, MaxFileSize: 100 << 20, RateLimitGlobal: 1000, RateLimitBurst: 1000, RateLimitCreation: 1000, RateLimitCreationBurst: 1000}
	if len(configuredFileLimits) > 0 {
		cfg.MaxFilesPerTransfer = configuredFileLimits[0]
	}
	return api.NewServer(cfg, q, fs).Router(), q, db, dbPath
}
func guestCapacityResponse(t *testing.T, h http.Handler) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/slots/"+guestCapacitySlot+"/availability", nil))
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("availability may be cached", w.Header())
	}
	return w, out
}
func TestGuestCapacityPublicAvailabilityContractAndPrivacy(t *testing.T) {
	h, q, db, _ := guestCapacityAPI(t)
	if err := q.CreateTransfer("private-child", time.Now().Add(time.Hour), 0, nil, "private-owner"); err != nil {
		t.Fatal(err)
	}
	if err := q.LinkSlotTransfer(guestCapacitySlot, "private-child"); err != nil {
		t.Fatal(err)
	}
	w, out := guestCapacityResponse(t, h)
	if w.Code != 200 || out["available"] != true {
		t.Fatal(w.Code, out)
	}
	capacity, ok := out["upload_capacity"].(map[string]any)
	if !ok {
		t.Fatal(out)
	}
	if capacity["state"] != "ready" || capacity["available_files"] != float64(7) || capacity["available_wire_bytes"] != float64((4<<20)-1024) || capacity["manifest_reserve_bytes"] != float64(1024) {
		t.Fatal(capacity)
	}
	if _, exists := capacity["reason"]; exists {
		t.Fatal("ready reason must be absent", capacity)
	}
	stamp, err := time.Parse(time.RFC3339Nano, capacity["checked_at"].(string))
	if err != nil || stamp.Location() != time.UTC {
		t.Fatal("invalid UTC timestamp", capacity)
	}
	for _, value := range []string{"private-owner", "private-name", "private-child", "reserved_bytes", "occupied_bytes", "server_storage", "account_storage", "reconciliation", "scope"} {
		if strings.Contains(w.Body.String(), value) {
			t.Fatal("public response disclosed private metadata", w.Body.String())
		}
	}
	// New canonical usage controls the next snapshot without summary workers.
	if err = q.CreateFile("private-payload", "private-child", 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM admin_resource_totals`); err != nil {
		t.Fatal(err)
	}
	w, out = guestCapacityResponse(t, h)
	if w.Code != 200 {
		t.Fatal(w.Code, out)
	}
	capacity = out["upload_capacity"].(map[string]any)
	if capacity["available_wire_bytes"] != float64((3<<20)-1024) {
		t.Fatal("snapshot trusted missing derived summary", capacity)
	}
}
func TestGuestCapacityUnknownAndBlockedKeepLinkAvailabilitySeparate(t *testing.T) {
	h, q, db, dbPath := guestCapacityAPI(t)
	q.SetCapacityPaths(filepath.Join(t.TempDir(), "absent"), dbPath)
	w, out := guestCapacityResponse(t, h)
	if w.Code != 200 || out["available"] != true {
		t.Fatal(w.Code, out)
	}
	c := out["upload_capacity"].(map[string]any)
	if c["state"] != "unknown" || c["reason"] != "capacity_unavailable" || c["available_wire_bytes"] != nil || c["available_files"] != nil {
		t.Fatal(c)
	}
	if _, err := db.Exec(`UPDATE resource_policy SET account_storage_bytes=1048576`); err != nil {
		t.Fatal(err)
	}
	// Configured manifest reserve is1024; fill all but its reserve in canonical usage.
	if _, err := db.Exec(`INSERT INTO transfers(id,owner_id,expires_at) VALUES('occupied','private-owner',?)`, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO files(id,transfer_id,size) VALUES('occupied','occupied',1047552)`); err != nil {
		t.Fatal(err)
	}
	w, out = guestCapacityResponse(t, h)
	if w.Code != 200 || out["available"] != true {
		t.Fatal(w.Code, out)
	}
	c = out["upload_capacity"].(map[string]any)
	if c["state"] != "blocked" || c["reason"] != "capacity_limit" || c["available_wire_bytes"] != float64(0) || c["available_files"] != float64(0) {
		t.Fatal(c)
	}
	if _, err := db.Exec(`UPDATE slots SET max_files=1,reserved_files=1 WHERE id=?`, guestCapacitySlot); err != nil {
		t.Fatal(err)
	}
	w, out = guestCapacityResponse(t, h)
	if w.Code != 200 || out["available"] != false {
		t.Fatal(w.Code, out)
	}
	c = out["upload_capacity"].(map[string]any)
	if c["reason"] != "link_limit" {
		t.Fatal(c)
	}
}
func TestGuestCapacityPreservesAvailabilityLifecycleGuards(t *testing.T) {
	for _, test := range []struct {
		name, sql string
		code      int
	}{{"disabled", `UPDATE users SET disabled=1 WHERE id='private-owner'`, 403}, {"revoked", `UPDATE slots SET status='revoked'`, 410}, {"expired", `UPDATE slots SET expires_at='2000-01-01 00:00:00+00:00'`, 410}} {
		t.Run(test.name, func(t *testing.T) {
			h, _, db, _ := guestCapacityAPI(t)
			if _, err := db.Exec(test.sql); err != nil {
				t.Fatal(err)
			}
			w, out := guestCapacityResponse(t, h)
			if w.Code != test.code {
				t.Fatal(w.Code, out)
			}
			if _, ok := out["upload_capacity"]; ok {
				t.Fatal("inactive link exposed capacity", out)
			}
		})
	}
}
