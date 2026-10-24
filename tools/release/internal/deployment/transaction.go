package deployment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// Operations is the concrete host boundary, replaced by fault-injection tests.
// No implementation may restart the old binary after migration-starting.
type Operations interface {
	ProtectedInputs() ([]string, error)
	Download(context.Context, string, string) (contract.ManifestV1, error)
	Current(context.Context) (Object, error)
	Candidate(context.Context, string, contract.ManifestV1) (Object, error)
	Preflight(context.Context, Object, Object, contract.ManifestV1, string) (Object, error)
	Pull(context.Context, contract.ManifestV1, string) error
	Ownership(context.Context, contract.ManifestV1, Object) error
	ValidateCaddy(context.Context, string) error
	Incident(context.Context, Object, string) (bool, error)
	Stopped(context.Context, string, Object) error
	FreePorts(Object) error
	Backup(context.Context, Object, Object, string, Object) error
	Compose(context.Context, string, ...string) error
	OriginalHealth(context.Context, Object, Object) error
	AutomaticChecks(context.Context, Object, Object) error
	VerificationHook(context.Context, string, string) (Object, error)
	HTTPS(context.Context, Object, string, string, bool) ([]byte, error)
	VerifyBackup(string) (Object, error)
	Restore(context.Context, string, string, Object) (Object, error)
	RestartPolicy(context.Context) error
	RetentionInventory(context.Context) (map[string]ImageFacts, map[string]bool, error)
	RemoveImage(context.Context, string) error
}

// Updater serializes complete protected records. A failure always retains the
// checkpoint and records whether normal candidate startup could have migrated it.
type Updater struct {
	Config         Config
	Host           Operations
	State, Backups string
	Now            func() time.Time
	protect        func(string, bool, bool, bool) error
}

func New(c Config, h Operations, state, backups string) *Updater {
	return &Updater{Config: c, Host: h, State: state, Backups: backups, Now: time.Now}
}
func (u *Updater) Read() (Object, error) {
	p := filepath.Join(u.State, "transaction.json")
	if !exists(p) {
		return nil, nil
	}
	return load(p)
}
func (u *Updater) write(t Object, phase string) error {
	t["phase"] = phase
	t["updated_at"] = u.Now().UTC().Format(time.RFC3339Nano)
	if err := atomicJSON(filepath.Join(u.State, "transaction.json"), t); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(u.target(t), "transaction.json"), t)
}
func (u *Updater) target(t Object) string {
	return filepath.Join(u.State, "transactions", text(t["id"]))
}
func (u *Updater) new(version string) (Object, string, error) {
	previous, err := u.Read()
	if err != nil {
		return nil, "", err
	}
	if previous != nil && !set("completed", "failed-safe", "abandoned")[text(previous["phase"])] {
		return nil, "", fmt.Errorf("unresolved transaction; inspect status and use explicit recovery")
	}
	n, err := nonce()
	if err != nil {
		return nil, "", err
	}
	id := u.Now().UTC().Format("20060102T150405Z-") + n[:12]
	target := filepath.Join(u.State, "transactions", id)
	if err := os.Mkdir(target, 0o700); err != nil {
		return nil, "", err
	}
	var old any
	active := filepath.Join(u.State, "active.json")
	if exists(active) {
		v, e := load(active)
		if e != nil {
			return nil, "", e
		}
		old = v["version"]
	} else if previous != nil {
		old = previous["active_version"]
		if old == nil {
			old = previous["previous_version"]
		}
	}
	t := Object{"schema_version": 1, "id": id, "version": version, "phase": "preparing", "checkpoint": filepath.Join(u.Backups, id), "checkpoint_protection": u.Config.Protection(), "verification_profile": u.Config.Profile(), "previous_version": old, "prior_pause": nil, "mutation_started": false, "restoring": false}
	err = u.write(t, "preparing")
	return t, target, err
}
func manifestObject(m contract.ManifestV1) Object {
	b, _ := contract.JSON(m)
	var out Object
	_ = decodeHost(b, maxJSON, &out)
	return out
}
func transactionManifest(t Object) (contract.ManifestV1, error) {
	b, err := contract.JSON(t["manifest"])
	if err != nil {
		return contract.ManifestV1{}, err
	}
	return contract.DecodeManifestV1(b, text(object(object(t["manifest"])["source"])["repository"]))
}

// Update authenticates and checkpoints before the irreversible startup boundary.
func (u *Updater) Update(ctx context.Context, version string) (result Object, err error) {
	if !versionRE.MatchString(version) {
		return nil, fmt.Errorf("version must be vMAJOR.MINOR.PATCH")
	}
	t, target, err := u.new(version)
	if err != nil {
		return nil, err
	}
	stopped := false
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("deployment operation failed; inspect protected state locally")
		}
		if err == nil || text(t["phase"]) == "completed" {
			return
		}
		recovery := context.WithoutCancel(ctx)
		if boolean(t["mutation_started"]) {
			_ = u.Host.Compose(recovery, filepath.Join(target, "private.compose.json"), "stop", "--timeout", "60")
			_ = u.write(t, "failed-closed")
			return
		}
		if stopped {
			recoverErr := u.Host.Compose(recovery, filepath.Join(target, "previous.compose.json"), "up", "-d", "--no-build", "--pull", "never")
			if recoverErr == nil {
				var old Object
				old, recoverErr = load(filepath.Join(target, "previous.compose.json"))
				if recoverErr == nil {
					recoverErr = u.Host.OriginalHealth(recovery, old, object(t["adopted"]))
				}
			}
			if recoverErr != nil {
				_ = u.Host.Compose(recovery, filepath.Join(target, "previous.compose.json"), "stop", "--timeout", "60")
				_ = u.write(t, "failed-closed")
				err = fmt.Errorf("old deployment recovery failed; inspect protected transaction, keep traffic closed")
				return
			}
		}
		if t["prior_pause"] != nil {
			old, e := load(filepath.Join(target, "previous.compose.json"))
			if e == nil {
				action := "resume"
				if boolean(t["prior_pause"]) {
					action = "pause"
				}
				_, e = u.Host.Incident(recovery, old, action)
			}
			if e != nil {
				_ = u.Host.Compose(recovery, filepath.Join(target, "previous.compose.json"), "stop", "--timeout", "60")
				_ = u.write(t, "failed-closed")
				return
			}
		}
		_ = u.write(t, "failed-safe")
	}()
	if _, err = u.Host.ProtectedInputs(); err != nil {
		return nil, err
	}
	m, err := u.Host.Download(ctx, version, target)
	if err != nil {
		return nil, err
	}
	t["manifest"] = manifestObject(m)
	current, err := u.Host.Current(ctx)
	if err != nil {
		return nil, err
	}
	candidate, err := u.Host.Candidate(ctx, target, m)
	if err != nil {
		return nil, err
	}
	private, err := Isolated(candidate)
	if err != nil {
		return nil, err
	}
	for name, v := range map[string]Object{"previous": current, "public": candidate, "private": private} {
		if err = atomicJSON(filepath.Join(target, name+".compose.json"), v); err != nil {
			return nil, err
		}
	}
	adopted, err := u.Host.Preflight(ctx, current, candidate, m, text(t["checkpoint"]))
	if err != nil {
		return nil, err
	}
	t["adopted"] = adopted
	for name, image := range object(adopted["images"]) {
		service := object(object(current["services"])[name])
		service["image"] = image
		delete(service, "build")
	}
	if err = atomicJSON(filepath.Join(target, "previous.compose.json"), current); err != nil {
		return nil, err
	}
	if err = u.Host.Pull(ctx, m, text(adopted["platform"])); err != nil {
		return nil, err
	}
	if err = u.Host.Ownership(ctx, m, adopted); err != nil {
		return nil, err
	}
	if err = u.Host.ValidateCaddy(ctx, filepath.Join(target, "private.compose.json")); err != nil {
		return nil, err
	}
	if adopted, err = u.Host.Preflight(ctx, current, candidate, m, text(t["checkpoint"])); err != nil {
		return nil, err
	}
	t["adopted"] = adopted
	paused, err := u.Host.Incident(ctx, current, "incident-status")
	if err != nil {
		return nil, err
	}
	t["prior_pause"] = paused
	if err = u.write(t, "pausing"); err != nil {
		return nil, err
	}
	if _, err = u.Host.Incident(ctx, current, "pause"); err != nil {
		return nil, err
	}
	if err = u.write(t, "stopping"); err != nil {
		return nil, err
	}
	stopped = true
	if err = u.Host.Stopped(ctx, filepath.Join(target, "previous.compose.json"), object(adopted["volumes"])); err != nil {
		return nil, err
	}
	if err = u.Host.FreePorts(candidate); err != nil {
		return nil, err
	}
	if err = u.write(t, "backing-up"); err != nil {
		return nil, err
	}
	if err = u.Host.Backup(ctx, current, adopted, text(t["checkpoint"]), t); err != nil {
		return nil, err
	}
	t["mutation_started"] = true
	if err = u.write(t, "migration-starting"); err != nil {
		return nil, err
	}
	if err = u.Host.Compose(ctx, filepath.Join(target, "private.compose.json"), "up", "-d", "--no-build", "--pull", "never"); err != nil {
		return nil, err
	}
	if err = u.write(t, "candidate-running"); err != nil {
		return nil, err
	}
	if err = u.Host.AutomaticChecks(ctx, private, t); err != nil {
		return nil, err
	}
	if err = u.write(t, "awaiting-verification"); err != nil {
		return nil, err
	}
	var report Object
	if profile(t) == "runtime-v1" {
		report = runtimeEvidence(t, u.Now())
	} else {
		report, err = u.Host.VerificationHook(ctx, filepath.Join(target, "private.compose.json"), filepath.Join(target, "transaction.json"))
		if err != nil {
			return nil, err
		}
	}
	if report != nil {
		if err = u.verifyEvidence(ctx, t, report); err != nil {
			return nil, err
		}
		if err = u.activate(ctx, t); err != nil {
			return nil, err
		}
	}
	return t, nil
}
func Isolated(compose Object) (Object, error) {
	v := clone(compose)
	if object(v["services"]) == nil || object(object(v["services"])["backend"]) == nil {
		return nil, fmt.Errorf("candidate services missing")
	}
	for _, item := range object(v["services"]) {
		s := object(item)
		if s == nil {
			return nil, fmt.Errorf("invalid candidate service")
		}
		s["restart"] = "no"
		for _, item := range array(s["ports"]) {
			p := object(item)
			if p["protocol"] != nil && p["protocol"] != "tcp" {
				return nil, fmt.Errorf("only reviewed TCP ingress is supported")
			}
			p["host_ip"] = "127.0.0.1"
		}
	}
	if len(array(object(object(v["services"])["backend"])["ports"])) != 0 {
		return nil, fmt.Errorf("backend may not have published ports")
	}
	return v, nil
}
func (u *Updater) verifyEvidence(ctx context.Context, t, report Object) error {
	if err := RequireFlowReport(report, boolean(t["restoring"]), profile(t), u.Now()); err != nil {
		return err
	}
	identity := object(t["verification_identity"])
	if identity == nil {
		identity = Object{"version": t["version"], "source_commit": object(object(t["manifest"])["source"])["commit"]}
	}
	if text(report["version"]) != text(identity["version"]) || text(report["source_commit"]) != text(identity["source_commit"]) {
		return fmt.Errorf("verification report describes another candidate")
	}
	if boolean(t["restoring"]) {
		private, err := load(filepath.Join(u.target(t), "private.compose.json"))
		if err != nil {
			return err
		}
		raw, err := u.Host.HTTPS(ctx, private, "/api/v1/config", "", false)
		if err != nil {
			return err
		}
		var observed Object
		if err = decodeHost(raw, maxJSON, &observed); err != nil {
			return err
		}
		baseline := object(object(t["adopted"])["baseline_config"])
		reconciled, err := reconciledSettings(baseline, observed)
		if err != nil {
			return err
		}
		if t["checkpoint_baseline_config"] == nil {
			t["checkpoint_baseline_config"] = baseline
		}
		object(t["adopted"])["baseline_config"] = reconciled
	}
	t["verification"] = report
	return u.write(t, "verified")
}
func (u *Updater) activate(ctx context.Context, t Object) (err error) {
	if text(t["phase"]) != "verified" {
		return fmt.Errorf("candidate is not verified")
	}
	if runtimeHost, ok := u.Host.(interface {
		RuntimeContext(context.Context, Object) (context.Context, context.CancelFunc)
	}); ok {
		var cancel context.CancelFunc
		ctx, cancel = runtimeHost.RuntimeContext(ctx, t)
		defer cancel()
	}
	target := u.target(t)
	public, err := load(filepath.Join(target, "public.compose.json"))
	if err != nil {
		return err
	}
	private, err := load(filepath.Join(target, "private.compose.json"))
	if err != nil {
		return err
	}
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("deployment operation failed; inspect protected state locally")
		}
		if err != nil {
			_ = u.Host.Compose(context.WithoutCancel(ctx), filepath.Join(target, "private.compose.json"), "stop", "--timeout", "60")
			_ = u.write(t, "failed-closed")
		}
	}()
	if err = u.Host.AutomaticChecks(ctx, private, t); err != nil {
		return err
	}
	if err = u.write(t, "activating"); err != nil {
		return err
	}
	proxies := []string{}
	for name := range object(public["services"]) {
		if name != "backend" {
			proxies = append(proxies, name)
		}
	}
	sort.Strings(proxies)
	args := append([]string{"up", "-d", "--no-deps", "--no-build", "--pull", "never"}, proxies...)
	if err = u.Host.Compose(ctx, filepath.Join(target, "public.compose.json"), args...); err != nil {
		return err
	}
	if err = u.Host.AutomaticChecks(ctx, public, t); err != nil {
		return err
	}
	action := "resume"
	if boolean(t["prior_pause"]) {
		action = "pause"
	}
	if _, err = u.Host.Incident(ctx, public, action); err != nil {
		return err
	}
	if err = u.Host.RestartPolicy(ctx); err != nil {
		return err
	}
	version := t["version"]
	if i := object(t["verification_identity"]); i != nil {
		version = i["version"]
	}
	t["active_version"] = version
	if err = atomicJSON(filepath.Join(u.State, "active.json"), Object{"transaction": t["id"], "version": version, "compose": public}); err != nil {
		return err
	}
	if err = u.write(t, "completed"); err != nil {
		return err
	}
	_ = u.Retention(context.WithoutCancel(ctx))
	latest, e := u.Read()
	if e == nil {
		for k, v := range latest {
			t[k] = v
		}
	}
	return nil
}
func (u *Updater) Restore(ctx context.Context) (result Object, err error) {
	t, err := u.Read()
	if err != nil {
		return nil, err
	}
	allowed := set("migration-starting", "candidate-running", "awaiting-verification", "verified", "activating", "failed-closed", "restore-starting", "restored-awaiting-verification", "completed")
	if t == nil || !allowed[text(t["phase"])] || boolean(t["restoring"]) {
		return nil, fmt.Errorf("no unattempted mutation checkpoint available for restore")
	}
	target := u.target(t)
	checkpoint := text(t["checkpoint"])
	if _, err = u.Host.VerifyBackup(checkpoint); err != nil {
		return nil, err
	}
	if err = u.Host.Compose(ctx, filepath.Join(target, "private.compose.json"), "stop", "--timeout", "60"); err != nil {
		return nil, err
	}
	t["restoring"] = true
	if err = u.write(t, "restore-starting"); err != nil {
		return nil, err
	}
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("deployment operation failed; inspect protected state locally")
		}
		if err != nil {
			_ = u.Host.Compose(context.WithoutCancel(ctx), filepath.Join(target, "private.compose.json"), "stop", "--timeout", "60")
			_ = u.write(t, "failed-closed")
		}
	}()
	restored, err := u.Host.Restore(ctx, checkpoint, target, t)
	if err != nil {
		return nil, err
	}
	private, err := Isolated(restored)
	if err != nil {
		return nil, err
	}
	if err = atomicJSON(filepath.Join(target, "public.compose.json"), restored); err != nil {
		return nil, err
	}
	if err = atomicJSON(filepath.Join(target, "private.compose.json"), private); err != nil {
		return nil, err
	}
	old := text(t["previous_version"])
	if old == "" {
		old = "source-installation"
	}
	t["verification_identity"] = Object{"version": old, "source_commit": "checkpoint:" + text(t["id"])}
	if err = u.write(t, "restore-starting"); err != nil {
		return nil, err
	}
	if err = u.Host.Compose(ctx, filepath.Join(target, "private.compose.json"), "up", "-d", "--no-build", "--pull", "never"); err != nil {
		return nil, err
	}
	if err = u.Host.AutomaticChecks(ctx, private, t); err != nil {
		return nil, err
	}
	if err = u.write(t, "restored-awaiting-verification"); err != nil {
		return nil, err
	}
	if u.Config.Profile() == "full" {
		report, e := u.Host.VerificationHook(ctx, filepath.Join(target, "private.compose.json"), filepath.Join(target, "transaction.json"))
		if e != nil {
			return nil, e
		}
		if report != nil {
			if err = u.verifyEvidence(ctx, t, report); err != nil {
				return nil, err
			}
			if err = u.activate(ctx, t); err != nil {
				return nil, err
			}
		}
	}
	return t, nil
}
func (u *Updater) RecoverSafe(ctx context.Context) (err error) {
	t, err := u.Read()
	if err != nil {
		return err
	}
	if t == nil || boolean(t["mutation_started"]) || boolean(t["restoring"]) {
		return fmt.Errorf("automatic old-binary recovery is forbidden after candidate startup")
	}
	if t["phase"] == "preparing" {
		return u.write(t, "abandoned")
	}
	target := u.target(t)
	previous, err := load(filepath.Join(target, "previous.compose.json"))
	if err != nil {
		return err
	}
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("deployment operation failed; inspect protected state locally")
		}
		if err != nil {
			_ = u.Host.Compose(context.WithoutCancel(ctx), filepath.Join(target, "previous.compose.json"), "stop", "--timeout", "60")
			_ = u.write(t, "failed-closed")
		}
	}()
	if _, err = u.Host.Incident(ctx, previous, "pause"); err != nil {
		return err
	}
	if err = u.Host.Compose(ctx, filepath.Join(target, "previous.compose.json"), "up", "-d", "--no-build", "--pull", "never"); err != nil {
		return err
	}
	if err = u.Host.OriginalHealth(ctx, previous, object(t["adopted"])); err != nil {
		return err
	}
	if t["prior_pause"] != nil {
		action := "resume"
		if boolean(t["prior_pause"]) {
			action = "pause"
		}
		if _, err = u.Host.Incident(ctx, previous, action); err != nil {
			return err
		}
	}
	return u.write(t, "failed-safe")
}

func (u *Updater) check(path string, directory, executable, private bool) error {
	if u.protect != nil {
		return u.protect(path, directory, executable, private)
	}
	return Protected(path, directory, executable, private)
}
