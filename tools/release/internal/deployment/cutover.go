package deployment

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const legacyUpdater = "/usr/local/lib/psst.zip/deploy/update.py"
const legacyParser = "/usr/local/lib/psst.zip/tools/release_artifacts.py"

func existingPrivateDirectory(path string) error {
	if err := Protected(path, true, false, false); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("cutover requires existing private deployment directories")
	}
	return nil
}

// resolvedCutoverState admits only the updater's terminal v1 states. In-flight,
// failed-closed and pending-verification transactions need explicit recovery.
func resolvedCutoverState(record Object) error {
	if record == nil {
		return nil
	}
	if !has([]string{"completed", "failed-safe", "abandoned"}, text(record["phase"])) {
		return fmt.Errorf("unresolved deployment transaction; cutover refused")
	}
	if text(record["phase"]) == "failed-safe" && boolean(record["mutation_started"]) {
		return fmt.Errorf("failed-safe transaction records mutation; cutover refused")
	}
	return nil
}

// cutoverCheck is local and read-only. The root installer holds deployment.lock
// across this check and the entrypoint switch; taking it here would deadlock.
func cutoverCheck(ctx context.Context, u *Updater, host *Host, executable string, output io.Writer) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return fmt.Errorf("cutover requires supported native Linux")
	}
	transaction := filepath.Join(u.State, "transaction.json")
	if exists(transaction) {
		if err := Protected(transaction, false, false, true); err != nil {
			return err
		}
	}
	record, err := u.Read()
	if err != nil {
		return err
	}
	if err := resolvedCutoverState(record); err != nil {
		return err
	}
	for _, path := range []string{legacyUpdater, legacyParser} {
		if err := Protected(path, false, path == legacyUpdater, false); err != nil {
			return err
		}
	}
	legacyRaw, err := host.run(ctx, nil, "", false, "python3", "-I", "-B", legacyUpdater, "status")
	if err != nil {
		return fmt.Errorf("retained Python status check failed")
	}
	var legacy Object
	if err := contract.Decode(legacyRaw, maxJSON, &legacy); err != nil {
		return fmt.Errorf("retained Python status is invalid")
	}
	var currentRaw bytes.Buffer
	if _, err := Display(record, &currentRaw); err != nil {
		return err
	}
	var current Object
	if err := contract.Decode(currentRaw.Bytes(), maxJSON, &current); err != nil || !equal(current, legacy) {
		return fmt.Errorf("retained Python/Go v1 status differs")
	}
	digest, err := fingerprint(executable)
	if err != nil {
		return err
	}
	revision := ""
	modified := true
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision = setting.Value
			}
			if setting.Key == "vcs.modified" {
				modified = setting.Value != "false"
			}
		}
	}
	phase := "no-transaction"
	if record != nil {
		phase = text(record["phase"])
	}
	facts := Object{"schema_version": 1, "kind": "updater-cutover-check", "helper_sha256": digest, "platform": runtime.GOOS + "/" + runtime.GOARCH, "go_version": runtime.Version(), "source_commit": revision, "source_modified": modified, "legacy_v1_status_matches": true, "transaction_resolved": true, "phase": phase}
	raw, err := contract.JSON(facts)
	if err != nil {
		return err
	}
	_, err = output.Write(raw)
	return err
}
