package scanning

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

const TrivyVersion = "0.75.0"
const TrivySource = "591e9799316a602e703f0b484f6c6d7b234ec8f3"
const trivyIdentity = "https://github.com/aquasecurity/trivy/.github/workflows/reusable-release.yaml@refs/tags/v" + TrivyVersion
const trivyDatabase = "ghcr.io/aquasecurity/trivy-db:2"

type trivyPin struct{ Archive, Bundle, Binary string }

var trivyPins = map[string]trivyPin{
	"linux/amd64": {"c6e65abddb348e25f10549df887045629cf28cc72453cd1c63acb717316b3f3f", "3565a49a9d857a977b18cb402323cb71ed806b735730c02a757c46133ddc12c0", "93f9da8e4ba5e0c1c76d8234ed2494cf9afb0a96fd21953e424bb795f3299b8e"},
	"linux/arm64": {"a1ee9f6ffb7d112b64ff726a2a0717c21175c1114361391f4a132956751a13b3", "5c09172152cc2d24837962e0dee2f2b0ff9dd4ffb2c2a93aff437e9c6f5f0052", ""},
}

// ImageScan is an authenticated native execution fact with all findings still
// unresolved. Aggregation below derives policy from exact compiler/advisory data.
type ImageScan struct {
	Target                                     string
	Image                                      ImageIdentity
	Scanner, Database                          map[string]any
	ScannedAt, MeasurementSHA256, ReportSHA256 string
	Findings                                   []map[string]any
}

func scanTime(value any) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, text(value))
	if err != nil {
		return t, fmt.Errorf("scanner timestamp missing or invalid")
	}
	return t, nil
}

// ReadImageMeasurement binds the actual raw Trivy result, scanner verification,
// database acquisition and complete finding list to the independently inspected
// final native image. Authentication of exact measurement bytes is caller-owned.
func ReadImageMeasurement(measurement, raw []byte, identity NativeIdentity, component string, image ImageIdentity) (ImageScan, error) {
	result := ImageScan{Target: component + "-" + strings.TrimPrefix(identity.Platform, "linux/"), Image: image, MeasurementSHA256: hash(measurement), ReportSHA256: hash(raw), Findings: []map[string]any{}}
	if err := identity.Validate(); err != nil {
		return result, err
	}
	if image.Platform != identity.Platform || !digestPattern.MatchString(image.Archive) || !digestPattern.MatchString(image.Config) || !digestPattern.MatchString(image.Manifest) {
		return result, fmt.Errorf("invalid expected native scan image")
	}
	var record map[string]any
	if err := contract.Decode(measurement, scannerRawLimit, &record); err != nil {
		return result, err
	}
	sourceRaw, _ := contract.JSON(identity)
	var expectedSource map[string]any
	json.Unmarshal(sourceRaw, &expectedSource)
	actualImage := object(record["image"])
	if record["schema_version"] != float64(1) || record["kind"] != "native-image-scanner-measurement" || !reflect.DeepEqual(record["source"], expectedSource) || record["execution"] != "native" || record["target"] != result.Target || record["publication_authorized"] != false || record["findings_review_required"] != true || record["final_image_scanners_gate_pending"] != true || record["status"] != "complete" || record["exit_code"] != float64(0) || record["raw_report_sha256"] != hash(raw) || actualImage["archive_digest"] != image.Archive || actualImage["manifest_digest"] != image.Manifest || actualImage["config_digest"] != image.Config || actualImage["platform"] != image.Platform || compilerTarget(component) == "" {
		return result, fmt.Errorf("native scanner measurement source/image/completion differs")
	}
	report, rows, err := CheckedImageReport(raw, identity.Platform, component, image.Config)
	if err != nil {
		return result, err
	}
	tool := object(record["scanner"])
	pin, ok := trivyPins[identity.Platform]
	if !ok || tool["name"] != "Trivy" || tool["version"] != TrivyVersion || tool["source_commit"] != TrivySource || tool["platform"] != identity.Platform || tool["archive_sha256"] != "sha256:"+pin.Archive || tool["bundle_sha256"] != "sha256:"+pin.Bundle || tool["certificate_identity"] != trivyIdentity || tool["oidc_issuer"] != "https://token.actions.githubusercontent.com" || tool["verification"] != "online-cosign-bundle-sct-rekor" || !digestPattern.MatchString(text(tool["sha256"])) || pin.Binary != "" && tool["sha256"] != "sha256:"+pin.Binary {
		return result, fmt.Errorf("scanner tool is not the authenticated pinned native release")
	}
	verifier := object(tool["cosign"])
	cosignVersion, cosignCommit, cosignDigest, err := source.CaddyVerifierPolicy(identity.Platform)
	if err != nil {
		return result, err
	}
	if !reflect.DeepEqual(verifier, map[string]any{"version": cosignVersion, "commit": cosignCommit, "sha256": cosignDigest}) {
		return result, fmt.Errorf("scanner signature verifier identity differs")
	}
	db := object(record["database"])
	if !exactFields(db, "sha256", "metadata_sha256", "acquisition", "Version", "UpdatedAt", "NextUpdate", "DownloadedAt") || db["Version"] != float64(2) || !digestPattern.MatchString(text(db["sha256"])) || !digestPattern.MatchString(text(db["metadata_sha256"])) {
		return result, fmt.Errorf("scanner database identity/schema is incomplete")
	}
	for _, name := range []string{"UpdatedAt", "NextUpdate", "DownloadedAt"} {
		if _, err = scanTime(db[name]); err != nil {
			return result, err
		}
	}
	acquired := object(db["acquisition"])
	if acquired["kind"] != "owned-authenticated-trivy-download" || acquired["repository"] != trivyDatabase || acquired["scanner_sha256"] != tool["sha256"] || acquired["database_sha256"] != db["sha256"] || acquired["metadata_sha256"] != db["metadata_sha256"] {
		return result, fmt.Errorf("official scanner database acquisition is missing")
	}
	for _, name := range []string{"started_at", "completed_at"} {
		if _, err = scanTime(acquired[name]); err != nil {
			return result, err
		}
	}
	for _, name := range []string{"started_at", "scanned_at", "completed_at"} {
		if _, err = scanTime(record[name]); err != nil {
			return result, err
		}
	}
	if record["scanned_at"] != report["CreatedAt"] {
		return result, fmt.Errorf("raw scanner timestamp differs")
	}
	wanted, _ := json.Marshal(rows)
	actual, _ := json.Marshal(record["findings"])
	if !bytesEqualJSON(wanted, actual) {
		return result, fmt.Errorf("scanner measurement omitted or changed raw findings")
	}
	for _, row := range rows {
		if row["class"] != "lang-pkgs" || row["target"] != compilerTarget(component) {
			return result, fmt.Errorf("OS/non-application/non-Go finding requires repair and complete rescan")
		}
	}
	result.Scanner, result.Database, result.ScannedAt, result.Findings = tool, db, text(record["scanned_at"]), rows
	return result, nil
}

func bytesEqualJSON(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

func CheckedImageReport(raw []byte, platform, component, config string) (map[string]any, []map[string]any, error) {
	var report map[string]any
	if len(raw) == 0 || contract.Decode(raw, 16<<20, &report) != nil || report["SchemaVersion"] != float64(2) || report["ArtifactType"] != "container_image" || object(report["Trivy"])["Version"] != TrivyVersion {
		return nil, nil, fmt.Errorf("unexpected scanner report identity")
	}
	meta := object(report["Metadata"])
	image := object(meta["ImageConfig"])
	if meta["ImageID"] != config || image["os"] != "linux" || image["architecture"] != strings.TrimPrefix(platform, "linux/") || object(meta["OS"])["Family"] != "alpine" {
		return nil, nil, fmt.Errorf("scanner report did not inspect exact native image")
	}
	if _, err := scanTime(report["CreatedAt"]); err != nil {
		return nil, nil, err
	}
	results, ok := report["Results"].([]any)
	if !ok || len(results) == 0 || len(results) > 128 {
		return nil, nil, fmt.Errorf("scanner package inventory missing")
	}
	seenOS, seenBinary := false, false
	rows := []map[string]any{}
	for _, value := range results {
		entry := object(value)
		packages, ok := entry["Packages"].([]any)
		if !slices.Contains([]string{"os-pkgs", "lang-pkgs"}, text(entry["Class"])) || text(entry["Target"]) == "" || !ok || len(packages) == 0 {
			return nil, nil, fmt.Errorf("incomplete scanner package result")
		}
		if entry["Class"] == "os-pkgs" {
			if entry["Type"] != "alpine" {
				return nil, nil, fmt.Errorf("unexpected OS scanner")
			}
			seenOS = true
		}
		if entry["Class"] == "lang-pkgs" && entry["Type"] == "gobinary" && entry["Target"] == compilerTarget(component) {
			seenBinary = true
		}
		vulns, ok := entry["Vulnerabilities"].([]any)
		if entry["Vulnerabilities"] != nil && !ok {
			return nil, nil, fmt.Errorf("malformed raw scanner findings")
		}
		if suppressed, exists := entry["ExperimentalModifiedFindings"]; exists && suppressed != nil {
			if items, ok := suppressed.([]any); !ok || len(items) > 0 {
				return nil, nil, fmt.Errorf("scanner unexpectedly suppressed findings")
			}
		}
		for _, value := range vulns {
			finding := object(value)
			if !slices.Contains([]string{"UNKNOWN", "LOW", "MEDIUM", "HIGH", "CRITICAL"}, text(finding["Severity"])) || text(finding["VulnerabilityID"]) == "" || text(finding["PkgName"]) == "" || text(finding["InstalledVersion"]) == "" || text(finding["Status"]) == "" {
				return nil, nil, fmt.Errorf("incomplete raw scanner finding")
			}
			if entry["Class"] == "lang-pkgs" && entry["Target"] == compilerTarget(component) && entry["Type"] != "gobinary" {
				return nil, nil, fmt.Errorf("application scanner analyzer differs")
			}
			rows = append(rows, map[string]any{"target": entry["Target"], "class": entry["Class"], "finding_sha256": hash(legacyJSON(finding)), "finding": finding})
		}
	}
	if !seenOS || !seenBinary {
		return nil, nil, fmt.Errorf("scanner omitted OS or executable package inventory")
	}
	return report, rows, nil
}
