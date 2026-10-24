package scanning

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

type SourceMeasurement struct {
	Schema        int               `json:"schema_version"`
	Kind          string            `json:"kind"`
	Source        NativeIdentity    `json:"source"`
	Execution     string            `json:"execution"`
	ArchiveSHA256 string            `json:"source_archive_sha256"`
	Inputs        map[string]string `json:"source_inputs"`
	Scans         []map[string]any  `json:"scans"`
	Completed     string            `json:"completed_at"`
	Authorized    bool              `json:"publication_authorized"`
	Pending       bool              `json:"source_scanners_gate_pending"`
	Review        bool              `json:"findings_review_required"`
}

// ReadSourceMeasurement is the assembler's structural source binding, not a
// scanner rerun or a finding policy gate. Later source aggregation must replay
// the referenced raw reports and derive every disposition independently.
func ReadSourceMeasurement(raw []byte, identity NativeIdentity, goImage, nodeImage string) (SourceMeasurement, error) {
	var result SourceMeasurement
	if err := identity.Validate(); err != nil {
		return result, err
	}
	if err := contract.Decode(raw, scannerRawLimit, &result); err != nil {
		return result, err
	}
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	if result.Source != identity || result.Kind != "native-source-scanner-measurement" || result.Schema != 1 && result.Schema != 2 || result.Schema == 1 && result.Execution != "native" || result.Schema == 2 && result.Execution != "native-go-common-lock" || fields["publication_authorized"] != false || fields["source_scanners_gate_pending"] != true || fields["findings_review_required"] != true || len(result.Scans) != 2 || !digestPattern.MatchString(result.ArchiveSHA256) || len(result.Inputs) == 0 || len(result.Inputs) > 50000 {
		return result, fmt.Errorf("source scanner source/execution binding differs")
	}
	if _, err := time.Parse(time.RFC3339Nano, result.Completed); err != nil {
		return result, fmt.Errorf("invalid source scanner completion time")
	}
	for name, digest := range result.Inputs {
		if !validImport(name) || !strings.HasPrefix(name, "backend/") && !strings.HasPrefix(name, "web/") || !digestPattern.MatchString(digest) {
			return result, fmt.Errorf("source scanner input inventory differs")
		}
	}
	for _, name := range []string{"backend/go.mod", "backend/go.sum", "web/package.json", "web/package-lock.json"} {
		if !digestPattern.MatchString(result.Inputs[name]) {
			return result, fmt.Errorf("source scanner exact dependency lock missing")
		}
	}
	seen := map[string]bool{}
	for _, scan := range result.Scans {
		target := text(scan["target"])
		reference := goImage
		if target == "web-source" {
			reference = nodeImage
		} else if target != "backend-source" {
			return result, fmt.Errorf("unexpected source scanner target")
		}
		if seen[target] || scan["subject"] != "git:"+identity.Repository+"@"+identity.Commit || scan["status"] != "complete" || scan["exit_code"] != float64(0) && !(target == "web-source" && scan["exit_code"] == float64(1)) {
			return result, fmt.Errorf("source scan incomplete or source differs")
		}
		seen[target] = true
		var builder Builder
		encoded, _ := json.Marshal(scan["builder"])
		if contract.Decode(encoded, 1<<20, &builder) != nil || builder.Reference != reference || !digestPattern.MatchString(builder.Config) || len(builder.Layers) == 0 {
			return result, fmt.Errorf("source scanner resolved builder differs")
		}
		expectedArch := strings.TrimPrefix(identity.Platform, "linux/")
		if result.Schema == 2 && target == "web-source" {
			common := object(scan["common_lock"])
			platform := text(common["platform"])
			if platform != "linux/amd64" && platform != "linux/arm64" || !digestPattern.MatchString(text(common["measurement_sha256"])) {
				return result, fmt.Errorf("common npm lock measurement missing")
			}
			expectedArch = strings.TrimPrefix(platform, "linux/")
		}
		if builder.Architecture != expectedArch || builder.Daemon != expectedArch && !(expectedArch == "amd64" && builder.Daemon == "x86_64") && !(expectedArch == "arm64" && builder.Daemon == "aarch64") {
			return result, fmt.Errorf("source scanner builder native platform differs")
		}
		for _, layer := range builder.Layers {
			if !digestPattern.MatchString(layer) {
				return result, fmt.Errorf("source scanner builder layer missing")
			}
		}
		inventory := object(scan["raw_files"])
		if len(inventory) == 0 || len(inventory) > 1024 {
			return result, fmt.Errorf("source scanner raw inventory missing")
		}
		for name, value := range inventory {
			if !rawFilename(name) || !digestPattern.MatchString(text(value)) {
				return result, fmt.Errorf("source scanner raw input identity invalid")
			}
		}
		if findings, ok := scan["findings"].([]any); !ok || findings == nil {
			return result, fmt.Errorf("source scanner complete findings missing")
		}
		for _, name := range []string{"started_at", "scanned_at"} {
			if _, err := scanTime(scan[name]); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}
func rawFilename(name string) bool {
	return name != "" && len(name) <= 200 && !strings.ContainsAny(name, "/\\:\x00\r\n") && name != "." && name != ".."
}
