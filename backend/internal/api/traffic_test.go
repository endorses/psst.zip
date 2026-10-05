package api_test

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestTrafficActualPayloadAndEventAccounting(t *testing.T) {
	env := setupAuthFixture(t, false)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	send := func(method, path, token string, body []byte, headers map[string]string, want int) (http.Header, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, env.url("/api/v1"+path), bytes.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := env.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, data)
		}
		return res.Header, data
	}
	data := []byte("encrypted file payload")
	h, _ := send("POST", "/transfers/"+id+"/files", env.userToken, nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": strconv.Itoa(len(data))}, 201)
	path := h.Get("Location")[len("/api/v1"):]
	patchHeaders := map[string]string{"Tus-Resumable": "1.0.0", "Upload-Offset": "0", "Content-Type": "application/offset+octet-stream"}
	send("PATCH", path, env.userToken, data[:5], patchHeaders, 204)
	// Rejected offset bytes are not read by the application and do not count.
	send("PATCH", path, env.userToken, data[:5], patchHeaders, 409)
	patchHeaders["Upload-Offset"] = "5"
	send("PATCH", path, env.userToken, data[5:], patchHeaders, 204)
	manifest := []byte("opaque encrypted manifest")
	for range 2 {
		send("POST", "/transfers/"+id+"/manifest", env.userToken, manifest, nil, 204)
	}
	authRequest(t, env, "POST", "/transfers/"+id+"/complete", env.userToken, nil, 204)
	for range 2 {
		_, got := send("GET", path, "", nil, nil, 200)
		if !bytes.Equal(got, data) {
			t.Fatal("download body changed")
		}
	}
	send("GET", "/transfers/"+id+"/manifest", "", nil, nil, 200)
	send("HEAD", path, env.userToken, nil, map[string]string{"Tus-Resumable": "1.0.0"}, 200)
	for range 2 {
		authRequest(t, env, "POST", "/transfers/"+id+"/downloaded", "", nil, 204)
	}
	overview := authRequest(t, env, "GET", "/admin/overview", env.authToken, nil, 200)
	if overview["stored_bytes"] != float64(len(data)+len(manifest)) || overview["files_uploaded"] != float64(1) || overview["files_delivered"] != float64(1) || overview["enabled_users"] != float64(1) {
		t.Fatal(overview)
	}
	report := authRequest(t, env, "GET", "/admin/traffic", env.authToken, nil, 200)
	total := report["lifetime"].(map[string]any)
	if total["uploaded_bytes"] != float64(len(data)+2*len(manifest)) || total["downloaded_bytes"] != float64(2*len(data)+len(manifest)) || total["files_uploaded"] != float64(1) || total["files_delivered"] != float64(1) {
		t.Fatal(total)
	}
	authRequest(t, env, "DELETE", "/transfers/"+id, env.authToken, nil, 204)
	after := authRequest(t, env, "GET", "/admin/overview", env.authToken, nil, 200)
	if after["stored_bytes"] != float64(0) || after["active_transfers"] != float64(0) || after["files_uploaded"] != float64(1) {
		t.Fatal(after)
	}
	got := authRequest(t, env, "GET", "/admin/traffic", env.authToken, nil, 200)["lifetime"].(map[string]any)
	for k, v := range total {
		if got[k] != v {
			t.Fatalf("cleanup altered counter %s: %v %v", k, v, got[k])
		}
	}
}

func TestTrafficAccessRangeSettingsAndCoverage(t *testing.T) {
	env := setupAuthFixture(t, false)
	for _, path := range []string{"/admin/traffic", "/admin/overview"} {
		authRequest(t, env, "GET", path, "", nil, 401)
		authRequest(t, env, "GET", path, env.userToken, nil, 403)
	}
	defaults := authRequest(t, env, "GET", "/admin/traffic", env.authToken, nil, 200)
	if defaults["recording_started_at"] == "" || defaults["status"] != "ok" || defaults["timezone"] != "UTC" || defaults["lifetime"].(map[string]any)["total_bytes"] != float64(0) {
		t.Fatal(defaults)
	}
	valid := map[string]any{"allowance_bytes": 100, "cycle_start_day": 31, "basis": "combined"}
	authRequest(t, env, "PATCH", "/admin/traffic/settings", env.userToken, valid, 403)
	for _, bad := range []map[string]any{
		{}, {"allowance_bytes": 100, "cycle_start_day": 32, "basis": "outbound"},
		{"allowance_bytes": 0, "cycle_start_day": 1, "basis": "outbound"},
		{"allowance_bytes": -1, "cycle_start_day": 1, "basis": "outbound"},
		{"allowance_bytes": 1.5, "cycle_start_day": 1, "basis": "outbound"},
		{"allowance_bytes": 1, "cycle_start_day": 1, "basis": "inbound"},
		{"allowance_bytes": 1, "cycle_start_day": 1, "basis": "outbound", "extra": true},
	} {
		authRequest(t, env, "PATCH", "/admin/traffic/settings", env.authToken, bad, 400)
	}
	authRequest(t, env, "PATCH", "/admin/traffic/settings", env.authToken, valid, 200)
	now := time.Now().UTC()
	if err := env.queries.AddTraffic(now, database.TrafficTotals{UploadedBytes: 60, DownloadedBytes: 30}); err != nil {
		t.Fatal(err)
	}
	date := now.Format("2006-01-02")
	result := authRequest(t, env, "GET", "/admin/traffic?from="+date+"&to="+date, env.authToken, nil, 200)
	cycle := result["cycle"].(map[string]any)
	if cycle["counted_bytes"] != float64(90) || cycle["remaining_bytes"] != float64(10) || len(result["days"].([]any)) != 1 {
		t.Fatal(result)
	}
	valid["basis"] = "outbound"
	authRequest(t, env, "PATCH", "/admin/traffic/settings", env.authToken, valid, 200)
	cycle = authRequest(t, env, "GET", "/admin/traffic", env.authToken, nil, 200)["cycle"].(map[string]any)
	if cycle["counted_bytes"] != float64(30) || cycle["remaining_bytes"] != float64(70) {
		t.Fatal(cycle)
	}
	for _, query := range []string{"from=bad", "from=2026-02-30", "from=2026-04-02&to=2026-04-01", "from=2020-01-01&to=2026-01-01"} {
		authRequest(t, env, "GET", "/admin/traffic?"+query, env.authToken, nil, 400)
	}
	if err := env.queries.MarkTrafficDegraded(); err != nil {
		t.Fatal(err)
	}
	if authRequest(t, env, "GET", "/admin/traffic", env.authToken, nil, 200)["status"] != "degraded" {
		t.Fatal("failure coverage hidden")
	}
	if _, err := env.db.Exec("DROP TABLE traffic_days"); err != nil {
		t.Fatal(err)
	}
	authRequest(t, env, "GET", "/admin/traffic", env.authToken, nil, 503)
}

func TestTrafficRetainedCoverageAndLifetime(t *testing.T) {
	env := setupAuthFixture(t, false)
	now := time.Now().UTC()
	floor := now.AddDate(0, 0, 1-database.TrafficHistoryRetentionDays).Format("2006-01-02")
	if err := env.queries.AddTraffic(now.AddDate(-2, 0, 0), database.TrafficTotals{UploadedBytes: 123, FilesUploaded: 4}); err != nil {
		t.Fatal(err)
	}
	if err := env.queries.AddTraffic(now, database.TrafficTotals{DownloadedBytes: 17}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.queries.PruneTrafficHistory(now); err != nil {
		t.Fatal(err)
	}
	got := authRequest(t, env, "GET", "/admin/traffic", env.authToken, nil, 200)
	if got["history_retained_from"] != floor || got["history_retention_days"] != float64(400) || got["lifetime"].(map[string]any)["total_bytes"] != float64(140) || got["totals"].(map[string]any)["total_bytes"] != float64(17) {
		t.Fatal(got)
	}
	old := now.AddDate(0, 0, -400).Format("2006-01-02")
	unavailable := authRequest(t, env, "GET", "/admin/traffic?from="+old+"&to="+floor, env.authToken, nil, 400)
	if unavailable["code"] != "traffic_history_unavailable" || unavailable["history_retained_from"] != floor || unavailable["days"] != nil {
		t.Fatal(unavailable)
	}
	today := now.Format("2006-01-02")
	first := now.AddDate(0, 0, -366).Format("2006-01-02")
	full := authRequest(t, env, "GET", "/admin/traffic?from="+first+"&to="+today, env.authToken, nil, 200)
	if len(full["days"].([]any)) != 367 {
		t.Fatal("maximum supported chart window changed")
	}
	authRequest(t, env, "GET", "/admin/traffic?from="+now.AddDate(0, 0, -367).Format("2006-01-02")+"&to="+today, env.authToken, nil, 400)
	future := now.AddDate(0, 0, 1).Format("2006-01-02")
	invalid := authRequest(t, env, "GET", "/admin/traffic?from="+future+"&to="+future, env.authToken, nil, 400)
	if invalid["code"] != "traffic_history_unavailable" {
		t.Fatal(invalid)
	}
}
