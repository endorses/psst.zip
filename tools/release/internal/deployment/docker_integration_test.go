package deployment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// This test runs only inside testdata/docker-smoke.sh's disposable root daemon.
// Acquisition uses already loaded local image IDs; it makes no provenance claim.
type dockerFixtureHost struct {
	*Host
	manifest contract.ManifestV1
	fail     bool
}

func (h *dockerFixtureHost) Download(_ context.Context, version, target string) (contract.ManifestV1, error) {
	if version != h.manifest.Version {
		return contract.ManifestV1{}, fmt.Errorf("fixture version differs")
	}
	dir := filepath.Join(target, "bundle", "deploy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return contract.ManifestV1{}, err
	}
	if err := copyFile("/opt/fixture/compose.release.yml", filepath.Join(dir, "compose.release.yml"), 0o644); err != nil {
		return contract.ManifestV1{}, err
	}
	return h.manifest, nil
}

func (h *dockerFixtureHost) AutomaticChecks(ctx context.Context, compose, transaction Object) error {
	if err := h.Host.AutomaticChecks(ctx, compose, transaction); err != nil {
		return err
	}
	if h.fail && !boolean(transaction["restoring"]) {
		// Leave an independently visible post-checkpoint change on the original
		// volume. Restore must preserve it and start old code only on new storage.
		_, err := h.run(ctx, nil, "", false, "sqlite3", text(object(transaction["adopted"])["database"]), "CREATE TABLE disposable_post_checkpoint(value TEXT); INSERT INTO disposable_post_checkpoint VALUES('preserve original');")
		if err != nil {
			return err
		}
		return fmt.Errorf("injected failure after real candidate checks")
	}
	return nil
}

func TestDisposableDockerUpdateRecoveryRetention(t *testing.T) {
	if os.Getenv("PSST_DISPOSABLE_DOCKER") != "1" {
		t.Skip("run testdata/docker-smoke.sh explicitly")
	}
	if os.Geteuid() != 0 {
		t.Fatal("disposable root runner required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	root := "/opt/go-updater-fixture"
	installation := filepath.Join(root, "installation")
	state, backups := filepath.Join(root, "state"), filepath.Join(root, "backups")
	for _, p := range []string{installation, state, backups, filepath.Join(state, "transactions")} {
		if err := privateDirectory(p); err != nil {
			t.Fatal(err)
		}
	}
	writeFile := func(path, value string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyFile("/opt/fixture/compose.release.yml", filepath.Join(installation, "compose.yml"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(installation, "Caddyfile"), `{
 http_port 8080
 https_port 8443
 skip_install_trust
}
{$PSST_DOMAIN} {
 tls internal
 handle /api/* {
  reverse_proxy backend:8080
 }
 handle {
  root * /srv/web
  try_files {path} /index.html
  file_server
 }
}
`, 0o644)
	if err := atomicJSON(filepath.Join(installation, "override.json"), Object{"services": Object{"caddy": Object{"volumes": []any{Object{"type": "bind", "source": filepath.Join(installation, "Caddyfile"), "target": "/etc/caddy/Caddyfile", "read_only": true}}}}}); err != nil {
		t.Fatal(err)
	}
	c := fixtureConfig(installation)
	c.Project = "psst-go-updater-fixture"
	c.Domain = "fixture.example.test"
	c.DBPath = "/app/data/psst.db"
	c.VerificationProfile = "runtime-v1"
	c.ComposeFiles = []string{"compose.yml", "override.json"}
	c.ReleaseOverrides = []string{"override.json"}
	c.VolumeNames = map[string]string{"backend:/app/data": "psst-go-fixture-data", "caddy:/data": "psst-go-fixture-caddy-data", "caddy:/config": "psst-go-fixture-caddy-config"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	var real = NewHost(c, state, backups)
	run := func(args ...string) []byte {
		t.Helper()
		b, err := real.run(ctx, nil, "", false, args...)
		if err != nil {
			t.Fatalf("fixture command %s: %v", args[0], err)
		}
		return b
	}
	imageID := func(ref string) string {
		t.Helper()
		v, err := real.jsonList(ctx, "docker", "image", "inspect", ref)
		if err != nil || len(v) != 1 {
			t.Fatal("fixture image missing", ref, err)
		}
		return text(object(v[0])["Id"])
	}
	priorBackend, priorWeb := imageID("psst-go-fixture-prior-backend:local"), imageID("psst-go-fixture-prior-web:local")
	backend, web := imageID("psst-go-fixture-candidate-backend:local"), imageID("psst-go-fixture-candidate-web:local")
	password, err := nonce()
	if err != nil {
		t.Fatal(err)
	}
	writeEnv := func(bootstrap bool) {
		user, pass := "", ""
		if bootstrap {
			user, pass = "fixture-admin", password
		}
		writeFile(filepath.Join(installation, ".env"), fmt.Sprintf("BACKEND_IMAGE=%s\nWEB_IMAGE=%s\nPSST_DOMAIN=%s\nPUBLIC_URL=https://%s\nHTTP_PORT=18080\nHTTPS_PORT=18443\nBACKEND_DATA_VOLUME=%s\nCADDY_DATA_VOLUME=%s\nCADDY_CONFIG_VOLUME=%s\nADMIN_USERNAME=%s\nADMIN_PASSWORD=%s\n", priorBackend, priorWeb, c.Domain, c.Domain, c.VolumeNames["backend:/app/data"], c.VolumeNames["caddy:/data"], c.VolumeNames["caddy:/config"], user, pass), 0o600)
	}
	writeEnv(true)
	initial, err := real.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initialPath := filepath.Join(installation, "initial.compose.json")
	if err := atomicJSON(initialPath, initial); err != nil {
		t.Fatal(err)
	}
	if err := real.Compose(ctx, initialPath, "up", "-d", "--no-build", "--pull", "never"); err != nil {
		t.Fatal(err)
	}
	volumes, err := real.jsonList(ctx, "docker", "volume", "inspect", c.VolumeNames["caddy:/data"])
	if err != nil || len(volumes) != 1 {
		t.Fatal("fixture CA volume unavailable", err)
	}
	ca := filepath.Join(text(object(volumes[0])["Mountpoint"]), "caddy", "pki", "authorities", "local", "root.crt")
	for attempt := 0; !exists(ca) && attempt < 60; attempt++ {
		if err := wait(ctx, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	trustedCA := filepath.Join(installation, "fixture-ca.crt")
	if err := copyFile(ca, trustedCA, 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHost(c, state, backups)
	// Only the test CA and local acquisition differ from the installed adapter.
	// Real TLS verification remains enabled; no global trust store is modified.
	h.command = func(ctx context.Context, input []byte, destination string, github bool, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "docker" && args[1] == "pull" {
			return nil, nil // The exact candidate bytes are already loaded locally.
		}
		if len(args) > 0 && args[0] == "curl" {
			args = append([]string{"curl", "--cacert", trustedCA}, args[1:]...)
		}
		return real.run(ctx, input, destination, github, args...)
	}
	for attempt := 0; attempt < 40; attempt++ {
		auth, e := h.httpsJSON(ctx, initial, "/api/v1/auth/status", "")
		if e == nil && auth["setup_required"] == false {
			break
		}
		if attempt == 39 {
			t.Fatal("fixture administrator bootstrap failed")
		}
		if err := wait(ctx, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := h.httpsJSON(ctx, initial, "/api/v1/config", "")
	if err != nil {
		t.Fatal(err)
	}
	delete(baseline, "public_transfers_paused")
	writeEnv(false)
	initial, err = h.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(initialPath, initial); err != nil {
		t.Fatal(err)
	}
	if err := h.Compose(ctx, initialPath, "up", "-d", "--no-build", "--pull", "never"); err != nil {
		t.Fatal(err)
	}
	if err := h.OriginalHealth(ctx, initial, Object{"baseline_config": baseline}); err != nil {
		t.Fatal(err)
	}
	// Import the existing v1 active/state shape without running the Python helper.
	if err := atomicJSON(filepath.Join(state, "active.json"), Object{"transaction": "source-installation", "version": "v0.0.0", "compose": initial}); err != nil {
		t.Fatal(err)
	}
	m := fixtureManifest()
	m.Version = "v0.0.0"
	m.Source.Commit = "750f440d143d6766d83b13e1ce789436471ae029"
	m.Images["backend"] = contract.ImageV1{Index: backend}
	m.Images["web"] = contract.ImageV1{Index: web}
	fixture := &dockerFixtureHost{Host: h, manifest: m}
	u := New(c, fixture, state, backups)
	completed, err := u.Update(ctx, m.Version)
	if err != nil || completed["phase"] != "completed" {
		t.Fatal("live runtime update did not activate", err)
	}
	t.Log("PASS live runtime update: protected v1 active import, preflight, image ownership, stopped checkpoint, TLS/runtime/SQLite checks, public activation")
	fixture.fail = true
	if _, err := u.Update(ctx, m.Version); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatal("post-startup failure not reached", err)
	}
	failed, err := u.Read()
	if err != nil || failed["phase"] != "failed-closed" || failed["mutation_started"] != true {
		t.Fatal("post-startup failure did not persist failed-closed", err)
	}
	if running := strings.TrimSpace(string(run("docker", "ps", "-q", "--filter", "label=com.docker.compose.project="+c.Project))); running != "" {
		t.Fatal("services survived failed-closed")
	}
	if err := u.RecoverSafe(ctx); err == nil {
		t.Fatal("old-binary recovery accepted after mutation")
	}
	originalDB := text(object(failed["adopted"])["database"])
	before := strings.TrimSpace(string(run("sqlite3", "-readonly", originalDB, "SELECT value FROM disposable_post_checkpoint;")))
	restored, err := u.Restore(ctx)
	if err != nil || restored["phase"] != "restored-awaiting-verification" {
		t.Fatal("live explicit restore did not retain full gate", err)
	}
	if after := strings.TrimSpace(string(run("sqlite3", "-readonly", originalDB, "SELECT value FROM disposable_post_checkpoint;"))); before != "preserve original" || after != before {
		t.Fatal("restore changed original post-checkpoint storage")
	}
	newNames := object(restored["restored_volumes"])
	if len(newNames) != 3 {
		t.Fatal("restore did not create all separate volumes")
	}
	for old, value := range newNames {
		if old == value || !strings.HasPrefix(text(value), "psst-restore-") {
			t.Fatal("restore reused source storage")
		}
	}
	newVolumes, err := real.jsonList(ctx, "docker", "volume", "inspect", text(newNames[c.VolumeNames["backend:/app/data"]]))
	if err != nil || len(newVolumes) != 1 {
		t.Fatal("restored database volume unavailable", err)
	}
	newDB := filepath.Join(text(object(newVolumes[0])["Mountpoint"]), "psst.db")
	if got := strings.TrimSpace(string(run("sqlite3", "-readonly", newDB, "SELECT count(*) FROM sqlite_master WHERE name='disposable_post_checkpoint';"))); got != "0" {
		t.Fatal("restore was not from the stopped checkpoint")
	}
	t.Log("PASS live post-start failure: every project service stopped; recover-safe rejected; original changed database preserved; matching checkpoint image pair starts only on three new volumes; full restore verification remains pending")
	testDockerRetention(t, ctx, real, root, backend)
}

func testDockerRetention(t *testing.T, ctx context.Context, h *Host, root, activeImage string) {
	t.Helper()
	state, backups := filepath.Join(root, "retention-state"), filepath.Join(root, "retention-backups")
	for _, p := range []string{state, backups, filepath.Join(state, "transactions")} {
		if err := privateDirectory(p); err != nil {
			t.Fatal(err)
		}
	}
	u := New(h.Config, h, state, backups)
	var current Object
	var expired, foreign, stopped, retained string
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("20261023T12000%dZ-00000000000%d", i, i)
		image := activeImage
		if i < 5 {
			rows, err := h.jsonList(ctx, "docker", "image", "inspect", fmt.Sprintf("psst-go-fixture-retention-%d:local", i))
			if err != nil || len(rows) != 1 {
				t.Fatal("retention fixture image missing", err)
			}
			image = text(object(rows[0])["Id"])
		}
		images := Object{"backend": image, "caddy": image}
		dir := filepath.Join(state, "transactions", id)
		checkpoint := filepath.Join(backups, id)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(checkpoint, 0o700); err != nil {
			t.Fatal(err)
		}
		compose := Object{"services": Object{"backend": Object{"image": image}, "caddy": Object{"image": image}}}
		record := Object{"schema_version": 1, "id": id, "version": "v0.0.0", "phase": "completed", "checkpoint": checkpoint, "adopted": Object{"images": images}, "restoring": false}
		// Historical fixture checkpoints are already marked as being pruned.
		// Full real checkpoint verification is exercised by update and restore.
		if i < 4 {
			record["checkpoint_prune_started"] = true
		}
		for path, value := range map[string]Object{filepath.Join(dir, "transaction.json"): record, filepath.Join(dir, "public.compose.json"): compose, filepath.Join(checkpoint, "checkpoint.json"): {"transaction": id, "images": images}} {
			if err := atomicJSON(path, value); err != nil {
				t.Fatal(err)
			}
		}
		tag := "psst-checkpoint-" + strings.ToLower(id) + ":0"
		if _, err := h.run(ctx, nil, "", false, "docker", "image", "tag", image, tag); err != nil {
			t.Fatal(err)
		}
		if i < 5 {
			if _, err := h.run(ctx, nil, "", false, "docker", "image", "rm", "--no-prune", fmt.Sprintf("psst-go-fixture-retention-%d:local", i)); err != nil {
				t.Fatal(err)
			}
		}
		switch i {
		case 1:
			expired = image
		case 2:
			foreign = image
			if _, err := h.run(ctx, nil, "", false, "docker", "image", "tag", image, "operator-owned:keep"); err != nil {
				t.Fatal(err)
			}
		case 3:
			stopped = image
			if _, err := h.run(ctx, nil, "", false, "docker", "create", "--name", "unrelated-stopped-fixture", "--entrypoint", "/bin/true", image); err != nil {
				t.Fatal(err)
			}
		case 4:
			retained = image
		case 5:
			current = record
			if err := atomicJSON(filepath.Join(state, "active.json"), Object{"transaction": id, "version": "v0.0.0", "compose": compose}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := u.write(current, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := u.Retention(ctx); err != nil {
		t.Fatal("live scoped retention failed", err)
	}
	if _, err := h.run(ctx, nil, "", false, "docker", "image", "inspect", expired); err == nil {
		t.Fatal("expired updater-only image survived")
	}
	for _, image := range []string{foreign, stopped, retained, activeImage} {
		if _, err := h.run(ctx, nil, "", false, "docker", "image", "inspect", image); err != nil {
			t.Fatal("protected image removed", err)
		}
	}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("20261023T12000%dZ-00000000000%d", i, i)
		if exists(filepath.Join(backups, id)) != (i >= 4) {
			t.Fatal("retention checkpoint selection differs")
		}
	}
	t.Log("PASS real Docker retention: expired owned image/checkpoints removed; foreign alias, stopped unrelated container, retained checkpoint and active images survived")
}
