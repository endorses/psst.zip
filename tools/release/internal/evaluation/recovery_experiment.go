package evaluation

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/containerbuild"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/deployment"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
)

var recoveryDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var recoveryHex = regexp.MustCompile(`^[a-f0-9]{64}$`)
var recoveryTransaction = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[a-f0-9]{12}$`)
var recoveryToolVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
var recoveryVolume = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func toolsValid(tools recoveryTools, platform string) bool {
	native := platform == "linux/amd64" && (tools.Architecture == "x86_64" || tools.Architecture == "amd64") || platform == "linux/arm64" && (tools.Architecture == "aarch64" || tools.Architecture == "arm64")
	return native && recoveryToolVersion.MatchString(tools.Docker) && recoveryToolVersion.MatchString(tools.Compose)
}
func pairValid(configs map[string]string) bool {
	return len(configs) == 2 && recoveryDigest.MatchString(configs["backend"]) && recoveryDigest.MatchString(configs["web"])
}
func instant(value string) (time.Time, error) {
	if len(value) == 0 || len(value) > 64 {
		return time.Time{}, fmt.Errorf("invalid recovery timestamp")
	}
	stamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid recovery timestamp")
	}
	return stamp, nil
}
func inWindow(value string, lower, upper time.Time) bool {
	stamp, err := instant(value)
	return err == nil && !stamp.Before(lower) && !stamp.After(upper)
}

func checkRecoveryExperiment(raw []byte, identity scanning.NativeIdentity, previous string, configs map[string]string, failure, paused bool, lower, upper time.Time) error {
	var e recoveryExperiment
	if err := decodeComplete(raw, &e); err != nil {
		return err
	}
	scenario := "normal"
	if failure {
		scenario = "post-startup-failure"
	}
	if e.SchemaVersion != 1 || e.Kind != "disposable-updater-experiment" || e.Scenario != scenario || e.PriorPause != paused || e.RepeatMode != "same-exact-candidate" || e.Acquisition != "fixture-local-exact-loaded-configs" || e.BackupProvider != "same-host-separated-store-encryption-simulation" || e.PublicProvenance || e.OffHostProvider || e.Publication {
		return fmt.Errorf("recovery experiment scope or acquisition policy differs")
	}
	if e.Candidate.Version != identity.Version || e.Candidate.Commit != identity.Commit || !reflect.DeepEqual(e.Candidate.Configs, configs) || e.Baseline.Version != "v0.0.0" || e.Baseline.Commit != previous || !pairValid(e.Baseline.Configs) || !toolsValid(e.Tools, identity.Platform) {
		return fmt.Errorf("recovery candidate, historical baseline or native tools differ")
	}
	end, err := instant(e.Completed)
	if err != nil || end.Before(lower) || end.After(upper) {
		return fmt.Errorf("recovery experiment completion is outside terminal measurement")
	}
	stages := []string{"candidate-activation", "repeat-activation", "isolated-restore-activation"}
	if failure {
		stages = []string{"post-startup-failure", "isolated-restore-activation"}
	}
	if len(e.Observations) != len(stages) {
		return fmt.Errorf("recovery experiment lacks upgrade, repeat, fault or restore observation")
	}
	identifiers := map[string]bool{}
	for i, observation := range e.Observations {
		restoring := stages[i] == "isolated-restore-activation"
		failed := stages[i] == "post-startup-failure"
		if observation.Stage != stages[i] || !recoveryTransaction.MatchString(observation.Transaction) || observation.Version != identity.Version || !observation.Mutation || observation.PriorPause != paused || observation.Restoring != restoring {
			return fmt.Errorf("durable recovery transaction facts differ")
		}
		if restoring {
			if observation.Transaction != e.Observations[i-1].Transaction {
				return fmt.Errorf("restore does not use the matching mutation checkpoint")
			}
		} else {
			if identifiers[observation.Transaction] {
				return fmt.Errorf("recovery reused an upgrade transaction identity")
			}
			identifiers[observation.Transaction] = true
		}
		if failed {
			var ingress struct {
				Running int `json:"running_services"`
			}
			if observation.Phase != "failed-closed" || observation.Active != nil || !bytes.Equal(bytes.TrimSpace(observation.Verification), []byte("null")) || decodeComplete(observation.PublicIngress, &ingress) != nil || ingress.Running != 0 {
				return fmt.Errorf("post-migration failure did not close public deployment")
			}
			continue
		}
		expectedVersion := identity.Version
		if restoring && failure {
			expectedVersion = "v0.0.0"
		}
		var ingress struct {
			Port   int  `json:"https_port"`
			Status int  `json:"config_status"`
			Paused bool `json:"public_transfers_paused"`
		}
		if observation.Phase != "completed" || observation.Active == nil || *observation.Active != expectedVersion || decodeComplete(observation.PublicIngress, &ingress) != nil || ingress.Port != 18443 || ingress.Status != 200 || ingress.Paused != paused {
			return fmt.Errorf("recovery candidate or restored ingress did not preserve prior pause")
		}
		var flow recoveryFlow
		if err := decodeComplete(observation.Verification, &flow); err != nil {
			return err
		}
		expectedCommit := identity.Commit
		if restoring {
			expectedCommit = "checkpoint:" + observation.Transaction
		}
		if flow.Version != expectedVersion || flow.Commit != expectedCommit || !inWindow(flow.Observed, lower, end) {
			return fmt.Errorf("authenticated recovery flows describe another source or time")
		}
		names := deployment.FullFlowCheckNames(restoring)
		if len(flow.Checks) != len(names) {
			return fmt.Errorf("recovery authenticated flow check set is incomplete")
		}
		for _, name := range names {
			details, ok := flow.Checks[name]
			var object map[string]any
			if !ok || len(details) < 12 || len(details) > 1000 || contract.Decode([]byte(details), 1000, &object) != nil || object == nil {
				return fmt.Errorf("recovery flow lacks directly asserted structured observations")
			}
		}
	}
	seenStartups := map[string]bool{}
	if len(e.Startups) == 0 {
		return fmt.Errorf("recovery lacks actual startup checks")
	}
	for _, startup := range e.Startups {
		if !identifiers[startup.Transaction] || !inWindow(startup.Observed, lower, end) {
			return fmt.Errorf("recovery startup describes another transaction or time")
		}
		seenStartups[startup.Transaction] = true
	}
	if len(seenStartups) != len(identifiers) {
		return fmt.Errorf("recovery startup coverage is incomplete")
	}
	if e.Schema.Before <= 0 || e.Schema.After <= e.Schema.Before || !e.Schema.OldRejected || e.Schema.Restored != e.Schema.After && !failure || failure && e.Schema.Restored != e.Schema.Before {
		return fmt.Errorf("historical migration, old CLI refusal or restored schema was not exercised")
	}
	checkpoint := e.Checkpoint
	if !recoveryHex.MatchString(checkpoint.SHA256) || checkpoint.SHA256 != checkpoint.Preserved || !checkpoint.CorruptRefused || checkpoint.Records.Image != 2 || checkpoint.Records.Volume != 3 || checkpoint.Records.Configuration < 3 || checkpoint.Receipt.Checkpoint != checkpoint.SHA256 || len(checkpoint.Receipt.Encrypted) < 64 || len(checkpoint.Receipt.Exercise) < 64 || !inWindow(checkpoint.Receipt.Verified, lower, end) {
		return fmt.Errorf("stopped checkpoint preservation or encrypted export evidence is incomplete")
	}
	r := e.Restore
	if len(r.OriginalVolumes) != 3 || len(r.Mapping) != 3 {
		return fmt.Errorf("isolated restore storage set is incomplete")
	}
	originals, restored := map[string]bool{}, map[string]bool{}
	for _, key := range []string{"backend:/app/data", "caddy:/data", "caddy:/config"} {
		name, ok := r.OriginalVolumes[key]
		if !ok || !recoveryVolume.MatchString(name) || originals[name] {
			return fmt.Errorf("original recovery volume identity is invalid")
		}
		originals[name] = true
	}
	for original, target := range r.Mapping {
		if !originals[original] || originals[target] || restored[target] || !recoveryVolume.MatchString(target) {
			return fmt.Errorf("restore overwrote or reused original storage")
		}
		restored[target] = true
	}
	if !recoveryHex.MatchString(r.Payload) || !recoveryHex.MatchString(r.Certificate) || r.Payload != r.PreservedPayload || r.Certificate != r.RestoredCertificate {
		return fmt.Errorf("restore changed original payload or TLS state")
	}
	return nil
}

func recoveryExperimentProfiles() []struct {
	name            string
	failure, paused bool
} {
	return []struct {
		name            string
		failure, paused bool
	}{{"upgrade-repeat-restore", false, false}, {"paused-upgrade-repeat-restore", false, true}, {"post-migration-startup-fault-restore", true, false}}
}

func safeRecoveryArtifact(artifact containerbuild.NativeArtifact) bool {
	if artifact.Size <= 0 || artifact.Size > 2<<30 || !recoveryDigest.MatchString(artifact.SHA256) || artifact.File == "" || strings.ContainsAny(artifact.File, "\\\r\n") || strings.HasPrefix(artifact.File, "/") || strings.HasSuffix(artifact.File, "/") {
		return false
	}
	for _, part := range strings.Split(artifact.File, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
