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

func fixtureConfig(root string) Config {
	return Config{Repository: "endorses/psst.zip", SignerWorkflow: "endorses/psst.zip/.github/workflows/release.yml", Installation: root, Project: "psst-zip", Environment: ".env", ComposeFiles: []string{"compose.yml"}, OperatorFiles: []string{"Caddyfile"}, ReleaseOverrides: []string{}, Domain: "transfer.example.com", DBPath: "/app/data/existing.db", VolumeNames: map[string]string{"backend:/app/data": "old_backend", "caddy:/data": "old_data", "caddy:/config": "old_config"}, DiskReserveBytes: 1 << 30, ImageReserveBytes: 1 << 30, RetentionCount: 2, CheckpointProtection: "local-only"}
}
func fixtureManifest() contract.ManifestV1 {
	return contract.ManifestV1{SchemaVersion: 1, Version: "v1.2.3", PayloadProfile: "deployment-ready", Source: contract.SourceV1{Repository: "endorses/psst.zip", Commit: strings.Repeat("a", 40), ArchiveURL: "https://github.com/endorses/psst.zip/archive/" + strings.Repeat("a", 40) + ".tar.gz"}, Platforms: []string{"linux/amd64", "linux/arm64"}, Images: map[string]contract.ImageV1{"backend": {Index: "ghcr.io/endorses/psst-zip-backend@sha256:" + strings.Repeat("b", 64), PlatformDigests: map[string]string{"linux/amd64": "sha256:" + strings.Repeat("c", 64), "linux/arm64": "sha256:" + strings.Repeat("d", 64)}}, "web": {Index: "ghcr.io/endorses/psst-zip-web@sha256:" + strings.Repeat("e", 64), PlatformDigests: map[string]string{"linux/amd64": "sha256:" + strings.Repeat("f", 64), "linux/arm64": "sha256:" + strings.Repeat("1", 64)}}}, Bundle: contract.BundleV1{Name: "psst.zip-deployment-v1.2.3.tar.gz", SHA256: strings.Repeat("2", 64)}, Requirements: contract.RequirementsV1{Docker: "27.0.0", Compose: "2.24.4"}, Notes: contract.NotesV1{CheckpointRequired: true, Migration: "Migration is a boundary.", Rollback: "Restore onto new volumes."}, Build: contract.BuildV1{BaseImages: map[string]string{"golang": "golang@sha256:" + strings.Repeat("3", 64), "node": "node@sha256:" + strings.Repeat("4", 64), "alpine": "alpine@sha256:" + strings.Repeat("5", 64), "caddy": "caddy@sha256:" + strings.Repeat("6", 64)}, Toolchains: map[string]string{"go": "1.27.3", "node": "24.0.0"}}}
}
func fixtureCompose() Object {
	return Object{"services": Object{"backend": Object{"image": "old-backend", "environment": Object{"DB_PATH": "/app/data/existing.db"}, "volumes": []any{Object{"type": "volume", "source": "backend", "target": "/app/data"}}}, "caddy": Object{"image": "old-web", "ports": []any{Object{"published": "443", "target": 8443, "protocol": "tcp", "host_ip": "0.0.0.0"}}, "volumes": []any{Object{"type": "volume", "source": "data", "target": "/data"}, Object{"type": "volume", "source": "config", "target": "/config"}}}}, "volumes": Object{"backend": Object{"name": "old_backend"}, "data": Object{"name": "old_data"}, "config": Object{"name": "old_config"}}}
}
func fixtureReport(t Object) Object {
	identity := object(t["verification_identity"])
	if identity == nil {
		identity = Object{"version": t["version"], "source_commit": object(object(t["manifest"])["source"])["commit"]}
	}
	r := clone(identity)
	r["observed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	checks := Object{}
	names := flowChecks
	if boolean(t["restoring"]) {
		names = append(append([]string{}, names...), restoreChecks...)
	}
	for _, name := range names {
		checks[name] = "Observed fixture invariant: " + name
	}
	r["checks"] = checks
	return r
}

type fakeHost struct {
	u                      *Updater
	fail                   string
	calls                  []string
	paused, hook, restored bool
	images                 map[string]ImageFacts
	containers             map[string]bool
	removed                []string
	mutated                bool
}

func (f *fakeHost) step(name string) error {
	f.calls = append(f.calls, name)
	if f.fail == name {
		return fmt.Errorf("injected fixture failure")
	}
	return nil
}
func (f *fakeHost) ProtectedInputs() ([]string, error) { return nil, f.step("configuration") }
func (f *fakeHost) Download(ctx context.Context, v, target string) (contract.ManifestV1, error) {
	m := fixtureManifest()
	m.Version = v
	return m, f.step("authenticate")
}
func (f *fakeHost) Current(context.Context) (Object, error) {
	return fixtureCompose(), f.step("inspect-current")
}
func (f *fakeHost) Candidate(ctx context.Context, target string, m contract.ManifestV1) (Object, error) {
	c := fixtureCompose()
	object(object(c["services"])["backend"])["image"] = m.Images["backend"].Index
	object(object(c["services"])["caddy"])["image"] = m.Images["web"].Index
	return c, f.step("candidate-config")
}
func (f *fakeHost) Preflight(context.Context, Object, Object, contract.ManifestV1, string) (Object, error) {
	return Object{"images": Object{"backend": "sha256:" + strings.Repeat("0", 64), "caddy": "sha256:" + strings.Repeat("1", 64)}, "platform": "linux/amd64", "volumes": Object{"old_backend": Object{}}, "baseline_config": Object{"max_file_size": 100}, "database": "fixture-only"}, f.step("preflight")
}
func (f *fakeHost) Pull(context.Context, contract.ManifestV1, string) error { return f.step("pull") }
func (f *fakeHost) Ownership(context.Context, contract.ManifestV1, Object) error {
	return f.step("ownership")
}
func (f *fakeHost) ValidateCaddy(context.Context, string) error { return f.step("proxy-validation") }
func (f *fakeHost) Incident(ctx context.Context, c Object, action string) (bool, error) {
	err := f.step("incident-" + action)
	if err == nil && action != "incident-status" {
		f.paused = action == "pause"
	}
	return f.paused, err
}
func (f *fakeHost) Stopped(context.Context, string, Object) error { return f.step("stop-writers") }
func (f *fakeHost) FreePorts(Object) error                        { return f.step("free-ports") }
func (f *fakeHost) Backup(ctx context.Context, c, a Object, p string, t Object) error {
	if err := f.step("backup"); err != nil {
		return err
	}
	return os.Mkdir(p, 0o700)
}
func (f *fakeHost) Compose(ctx context.Context, path string, args ...string) error {
	if args[0] == "up" && filepath.Base(path) == "private.compose.json" {
		t, err := f.u.Read()
		if err != nil {
			return err
		}
		if !boolean(t["mutation_started"]) || !has([]string{"migration-starting", "restore-starting"}, text(t["phase"])) {
			return fmt.Errorf("startup before durable boundary")
		}
		c, err := load(path)
		if err != nil {
			return err
		}
		for _, row := range object(c["services"]) {
			for _, item := range array(object(row)["ports"]) {
				if object(item)["host_ip"] != "127.0.0.1" {
					return fmt.Errorf("public candidate startup")
				}
			}
		}
		f.mutated = true
		if f.restored {
			return f.step("start-restored")
		}
		return f.step("start-candidate")
	}
	if args[0] == "up" && filepath.Base(path) == "public.compose.json" {
		t, _ := f.u.Read()
		if t["phase"] != "activating" {
			return fmt.Errorf("ungated public route")
		}
		return f.step("open-public")
	}
	if args[0] == "up" {
		return f.step("restart-original")
	}
	return f.step("stop-candidate")
}
func (f *fakeHost) OriginalHealth(context.Context, Object, Object) error { return f.step("old-health") }
func (f *fakeHost) AutomaticChecks(ctx context.Context, c, t Object) error {
	if !f.paused {
		return fmt.Errorf("checks after resume")
	}
	if err := f.step("health"); err != nil {
		return err
	}
	if profile(t) == "runtime-v1" {
		return f.step("runtime-database")
	}
	return nil
}
func (f *fakeHost) VerificationHook(ctx context.Context, p, t string) (Object, error) {
	if err := f.step("flow-verification"); err != nil {
		return nil, err
	}
	if f.hook {
		v, e := load(t)
		return fixtureReport(v), e
	}
	return nil, nil
}
func (f *fakeHost) HTTPS(context.Context, Object, string, string, bool) ([]byte, error) {
	return []byte(`{"max_file_size":100,"public_transfers_paused":true}`), nil
}
func (f *fakeHost) VerifyBackup(p string) (Object, error) {
	if err := f.step("verify-checkpoint"); err != nil {
		return nil, err
	}
	id := filepath.Base(p)
	t, e := load(filepath.Join(f.u.State, "transactions", id, "transaction.json"))
	return Object{"transaction": id, "images": object(t["adopted"])["images"]}, e
}
func (f *fakeHost) Restore(context.Context, string, string, Object) (Object, error) {
	f.restored = true
	f.paused = true
	c := fixtureCompose()
	object(object(c["volumes"])["backend"])["name"] = "new-volume"
	return c, f.step("restore")
}
func (f *fakeHost) RestartPolicy(context.Context) error { return f.step("restart-policy") }
func (f *fakeHost) RetentionInventory(context.Context) (map[string]ImageFacts, map[string]bool, error) {
	return f.images, f.containers, f.step("retention-inventory")
}
func (f *fakeHost) RemoveImage(ctx context.Context, id string) error {
	f.removed = append(f.removed, id)
	delete(f.images, id)
	for image, facts := range f.images {
		delete(facts.Tags, id)
		f.images[image] = facts
	}
	return nil
}
func newFixture(t *testing.T) (*Updater, *fakeHost) {
	t.Helper()
	root := t.TempDir()
	state, backup := filepath.Join(root, "state"), filepath.Join(root, "backups")
	if err := os.MkdirAll(filepath.Join(state, "transactions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	f := &fakeHost{}
	u := New(fixtureConfig(root), f, state, backup)
	f.u = u
	return u, f
}
func TestBeforeAndAfterMigrationFailureBoundaries(t *testing.T) {
	cases := []struct {
		step, phase      string
		restart, mutated bool
	}{{"configuration", "failed-safe", false, false}, {"authenticate", "failed-safe", false, false}, {"inspect-current", "failed-safe", false, false}, {"candidate-config", "failed-safe", false, false}, {"preflight", "failed-safe", false, false}, {"pull", "failed-safe", false, false}, {"ownership", "failed-safe", false, false}, {"proxy-validation", "failed-safe", false, false}, {"incident-pause", "failed-safe", false, false}, {"stop-writers", "failed-safe", true, false}, {"free-ports", "failed-safe", true, false}, {"backup", "failed-safe", true, false}, {"start-candidate", "failed-closed", false, true}, {"health", "failed-closed", false, true}, {"flow-verification", "failed-closed", false, true}, {"open-public", "failed-closed", false, true}, {"restart-policy", "failed-closed", false, true}}
	for _, tc := range cases {
		t.Run(tc.step, func(t *testing.T) {
			u, f := newFixture(t)
			f.fail = tc.step
			f.hook = true
			if _, err := u.Update(context.Background(), "v1.2.3"); err == nil {
				t.Fatal("fault accepted")
			}
			record, err := u.Read()
			if err != nil {
				t.Fatal(err)
			}
			if record["phase"] != tc.phase || has(f.calls, "restart-original") != tc.restart || f.mutated != tc.mutated {
				t.Fatalf("phase=%v calls=%v mutated=%v", record["phase"], f.calls, f.mutated)
			}
			if tc.mutated {
				if err := u.RecoverSafe(context.Background()); err == nil {
					t.Fatal("old binary recovery allowed after migration")
				}
			}
		})
	}
}
func TestManualAndRuntimeVerification(t *testing.T) {
	t.Run("manual pending", func(t *testing.T) {
		u, f := newFixture(t)
		record, err := u.Update(context.Background(), "v1.2.3")
		if err != nil || record["phase"] != "awaiting-verification" || has(f.calls, "open-public") {
			t.Fatalf("record=%v err=%v", record, err)
		}
		if _, err = u.Update(context.Background(), "v1.2.4"); err == nil {
			t.Fatal("unresolved candidate replaced")
		}
	})
	for _, runtime := range []bool{false, true} {
		t.Run(fmt.Sprint("automatic ", runtime), func(t *testing.T) {
			u, f := newFixture(t)
			if runtime {
				u.Config.VerificationProfile = "runtime-v1"
			} else {
				f.hook = true
			}
			record, err := u.Update(context.Background(), "v1.2.3")
			if err != nil || record["phase"] != "completed" || record["active_version"] != "v1.2.3" || !has(f.calls, "open-public") || f.paused {
				t.Fatalf("phase=%v err=%v calls=%v", record, err, f.calls)
			}
			if runtime && !has(f.calls, "runtime-database") {
				t.Fatal("runtime DB integrity omitted")
			}
			active, err := load(filepath.Join(u.State, "active.json"))
			if err != nil || active["version"] != "v1.2.3" {
				t.Fatal(active, err)
			}
		})
	}
}
func TestRuntimeDatabaseFailureAndPriorPause(t *testing.T) {
	u, f := newFixture(t)
	u.Config.VerificationProfile = "runtime-v1"
	f.fail = "runtime-database"
	if _, err := u.Update(context.Background(), "v1.2.3"); err == nil {
		t.Fatal("integrity fault accepted")
	}
	r, _ := u.Read()
	if r["phase"] != "failed-closed" || has(f.calls, "restart-original") {
		t.Fatal(r, f.calls)
	}
	u, f = newFixture(t)
	f.hook = true
	f.paused = true
	r, err := u.Update(context.Background(), "v1.2.3")
	if err != nil || r["phase"] != "completed" || !f.paused {
		t.Fatal(r, err)
	}
}
func TestRestoreCopiesIntoNewStorageAndRequiresFullGate(t *testing.T) {
	u, f := newFixture(t)
	u.Config.VerificationProfile = "runtime-v1"
	f.fail = "health"
	if _, err := u.Update(context.Background(), "v1.2.3"); err == nil {
		t.Fatal("fault accepted")
	}
	f.fail = ""
	record, err := u.Restore(context.Background())
	if err != nil || record["phase"] != "restored-awaiting-verification" || !f.restored {
		t.Fatal(record, err)
	}
	if has(f.calls, "restart-original") {
		t.Fatal("restored original inplace")
	}
	if _, err = u.Restore(context.Background()); err == nil {
		t.Fatal("partial restore overwritten")
	}
	report := fixtureReport(record)
	if err = u.verifyEvidence(context.Background(), record, report); err != nil {
		t.Fatal(err)
	}
	if err = u.activate(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}
func TestRuntimeReportCannotCrossProfiles(t *testing.T) {
	u, _ := newFixture(t)
	m := fixtureManifest()
	tRecord := Object{"version": m.Version, "manifest": manifestObject(m), "verification_profile": "runtime-v1", "phase": "awaiting-verification"}
	r := runtimeEvidence(tRecord, time.Now())
	if err := RequireFlowReport(r, false, "runtime-v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := RequireFlowReport(r, false, "full", time.Now()); err == nil {
		t.Fatal("runtime report used for full gate")
	}
	if err := RequireFlowReport(r, true, "runtime-v1", time.Now()); err == nil {
		t.Fatal("runtime report used for restore")
	}
	tRecord["id"] = "fixture"
	r["version"] = "v9.9.9"
	if err := u.verifyEvidence(context.Background(), tRecord, r); err == nil {
		t.Fatal("wrong candidate report accepted")
	}
}

func TestLegacyV1ConfigAndStateImportWithoutRewritingCheckpoint(t *testing.T) {
	u, _ := newFixture(t)
	config := fixtureConfig(u.Config.Installation)
	config.CheckpointProtection = ""
	config.VerificationProfile = ""
	encoded, err := contract.JSON(config)
	if err != nil {
		t.Fatal(err)
	}
	c, err := DecodeConfig(encoded)
	if err != nil || c.Protection() != "off-host" || c.Profile() != "full" {
		t.Fatal(c, err)
	}
	var raw Object
	_ = decodeHost(encoded, maxJSON, &raw)
	delete(raw, "external_proxy")
	bad, _ := contract.JSON(raw)
	if _, err = DecodeConfig(bad); err == nil {
		t.Fatal("missing false boolean accepted")
	}
	_ = decodeHost(encoded, maxJSON, &raw)
	raw["external_proxy"] = nil
	bad, _ = contract.JSON(raw)
	if _, err = DecodeConfig(bad); err == nil {
		t.Fatal("null external proxy flag accepted")
	}
	_ = decodeHost(encoded, maxJSON, &raw)
	raw["operator_files"] = nil
	bad, _ = contract.JSON(raw)
	if _, err = DecodeConfig(bad); err == nil {
		t.Fatal("null file list accepted")
	}
	for _, key := range []string{"checkpoint_protection", "verification_profile"} {
		for _, value := range []any{nil, "", false, "unexpected"} {
			_ = decodeHost(encoded, maxJSON, &raw)
			raw[key] = value
			bad, _ = contract.JSON(raw)
			if _, err = DecodeConfig(bad); err == nil {
				t.Fatalf("explicit invalid policy %s accepted", key)
			}
		}
	}
	// The byte format is the legacy helper's durable state, including absent
	// verification_profile and a null previous_version from initial adoption.
	legacy := `{"schema_version":1,"id":"20261009T104935Z-6ac521b2a4c6","version":"v0.1.6","active_version":"v0.1.6","previous_version":null,"phase":"completed","checkpoint_protection":"local-only","prior_pause":false,"mutation_started":true,"restoring":false,"cleanup":{"status":"completed"},"updated_at":"2026-10-09T11:42:08.997421+00:00"}`
	path := filepath.Join(u.State, "transaction.json")
	if err = os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := u.Read()
	if err != nil || profile(r) != "full" || r["active_version"] != "v0.1.6" {
		t.Fatal(r, err)
	}
	next, _, err := u.new("v0.1.7")
	if err != nil || next["previous_version"] != "v0.1.6" {
		t.Fatal(next, err)
	}
}
