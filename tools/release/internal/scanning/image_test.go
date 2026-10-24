package scanning

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

func imageScannerFixture(t *testing.T, component, arch string, findings []any) (NativeIdentity, ImageIdentity, map[string]any, map[string]any) {
	t.Helper()
	platform := "linux/" + arch
	digest := "sha256:" + strings.Repeat("a", 64)
	stamp := "2026-10-24T00:00:00Z"
	identity := NativeIdentity{Source{"endorses/psst.zip", "v1.2.3", strings.Repeat("b", 40)}, platform}
	image := ImageIdentity{digest, digest, digest, platform}
	report := map[string]any{"SchemaVersion": 2, "ArtifactType": "container_image", "Trivy": map[string]any{"Version": TrivyVersion}, "Metadata": map[string]any{"ImageID": digest, "ImageConfig": map[string]any{"os": "linux", "architecture": arch}, "OS": map[string]any{"Family": "alpine"}}, "CreatedAt": stamp, "Results": []any{map[string]any{"Class": "os-pkgs", "Type": "alpine", "Target": "Alpine Linux", "Packages": []any{map[string]any{"Name": "musl"}}}, map[string]any{"Class": "lang-pkgs", "Type": "gobinary", "Target": compilerTarget(component), "Packages": []any{map[string]any{"Name": "stdlib"}}, "Vulnerabilities": findings}}}
	raw, _ := contract.JSON(report)
	_, rows, err := CheckedImageReport(raw, platform, component, digest)
	if err != nil {
		t.Fatal(err)
	}
	pin := trivyPins[platform]
	binary := pin.Binary
	if binary == "" {
		binary = strings.Repeat("c", 64)
	}
	cosignVersion, cosignCommit, cosignDigest, _ := source.CaddyVerifierPolicy(platform)
	tool := map[string]any{"name": "Trivy", "version": TrivyVersion, "source_commit": TrivySource, "platform": platform, "archive_sha256": "sha256:" + pin.Archive, "bundle_sha256": "sha256:" + pin.Bundle, "sha256": "sha256:" + binary, "certificate_identity": trivyIdentity, "oidc_issuer": "https://token.actions.githubusercontent.com", "verification": "online-cosign-bundle-sct-rekor", "cosign": map[string]any{"version": cosignVersion, "commit": cosignCommit, "sha256": cosignDigest}}
	database := map[string]any{"sha256": digest, "metadata_sha256": digest, "Version": 2, "UpdatedAt": stamp, "NextUpdate": stamp, "DownloadedAt": stamp, "acquisition": map[string]any{"kind": "owned-authenticated-trivy-download", "repository": trivyDatabase, "scanner_sha256": tool["sha256"], "database_sha256": digest, "metadata_sha256": digest, "started_at": stamp, "completed_at": stamp}}
	record := map[string]any{"schema_version": 1, "kind": "native-image-scanner-measurement", "source": identity, "execution": "native", "target": component + "-" + arch, "image": image, "scanner": tool, "database": database, "started_at": stamp, "scanned_at": stamp, "completed_at": stamp, "status": "complete", "exit_code": 0, "raw_report_sha256": hash(raw), "findings": rows, "publication_authorized": false, "findings_review_required": true, "final_image_scanners_gate_pending": true}
	return identity, image, record, report
}
func imageRecordJSON(t *testing.T, record, report map[string]any) ([]byte, []byte) {
	t.Helper()
	raw, _ := contract.JSON(report)
	record["raw_report_sha256"] = hash(raw)
	body, _ := contract.JSON(record)
	return body, raw
}
func testFinding() map[string]any {
	return map[string]any{"VulnerabilityID": "CVE-2026-1234", "VendorIDs": []any{"GO-2026-1234"}, "PkgName": "stdlib", "InstalledVersion": "v1.27.3", "FixedVersion": "1.27.4", "Status": "affected", "Severity": "HIGH", "DataSource": map[string]any{"ID": "govulndb"}}
}

func TestImageMeasurementBindsToolDatabaseImageAndCompleteFindings(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"valid", func(_, _ map[string]any) {}},
		{"changed configuration", func(_, r map[string]any) {
			r["Metadata"].(map[string]any)["ImageID"] = "sha256:" + strings.Repeat("f", 64)
		}},
		{"omitted finding", func(m, _ map[string]any) { m["findings"] = []any{} }},
		{"retained unapproved database", func(m, _ map[string]any) {
			m["database"].(map[string]any)["acquisition"] = map[string]any{"kind": "retained-unapproved-snapshot"}
		}},
		{"wrong signer", func(m, _ map[string]any) {
			m["scanner"].(map[string]any)["certificate_identity"] = "https://example.invalid/signer"
		}},
		{"missing execution flag", func(m, _ map[string]any) { delete(m, "publication_authorized") }},
		{"suppressed raw finding", func(_, r map[string]any) {
			r["Results"].([]any)[1].(map[string]any)["ExperimentalModifiedFindings"] = []any{map[string]any{"Status": "ignored"}}
		}},
		{"missing OS inventory", func(_, r map[string]any) { r["Results"] = r["Results"].([]any)[1:] }},
	} {
		t.Run(change.name, func(t *testing.T) {
			identity, image, m, r := imageScannerFixture(t, "backend", "amd64", []any{testFinding()})
			change.mutate(m, r)
			body, raw := imageRecordJSON(t, m, r)
			_, err := ReadImageMeasurement(body, raw, identity, "backend", image)
			if (err == nil) != (change.name == "valid") {
				t.Fatalf("unexpected measurement decision: %v", err)
			}
		})
	}
}
func TestAggregateRequiresBothPairsAndAuthenticatesEachExactFact(t *testing.T) {
	o := ImageAggregationOptions{Root: t.TempDir(), Binding: github.Binding{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("b", 40), RunID: 1, Attempt: 2}, Images: map[string]ImageEvidence{}, Subjects: map[string]string{}}
	calls := 0
	o.Authenticate = func(_ context.Context, raw []byte) error {
		calls++
		var v map[string]any
		if err := contract.Decode(raw, scannerRawLimit, &v); err != nil {
			return err
		}
		return nil
	}
	for _, component := range []string{"backend", "web"} {
		for _, arch := range []string{"amd64", "arm64"} {
			identity, image, m, r := imageScannerFixture(t, component, arch, []any{})
			body, raw := imageRecordJSON(t, m, r)
			target := component + "-" + arch
			o.Images[target] = ImageEvidence{Measurement: body, Report: raw, Expected: CompilerExpected{Source: identity, Component: component, Image: image}}
			o.Subjects[target] = "oci://ghcr.io/endorses/psst-zip-" + component + "@" + image.Manifest
		}
	}
	details, err := AggregateImages(context.Background(), o)
	if err != nil || len(details["scans"].([]any)) != 4 || calls != 4 {
		t.Fatalf("valid aggregate: %v calls=%d", err, calls)
	}
	delete(o.Images, "web-arm64")
	if _, err := AggregateImages(context.Background(), o); err == nil {
		t.Fatal("accepted missing native pair")
	}
}

func advisoryFixture(t *testing.T, affected string) (map[string]any, CompilerMeasurement, string) {
	t.Helper()
	id := "GO-2026-1234"
	raw, _ := contract.JSON(map[string]any{"id": id, "aliases": []string{"CVE-2026-1234"}, "modified": "2026-10-24T00:00:00Z", "affected": []any{map[string]any{"package": map[string]any{"ecosystem": "Go", "name": "stdlib"}, "ecosystem_specific": map[string]any{"imports": []any{map[string]any{"path": affected}}}}}})
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, id+".json"), raw, 0600)
	entry := Advisory{id + ".json", hash(raw), "https://vuln.go.dev/ID/" + id + ".json"}
	compiler := CompilerMeasurement{RawFiles: map[string]string{id + ".json": hash(raw)}, Advisories: map[string]Advisory{id: entry}, Graph: CompilerGraph{SHA256: "sha256:" + strings.Repeat("d", 64)}, Binary: CompilerBinary{SHA256: "sha256:" + strings.Repeat("e", 64)}, Inputs: map[string]string{}}
	finding := testFinding()
	row := map[string]any{"target": "app/server", "class": "lang-pkgs", "finding": finding, "finding_sha256": hash(legacyJSON(finding))}
	return row, compiler, root
}
func TestPackageAbsenceUsesEveryOfficialPathAndFailsUncertainEvidence(t *testing.T) {
	row, compiler, root := advisoryFixture(t, "archive/zip")
	paths := []string{"net/http"}
	modules := map[string]string{"stdlib": "1.27.3"}
	result, err := deriveImageFinding(row, compiler, root, paths, modules)
	if err != nil || result["disposition"] != "not-applicable" {
		t.Fatalf("complete absent package: %v", err)
	}
	result, err = deriveImageFinding(row, compiler, root, []string{"archive/zip"}, modules)
	if err != nil || result["disposition"] != "affected" {
		t.Fatalf("affected package suppressed: %v", err)
	}
	row["finding"].(map[string]any)["VendorIDs"] = []any{"GO-2026-1234", "GO-2026-9999"}
	if _, err := deriveImageFinding(row, compiler, root, paths, modules); err == nil {
		t.Fatal("accepted ambiguous official advisory")
	}
	row, compiler, root = advisoryFixture(t, "../uncertain")
	if _, err := deriveImageFinding(row, compiler, root, paths, modules); err == nil {
		t.Fatal("accepted unsafe affected package")
	}
	row, compiler, root = advisoryFixture(t, "archive/zip")
	os.WriteFile(filepath.Join(root, "GO-2026-1234.json"), []byte("substituted"), 0600)
	if _, err := deriveImageFinding(row, compiler, root, paths, modules); err == nil {
		t.Fatal("accepted substituted official advisory")
	}
}
func TestAdvisoryCollectionIsOnlyEvidenceReferences(t *testing.T) {
	ids, err := AdvisoryIDs([]byte(`{"findings":[{"id":"GO-2026-1234"},{"alias":"CVE-2026-1234; GO-2026-1234"},"GO-2026-7777","NOT-GO-2026-9999"]}`))
	if err != nil || !reflect.DeepEqual(ids, []string{"GO-2026-1234", "GO-2026-7777"}) {
		t.Fatalf("advisory references: %v %v", ids, err)
	}
}

func TestTemporaryCompilerAcceptanceCannotExpandBinaryOrLifetime(t *testing.T) {
	root, commit, node, fact := policyFixture(t)
	_ = root
	native := object(object(node["platforms"])["linux/amd64"])
	row := object(acceptedFixture(node, fact)["scans"].([]any)[2].(map[string]any)["findings"].([]any)[0])
	row["disposition"] = "affected"
	var compiler CompilerMeasurement
	compiler.Component = "web"
	compiler.Binary.SHA256 = text(native["binary_sha256"])
	raw := []byte(`{"GoVersion":"go1.27.2"}`)
	json.Unmarshal(raw, &compiler.Binary.Build)
	compiler.Inputs = map[string]string{"wrapper_revision": text(node["source_revision"])}
	expected := CompilerExpected{Source: NativeIdentity{Source{"endorses/psst.zip", "v1.2.3", commit}, "linux/amd64"}, Caddy: source.Source{Version: text(node["caddy_version"]), Authentication: map[string]string{"source_commit": text(node["source_revision"]), "base_index": text(node["base_image"])}}}
	valid := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if _, err := acceptImageFinding(row, compiler, expected, node, fact, valid); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptImageFinding(row, compiler, expected, node, fact, valid.Add(20*24*time.Hour)); err == nil {
		t.Fatal("accepted expired compiler exception")
	}
	compiler.Binary.SHA256 = "sha256:" + strings.Repeat("f", 64)
	if _, err := acceptImageFinding(row, compiler, expected, node, fact, valid); err == nil {
		t.Fatal("expanded Caddy accepted binary")
	}
}
