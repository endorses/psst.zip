package evaluation

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/endorses/psst.zip/tools/release/internal/containerbuild"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
)

var recoveryChecks = []string{"historical-upgrade", "same-version-reapply", "pause-preservation", "post-migration-startup-failure", "matching-checkpoint-isolated-restore"}
var recoveryFixtureFiles = []string{"tools/measure_release_recovery.py", "tools/test_release_updater_integration.py", "tools/fixtures/release-updater/Dockerfile", "tools/fixtures/release-updater/controller.py", "tools/fixtures/release-updater/checkpoint.py", "tools/fixtures/release-updater/flows.py", "tools/fixtures/release-updater/verify.py"}

type recoveryTools struct {
	Docker       string `json:"docker"`
	Compose      string `json:"compose"`
	Architecture string `json:"architecture"`
}
type recoveryImage struct {
	Platform     string `json:"platform"`
	Manifest     string `json:"manifest_digest"`
	ManifestSize int64  `json:"manifest_size"`
	MediaType    string `json:"manifest_media_type"`
	Config       string `json:"config_digest"`
	Archive      string `json:"archive_digest"`
	BlobCount    int    `json:"blob_count"`
}
type recoveryMeasurement struct {
	SchemaVersion     int                           `json:"schema_version"`
	Kind              string                        `json:"kind"`
	Source            scanning.NativeIdentity       `json:"source"`
	Execution         string                        `json:"execution"`
	Manifest          string                        `json:"manifest_sha256"`
	Bundle            string                        `json:"bundle_sha256"`
	NativeDescriptors map[string]string             `json:"native_descriptors"`
	TestedConfigs     map[string]string             `json:"tested_configs"`
	SavedPair         containerbuild.NativeArtifact `json:"saved_pair"`
	Images            map[string]recoveryImage      `json:"images"`
	HistoricalCommit  string                        `json:"historical_source_commit"`
	ExecutionInputs   map[string]string             `json:"execution_inputs"`
	Tools             recoveryTools                 `json:"tools"`
	Experiments       map[string]json.RawMessage    `json:"experiments"`
	Started           string                        `json:"started_at"`
	Completed         string                        `json:"completed_at"`
	GatePending       bool                          `json:"upgrade_recovery_gate_pending"`
	Authentication    bool                          `json:"measurement_authentication_required"`
	PublicProvenance  bool                          `json:"public_provenance_verified"`
	OffHostProvider   bool                          `json:"off_host_provider_verified"`
	BrowserMobile     bool                          `json:"browser_mobile_flows_verified"`
	Publication       bool                          `json:"publication_authorized"`
}
type recoveryVersion struct {
	Version string            `json:"version"`
	Commit  string            `json:"commit"`
	Configs map[string]string `json:"configs"`
}
type recoveryObservation struct {
	Stage         string          `json:"stage"`
	Transaction   string          `json:"transaction"`
	Version       string          `json:"version"`
	Active        *string         `json:"active_version"`
	Previous      *string         `json:"previous_version"`
	Phase         string          `json:"phase"`
	Mutation      bool            `json:"mutation_started"`
	PriorPause    bool            `json:"prior_pause"`
	Restoring     bool            `json:"restoring"`
	Verification  json.RawMessage `json:"verification"`
	PublicIngress json.RawMessage `json:"public_ingress"`
}
type recoveryStartup struct {
	Transaction string `json:"transaction"`
	Restoring   bool   `json:"restoring"`
	Observed    string `json:"observed_at"`
}
type recoveryFlow struct {
	Checks   map[string]string `json:"checks"`
	Observed string            `json:"observed_at"`
	Version  string            `json:"version"`
	Commit   string            `json:"source_commit"`
}
type recoverySchema struct {
	Before      int  `json:"before"`
	After       int  `json:"original_after"`
	Restored    int  `json:"restored"`
	OldRejected bool `json:"old_cli_rejected_migrated_original"`
}
type recoveryReceipt struct {
	Checkpoint string `json:"checkpoint_sha256"`
	Encrypted  string `json:"encrypted_off_host_receipt"`
	Exercise   string `json:"restore_exercise"`
	Verified   string `json:"verified_at"`
}
type recoveryCheckpoint struct {
	SHA256  string `json:"sha256"`
	Records struct {
		Image         int `json:"image"`
		Volume        int `json:"volume"`
		Configuration int `json:"configuration"`
	} `json:"records"`
	CorruptRefused bool            `json:"corrupt_archive_refused"`
	Preserved      string          `json:"preserved_sha256"`
	Receipt        recoveryReceipt `json:"encrypted_export_receipt"`
}
type recoveryRestore struct {
	Mapping             map[string]string `json:"volume_mapping"`
	OriginalVolumes     map[string]string `json:"original_volumes"`
	Payload             string            `json:"original_payload_sha256"`
	PreservedPayload    string            `json:"preserved_original_payload_sha256"`
	Certificate         string            `json:"certificate_sha256"`
	RestoredCertificate string            `json:"restored_certificate_sha256"`
}
type recoveryExperiment struct {
	SchemaVersion    int                   `json:"schema_version"`
	Kind             string                `json:"kind"`
	Scenario         string                `json:"scenario"`
	Candidate        recoveryVersion       `json:"candidate"`
	Baseline         recoveryVersion       `json:"baseline"`
	PriorPause       bool                  `json:"prior_pause"`
	RepeatMode       string                `json:"repeat_mode"`
	Observations     []recoveryObservation `json:"observations"`
	Startups         []recoveryStartup     `json:"startup_observations"`
	Schema           recoverySchema        `json:"schema"`
	Checkpoint       recoveryCheckpoint    `json:"checkpoint"`
	Restore          recoveryRestore       `json:"restore"`
	Completed        string                `json:"completed_at"`
	Acquisition      string                `json:"acquisition"`
	BackupProvider   string                `json:"backup_provider"`
	Tools            recoveryTools         `json:"nested_tools"`
	PublicProvenance bool                  `json:"public_provenance_verified"`
	OffHostProvider  bool                  `json:"off_host_provider_verified"`
	Publication      bool                  `json:"publication_authorized"`
}

// decodeComplete additionally rejects absent required fields and null scalar
// values. A false scope flag must have actually been recorded, not defaulted by
// a missing JSON property. Raw observation objects are checked when consumed.
func decodeComplete(raw []byte, value any) error {
	if err := contract.Decode(raw, jsonLimit, value); err != nil {
		return err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var actual, expected any
	if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(canonical, &expected) != nil || !sameShape(actual, expected) {
		return fmt.Errorf("recovery evidence omits a required field or uses null for a scalar")
	}
	return nil
}
func sameShape(actual, expected any) bool {
	switch want := expected.(type) {
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for key, value := range want {
			if _, ok := got[key]; !ok || !sameShape(got[key], value) {
				return false
			}
		}
	case []any:
		got, ok := actual.([]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for i := range want {
			if !sameShape(got[i], want[i]) {
				return false
			}
		}
	default:
		return reflect.TypeOf(actual) == reflect.TypeOf(expected)
	}
	return true
}
