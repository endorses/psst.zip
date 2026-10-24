package deployment

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRestrictedSSHAndConfig(t *testing.T) {
	for _, command := range []string{"status", "update v0.1.6"} {
		if _, err := SSHCommand(command); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range []string{"", " status", "status ", "update v01.2.3", "update v1.2.3;id", "update /tmp/release", "update v1.2.3 --force", "update v1.2.3\nstatus"} {
		if _, err := SSHCommand(command); err == nil {
			t.Fatalf("accepted %q", command)
		}
	}
	c := fixtureConfig(t.TempDir())
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.CheckpointProtection = ""
	if c.Protection() != "off-host" || c.Profile() != "full" {
		t.Fatal("default policy weakened")
	}
	for _, change := range []func(*Config){func(c *Config) { c.VolumeNames["caddy:/config"] = "old_data" }, func(c *Config) { c.DBPath = "/app/data/../other" }, func(c *Config) { c.SignerWorkflow = "other/repository/.github/workflows/release.yml" }, func(c *Config) { c.CheckpointProtection = "none" }, func(c *Config) {
		c.VerificationProfile = "runtime-v1"
		hook := "/protected/hook"
		c.VerificationHook = &hook
	}, func(c *Config) { c.RetentionCount = 1 }} {
		c := fixtureConfig(t.TempDir())
		change(&c)
		if c.Validate() == nil {
			t.Fatal("unsafe config accepted")
		}
	}
}
func tarFixture(t *testing.T, name string, kind byte, mode int64, truncated bool) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "checkpoint.tar")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(f)
	size := int64(3)
	if kind != tar.TypeReg {
		size = 0
	}
	if err = w.WriteHeader(&tar.Header{Name: name, Typeflag: kind, Mode: mode, Size: size, Linkname: "target"}); err != nil {
		t.Fatal(err)
	}
	if size > 0 {
		_, _ = w.Write([]byte("abc"))
	}
	_ = w.Close()
	_ = f.Close()
	if truncated {
		if err = os.Truncate(p, 513); err != nil {
			t.Fatal(err)
		}
	}
	return p
}
func TestCheckpointArchivePolicy(t *testing.T) {
	for _, tc := range []struct {
		name            string
		kind            byte
		mode            int64
		truncate, valid bool
	}{{"./file", tar.TypeReg, 0o600, false, true}, {".", tar.TypeDir, 0o1777, false, true}, {"group", tar.TypeDir, 0o2770, false, true}, {"root", tar.TypeDir, 0o4770, false, false}, {"file", tar.TypeReg, 0o4600, false, false}, {"../escape", tar.TypeReg, 0o600, false, false}, {"/absolute", tar.TypeReg, 0o600, false, false}, {"symlink", tar.TypeSymlink, 0o600, false, false}, {"hardlink", tar.TypeLink, 0o600, false, false}, {"file", tar.TypeReg, 0o600, true, false}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			err := VerifyArchive(tarFixture(t, tc.name, tc.kind, tc.mode, tc.truncate))
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}
func TestAtomicRecordsPreserveLargeIntegersAndRejectDuplicateJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	want := Object{"phase": "preparing", "bytes": json.Number("1152921504606846977")}
	if err := atomicJSON(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := load(p)
	if err != nil || !equal(got, want) || number(got["bytes"]) != 1152921504606846977 {
		t.Fatal(got, err)
	}
	i, err := os.Stat(p)
	if err != nil || i.Mode().Perm() != 0o600 {
		t.Fatal(i, err)
	}
	if err = os.WriteFile(p, []byte(`{"phase":"completed","phase":"preparing"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = load(p); err == nil {
		t.Fatal("duplicate state field accepted")
	}
}
func TestProtectedPathsRejectSymlinksAndPrivateConfigurationLeak(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "config")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	if Protected(link, false, false, true) == nil {
		t.Fatal("symlink accepted")
	}
	if Protected("relative", false, false, true) == nil {
		t.Fatal("relative protected path accepted")
	}
	if os.Geteuid() != 0 && Protected(dir, true, false, false) == nil {
		t.Fatal("unprivileged directory accepted")
	}
}
func TestHostExecutionIgnoresAmbientCredentialsAndWithholdsOutput(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	h := NewHost(fixtureConfig(t.TempDir()), "", "")
	t.Setenv("GH_TOKEN", "should-not-be-inherited")
	t.Setenv("GH_HOST", "foreign.invalid")
	t.Setenv("DOCKER_HOST", "should-not-be-inherited")
	t.Setenv("PATH", t.TempDir())
	out, err := h.run(context.Background(), nil, "", false, "sh", "-c", `printf '%s|%s|%s|%s' "$GH_TOKEN" "$GH_HOST" "$DOCKER_HOST" "$PATH"`)
	if err != nil || string(out) != "|||/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Fatal(string(out), err)
	}
	_, err = h.run(context.Background(), nil, "", false, "sh", "-c", "printf private-fixture-output >&2; exit 7")
	if err == nil || strings.Contains(err.Error(), "private-fixture-output") {
		t.Fatal(err)
	}
	out, err = h.run(context.Background(), nil, "", true, "sh", "-c", `printf '%s|%s' "$GH_HOST" "$GH_TOKEN"`)
	if err != nil || string(out) != "github.com|" {
		t.Fatal(string(out), err)
	}
}
func TestRuntimeBudgetIsSharedAndCommandCancellationIsPrompt(t *testing.T) {
	h := NewHost(fixtureConfig(t.TempDir()), "", "")
	now := time.Now()
	h.Now = func() time.Time { return now }
	record := Object{"id": "transaction", "verification_profile": "runtime-v1"}
	ctx, cancel := h.RuntimeContext(context.Background(), record)
	deadline, _ := ctx.Deadline()
	cancel()
	h.Now = func() time.Time { return now.Add(time.Hour) }
	ctx, cancel = h.RuntimeContext(context.Background(), record)
	defer cancel()
	next, _ := ctx.Deadline()
	if !next.Equal(deadline) {
		t.Fatal("recheck refreshed runtime budget")
	}
	expired, c := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer c()
	started := time.Now()
	if _, err := h.run(expired, nil, "", false, "sh", "-c", "exit 0"); err == nil || time.Since(started) > time.Second {
		t.Fatal("expired command executed or stalled", err)
	}
}
func TestSQLiteReadOnlyIntegrityAndForeignKeys(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not installed")
	}
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "database.sqlite")
			query := "CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER REFERENCES parent(id));"
			if corrupt {
				query += "INSERT INTO child VALUES (99);"
			}
			if out, err := exec.Command(sqlite, p, query).CombinedOutput(); err != nil {
				t.Fatalf("fixture creation: %v %s", err, out)
			}
			before, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			h := NewHost(fixtureConfig(t.TempDir()), "", "")
			err = h.sqliteIntegrity(context.Background(), p)
			if (err != nil) != corrupt {
				t.Fatalf("corrupt=%v err=%v", corrupt, err)
			}
			after, err := os.ReadFile(p)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("readonly check changed database", err)
			}
			expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			if err = h.sqliteIntegrity(expired, p); err == nil {
				t.Fatal("expired database check accepted")
			}
		})
	}
}
func TestRestoreBudgetReconciliationAndCapability(t *testing.T) {
	baseline := Object{"history_sync_version": 1, "max_file_size": 100, "traffic_policy": Object{"server_budget_bytes": 1000, "default_account_budget_bytes": 500, "enforcement": "strict"}}
	observed := clone(baseline)
	observed["history_sync_version"] = 2
	observed["public_transfers_paused"] = true
	object(observed["traffic_policy"])["server_budget_bytes"] = 800
	object(observed["traffic_policy"])["default_account_budget_bytes"] = 400
	if _, err := reconciledSettings(baseline, observed); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(Object){func(v Object) { v["public_transfers_paused"] = false }, func(v Object) { object(v["traffic_policy"])["server_budget_bytes"] = 1001 }, func(v Object) { object(v["traffic_policy"])["default_account_budget_bytes"] = 900 }, func(v Object) { v["max_file_size"] = 200 }, func(v Object) { object(v["traffic_policy"])["enforcement"] = "off" }} {
		v := clone(observed)
		mutate(v)
		if _, err := reconciledSettings(baseline, v); err == nil {
			t.Fatal("unsafe restored settings accepted")
		}
	}
}
func concreteFixture(t *testing.T) (*Host, Object, Object, []any) {
	t.Helper()
	root := t.TempDir()
	config := fixtureConfig(root)
	h := NewHost(config, root, root)
	h.protect = func(string, bool, bool, bool) error { return nil }
	current := fixtureCompose()
	backend := object(object(current["services"])["backend"])
	backend["environment"] = Object{"DB_PATH": config.DBPath, "STORAGE_PATH": "/app/data/files", "PUBLIC_URL": "https://" + config.Domain, "AUTH_ALLOW_INSECURE_HTTP": "false", "ADMIN_USERNAME": "", "ADMIN_PASSWORD": ""}
	for _, item := range object(current["services"]) {
		s := object(item)
		s["read_only"] = true
		s["cap_drop"] = []any{"ALL"}
		s["security_opt"] = []any{"no-new-privileges:true"}
	}
	candidate := clone(current)
	m := fixtureManifest()
	object(object(candidate["services"])["backend"])["image"] = m.Images["backend"].Index
	object(object(candidate["services"])["caddy"])["image"] = m.Images["web"].Index
	var containers []any
	for _, service := range []string{"backend", "caddy"} {
		s := object(object(current["services"])[service])
		var env []any
		for key, value := range object(s["environment"]) {
			env = append(env, key+"="+scalar(value))
		}
		mounts := []any{}
		bindings, err := Bindings(current, service)
		if err != nil {
			t.Fatal(err)
		}
		for dest, volume := range bindings {
			mounts = append(mounts, Object{"Destination": dest, "Name": volume, "Type": "volume", "RW": true})
		}
		ports := Object{}
		if service == "caddy" {
			ports["8443/tcp"] = []any{Object{"HostIp": "0.0.0.0", "HostPort": "443"}}
		}
		containers = append(containers, Object{"Id": service, "Image": "sha256:" + strings.Repeat("0", 64), "Config": Object{"Labels": Object{"com.docker.compose.service": service}, "Env": env, "Image": s["image"]}, "State": Object{"Running": true, "Restarting": false}, "HostConfig": Object{"PortBindings": ports, "ReadonlyRootfs": true, "CapDrop": []any{"ALL"}, "SecurityOpt": []any{"no-new-privileges"}}, "Mounts": mounts})
	}
	for _, volume := range config.VolumeNames {
		path := filepath.Join(root, volume)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "old_backend", "existing.db"), []byte("fixture nonempty private database"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.command = func(ctx context.Context, input []byte, dst string, github bool, args ...string) ([]byte, error) {
		var value any
		switch {
		case strings.Join(args, " ") == "docker version --format {{.Server.Version}}":
			return []byte("29.0.0"), nil
		case strings.Join(args, " ") == "docker compose version --short":
			return []byte("v2.40.0"), nil
		case args[0] == "sqlite3":
			return []byte("1\n"), nil
		case len(args) > 1 && args[1] == "info":
			value = Object{"Architecture": "amd64", "OSType": "linux", "DockerRootDir": root}
		case len(args) > 1 && args[1] == "ps":
			var ids []string
			for _, row := range containers {
				ids = append(ids, text(object(row)["Id"]))
			}
			return []byte(strings.Join(ids, "\n")), nil
		case len(args) > 1 && args[1] == "inspect":
			value = containers
		case len(args) > 2 && args[1] == "volume" && args[2] == "inspect":
			value = []any{Object{"Driver": "local", "Mountpoint": filepath.Join(root, args[3])}}
		case len(args) > 2 && args[1] == "image" && args[2] == "inspect":
			value = []any{Object{"Size": 1}}
		case len(args) > 1 && args[1] == "run":
			value = Object{"public_transfers_paused": false}
		case args[0] == "curl":
			url := args[len(args)-1]
			if strings.HasSuffix(url, "/api/v1/config") {
				value = Object{"max_file_size": 100, "history_sync_version": 1, "public_transfers_paused": false}
			} else {
				value = Object{"setup_required": false}
			}
		default:
			return nil, fmt.Errorf("unexpected fixture command")
		}
		return json.Marshal(value)
	}
	return h, current, candidate, containers
}
func TestConcretePreflightRejectsStorageEnvironmentAndPortDrift(t *testing.T) {
	for _, drift := range []string{"", "mount", "port", "setting", "hardening", "owner", "network", "image"} {
		t.Run(drift, func(t *testing.T) {
			h, current, candidate, containers := concreteFixture(t)
			switch drift {
			case "mount":
				object(array(object(containers[0])["Mounts"])[0])["Name"] = "foreign"
			case "port":
				object(array(object(object(containers[1])["HostConfig"])["PortBindings"].(map[string]any)["8443/tcp"])[0])["HostPort"] = "8443"
			case "setting":
				object(object(object(candidate["services"])["backend"])["environment"])["STORAGE_PATH"] = "/different"
			case "hardening":
				object(object(candidate["services"])["backend"])["read_only"] = false
			case "owner":
				object(object(candidate["services"])["backend"])["user"] = "0"
			case "network":
				candidate["networks"] = Object{"foreign": Object{}}
			case "image":
				object(object(candidate["services"])["backend"])["image"] = "untrusted"
			}
			_, err := h.Preflight(context.Background(), current, candidate, fixtureManifest(), filepath.Join(h.Backups, "checkpoint"))
			if (err != nil) != (drift != "") {
				t.Fatalf("drift %s err %v", drift, err)
			}
		})
	}
}

func TestOutputLimitCancelsProducerImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buffer := limitedOutput{limit: 4, cancel: cancel}
	if _, err := buffer.Write([]byte("more than four bytes")); err == nil {
		t.Fatal("output limit ignored")
	}
	if ctx.Err() == nil {
		t.Fatal("oversized producer not cancelled")
	}
	if buffer.Len() != 0 {
		t.Fatal("oversized private bytes retained")
	}
}
