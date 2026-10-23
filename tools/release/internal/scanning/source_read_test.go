package scanning

import (
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func TestSourceStructuralBindingPreservesSharedNPMPlatformWithoutClaimingPolicy(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	identity := NativeIdentity{Source{"endorses/psst.zip", "v1.2.3", strings.Repeat("b", 40)}, "linux/amd64"}
	goImage := "docker.io/library/golang@" + digest
	nodeImage := "docker.io/library/node@" + digest
	scans := []map[string]any{}
	for _, target := range []string{"backend-source", "web-source"} {
		arch, image := "amd64", goImage
		if target == "web-source" {
			arch, image = "arm64", nodeImage
		}
		scan := map[string]any{"target": target, "subject": "git:endorses/psst.zip@" + identity.Commit, "status": "complete", "exit_code": 0, "builder": Builder{Reference: image, Config: digest, Architecture: arch, Layers: []string{digest}, Daemon: arch}, "raw_files": map[string]string{"report.json": digest}, "findings": []any{}, "started_at": "2026-10-24T00:00:00Z", "scanned_at": "2026-10-24T00:00:00Z"}
		if target == "web-source" {
			scan["common_lock"] = map[string]any{"platform": "linux/arm64", "measurement_sha256": digest}
		}
		scans = append(scans, scan)
	}
	measurement := SourceMeasurement{Schema: 2, Kind: "native-source-scanner-measurement", Source: identity, Execution: "native-go-common-lock", ArchiveSHA256: digest, Inputs: map[string]string{"backend/go.mod": digest, "backend/go.sum": digest, "web/package.json": digest, "web/package-lock.json": digest}, Scans: scans, Completed: "2026-10-24T00:00:00Z", Pending: true, Review: true}
	raw, _ := contract.JSON(measurement)
	if _, err := ReadSourceMeasurement(raw, identity, goImage, nodeImage); err != nil {
		t.Fatal(err)
	}
	measurement.Source.Commit = strings.Repeat("f", 40)
	raw, _ = contract.JSON(measurement)
	if _, err := ReadSourceMeasurement(raw, identity, goImage, nodeImage); err == nil {
		t.Fatal("accepted different source")
	}
	measurement.Source = identity
	measurement.Scans[0]["raw_files"] = map[string]string{"../secret": digest}
	raw, _ = contract.JSON(measurement)
	if _, err := ReadSourceMeasurement(raw, identity, goImage, nodeImage); err == nil {
		t.Fatal("accepted unbounded/unsafe raw evidence reference")
	}
}
