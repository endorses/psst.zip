package deployment

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// Run preserves the installed version/status SSH contract. Privileged commands
// use fixed protected paths; no state directory or executable may be supplied.
func Run(ctx context.Context, args []string, input *os.File, output io.Writer) (int, error) {
	if len(args) == 1 && args[0] == "ssh" {
		var err error
		args, err = SSHCommand(os.Getenv("SSH_ORIGINAL_COMMAND"))
		if err != nil {
			return 1, err
		}
	}
	if len(args) == 0 || !has([]string{"update", "status", "verify", "restore", "recover-safe", "retention", "cutover-check"}, args[0]) || len(args) > 2 {
		return 1, fmt.Errorf("usage: psst-deploy update VERSION | status | verify | restore | recover-safe | retention | cutover-check (local only)")
	}
	command := args[0]
	if command == "update" {
		if len(args) != 2 || !versionRE.MatchString(args[1]) {
			return 1, fmt.Errorf("update requires vMAJOR.MINOR.PATCH")
		}
	} else if len(args) != 1 {
		return 1, fmt.Errorf("this command accepts no release/path argument")
	}
	if os.Geteuid() != 0 {
		return 1, fmt.Errorf("run the installed root-owned deployment helper")
	}
	if err := setPrivateUmask(); err != nil {
		return 1, err
	}
	executable, err := os.Executable()
	if err != nil {
		return 1, err
	}
	if err = Protected(executable, false, true, false); err != nil {
		return 1, err
	}
	if err = Protected(ConfigPath, false, false, true); err != nil {
		return 1, err
	}
	data, err := boundedFile(ConfigPath, maxJSON)
	if err != nil {
		return 1, err
	}
	config, err := DecodeConfig(data)
	if err != nil {
		return 1, err
	}
	active := filepath.Join(StatePath, "active.json")
	if exists(active) {
		if err = Protected(active, false, false, true); err != nil {
			return 1, err
		}
		record, e := load(active)
		if e != nil {
			return 1, e
		}
		compose := object(record["compose"])
		config.VolumeNames = map[string]string{}
		for service := range object(compose["services"]) {
			bindings, e := Bindings(compose, service)
			if e != nil {
				return 1, e
			}
			for target, name := range bindings {
				config.VolumeNames[service+":"+target] = text(name)
			}
		}
		if err = config.Validate(); err != nil {
			return 1, err
		}
	}
	for _, path := range []string{StatePath, BackupPath, filepath.Join(StatePath, "transactions")} {
		if command == "cutover-check" {
			if err = existingPrivateDirectory(path); err != nil {
				return 1, err
			}
			continue
		}
		if err = privateDirectory(path); err != nil {
			return 1, err
		}
	}
	host := NewHost(config, StatePath, BackupPath)
	u := New(config, host, StatePath, BackupPath)
	if command == "cutover-check" {
		return 0, cutoverCheck(ctx, u, host, executable, output)
	}
	if command != "status" {
		unlock, e := lock(StatePath)
		if e != nil {
			return 1, e
		}
		defer unlock()
		switch command {
		case "update":
			_, err = u.Update(ctx, args[1])
		case "verify":
			err = u.localVerify(ctx, host, input, output)
		case "restore":
			_, err = u.Restore(ctx)
		case "recover-safe":
			err = u.RecoverSafe(ctx)
		case "retention":
			err = u.Retention(ctx)
		}
		if err != nil {
			return 1, err
		}
	}
	record, err := u.Read()
	if err != nil {
		return 1, err
	}
	return Display(record, output)
}
func Display(record Object, output io.Writer) (int, error) {
	if record == nil {
		_, err := fmt.Fprintln(output, `{"phase":"no-transaction"}`)
		return 0, err
	}
	summary := Object{}
	for _, key := range []string{"id", "version", "active_version", "previous_version", "phase", "cleanup", "checkpoint_protection", "verification_profile", "updated_at"} {
		summary[key] = record[key]
	}
	b, err := contract.JSON(summary)
	if err != nil {
		return 1, err
	}
	if _, err = output.Write(b); err != nil {
		return 1, err
	}
	if record["phase"] == "awaiting-verification" || record["phase"] == "restored-awaiting-verification" {
		return 20, nil
	}
	return 0, nil
}
func (u *Updater) localVerify(ctx context.Context, host *Host, input *os.File, output io.Writer) error {
	t, err := u.Read()
	if err != nil {
		return err
	}
	if t == nil || !has([]string{"awaiting-verification", "restored-awaiting-verification", "verified"}, text(t["phase"])) {
		return fmt.Errorf("no isolated candidate awaiting verification")
	}
	if profile(t) != "full" {
		return fmt.Errorf("runtime verification is automatic; full verification required for manual flows")
	}
	if !isTerminal(input) {
		return fmt.Errorf("local verification requires an interactive administrator terminal")
	}
	target := u.target(t)
	private, err := load(filepath.Join(target, "private.compose.json"))
	if err != nil {
		return err
	}
	if err = host.AutomaticChecks(ctx, private, t); err != nil {
		return err
	}
	if _, err = fmt.Fprint(output, "Current candidate administrator Cookie header (kept only in memory): "); err != nil {
		return err
	}
	cookie, err := readSecret(ctx, input)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(output); err != nil {
		return err
	}
	if err = host.AuthenticatedChecks(ctx, private, cookie); err != nil {
		return err
	}
	if _, err = fmt.Fprintln(output, "Verify the isolated candidate using a browser tunnel and retained client keys. Keep public routing closed. Evidence is protected, not logged."); err != nil {
		return err
	}
	checks := append([]string{}, flowChecks...)
	if boolean(t["restoring"]) {
		checks = append(checks, restoreChecks...)
	}
	sort.Strings(checks)
	identity := object(t["verification_identity"])
	if identity == nil {
		identity = Object{"version": t["version"], "source_commit": object(object(t["manifest"])["source"])["commit"]}
	}
	report := clone(identity)
	observations := Object{}
	for _, check := range checks {
		if _, err = fmt.Fprint(output, check+" — describe observed evidence (no secrets): "); err != nil {
			return err
		}
		line, e := readLine(ctx, input)
		if e != nil {
			return e
		}
		observations[check] = strings.TrimSpace(line)
	}
	report["checks"] = observations
	report["observed_at"] = u.Now().UTC().Format(time.RFC3339Nano)
	if err = host.AutomaticChecks(ctx, private, t); err != nil {
		return err
	}
	if err = host.AuthenticatedChecks(ctx, private, cookie); err != nil {
		return err
	}
	cookie = ""
	if err = u.verifyEvidence(ctx, t, report); err != nil {
		return err
	}
	return u.activate(ctx, t)
}
