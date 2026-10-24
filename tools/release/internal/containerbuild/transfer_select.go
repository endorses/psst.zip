package containerbuild

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

type transferObject map[string]json.RawMessage

func transferObjectOf(raw []byte) (transferObject, error) {
	var value transferObject
	if err := contract.Decode(raw, maxJSON, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("transfer control record must be an object")
	}
	return value, nil
}
func transferString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}
func transferInt(raw json.RawMessage) int64 {
	var value int64
	if json.Unmarshal(raw, &value) != nil {
		return -1
	}
	return value
}
func transferBool(raw json.RawMessage, want bool) bool {
	var value *bool
	return json.Unmarshal(raw, &value) == nil && value != nil && *value == want
}
func transferEqual(raw json.RawMessage, value any) bool {
	var actual any
	return contract.Decode(raw, maxJSON, &actual) == nil && sameAssemblyJSON(actual, value)
}
func transferChild(record transferObject, key string) (transferObject, error) {
	return transferObjectOf(record[key])
}
func transferPair(record transferObject, key string, expected map[string]string) error {
	var values map[string]string
	if json.Unmarshal(record[key], &values) != nil || len(values) != 2 || !digestRE.MatchString(values["backend"]) || !digestRE.MatchString(values["web"]) || values["backend"] == values["web"] || expected != nil && !sameAssemblyJSON(values, expected) {
		return fmt.Errorf("native transfer configuration pair differs")
	}
	return nil
}
func (p *transferPlan) object(name string) (transferObject, error) {
	raw, err := p.read(name)
	if err != nil {
		return nil, err
	}
	return transferObjectOf(raw)
}
func checkTransferControl(record transferObject, identity scanning.NativeIdentity, kind string, schemas ...int64) error {
	schema := transferInt(record["schema_version"])
	allowed := false
	for _, value := range schemas {
		allowed = allowed || schema == value
	}
	if !allowed || transferString(record["kind"]) != kind || !transferEqual(record["source"], identity) || !transferBool(record["publication_authorized"], false) {
		return fmt.Errorf("transfer producer source/schema/approval boundary differs")
	}
	return nil
}

// DecodeNativeDescriptor checks the fixed schema2 producer boundary, not its
// attestation. Authentication of these exact bytes belongs to the receiving job.
func DecodeNativeDescriptor(raw []byte, identity scanning.NativeIdentity) (NativeDescriptor, error) {
	var descriptor NativeDescriptor
	if err := contract.Decode(raw, maxJSON, &descriptor); err != nil {
		return descriptor, err
	}
	record, err := transferObjectOf(raw)
	if err != nil {
		return descriptor, err
	}
	if len(record) != 10 {
		return descriptor, fmt.Errorf("native descriptor has missing fields")
	}
	if err := checkTransferControl(record, identity, "native-release-artifacts", 2); err != nil {
		return descriptor, err
	}
	if !transferBool(record["measurement_authentication_required"], true) || descriptor.OCIExporter != "docker-save-byte-preserving-oci-v1" || !digestRE.MatchString(descriptor.SourceHelperConfig) {
		return descriptor, fmt.Errorf("native descriptor exporter/authentication binding differs")
	}
	if err := transferPair(record, "original_tested_configs", nil); err != nil {
		return descriptor, err
	}
	if err := transferPair(record, "tested_configs", nil); err != nil {
		return descriptor, err
	}
	arch := strings.TrimPrefix(identity.Platform, "linux/")
	expected := map[string]string{"browser_inputs": "browser/browser-inputs.tar", "browser_verification": "browser/browser-verification.json", "build_record": "build-record.json", "final_archive": "final-images.docker.tar", "native_measurement": "native-measurement.json", "smoke_report": "smoke-report.json", "source_verification": "source-completeness-verification.json", "runtime_pack": "pack/source-inventory.json", "runtime_source": "pack/psst.zip-" + identity.Version + "-runtime-sources-" + arch + ".tar.gz", "backend_archive": "export/backend-" + arch + ".oci.tar", "web_archive": "export/web-" + arch + ".oci.tar"}
	if len(descriptor.Artifacts) != len(expected)+1 {
		return descriptor, fmt.Errorf("native descriptor must retain the complete current artifact set")
	}
	for key, record := range descriptor.Artifacts {
		if !safeTransferPath(record.File) || !digestRE.MatchString(record.SHA256) || record.Size <= 0 || record.Size > transferFileLimit {
			return descriptor, fmt.Errorf("invalid native artifact reference")
		}
		if key == "original_archive" {
			if path.Dir(record.File) != "original" || !strings.HasSuffix(record.File, ".tar") {
				return descriptor, fmt.Errorf("unsafe original image-save reference")
			}
		} else if expected[key] == "" || record.File != expected[key] {
			return descriptor, fmt.Errorf("native artifact path differs from fixed producer layout")
		}
	}
	return descriptor, nil
}
func checkTransferRecord(raw []byte, record Transfer, identity scanning.NativeIdentity, kind string) error {
	fields, err := transferObjectOf(raw)
	if err != nil {
		return err
	}
	if len(fields) != 7 || record.SchemaVersion != 1 || record.Kind != "native-candidate-transfer" || record.Source != identity || record.SourceKind != kind || !transferBool(fields["publication_authorized"], false) || !transferBool(fields["measurement_authentication_required"], true) || len(record.Files) == 0 || len(record.Files) > transferFileCount {
		return fmt.Errorf("transfer metadata source/authentication boundary differs")
	}
	return nil
}

var goTransferRaw = strings.Fields("go-version.txt go-env.json scanner-module.json scanner-binary-sha256.txt scanner-build.json go-roots.json go-all-graph.json go-server-graph.json go-modules.json govulncheck.json govulncheck.stderr govulncheck.exit govulncheck.txt govulncheck-convert.stderr govulncheck-convert.exit go-execution.complete execution.stderr")
var nodeTransferRaw = strings.Fields("node-version.txt npm-version.txt npm-lock-graph.json npm-graph.stderr npm-audit.json npm-audit.stderr npm-audit.exit node-execution.complete execution.stderr")
var compilerTransferRaw = strings.Fields("binary-build-info.json compiler-environment.json compiler-execution.complete compiler-graph.json go-version.txt")

func (p *transferPlan) raw(directory string, raw json.RawMessage, allowed []string) error {
	var files map[string]string
	if json.Unmarshal(raw, &files) != nil || len(files) != len(allowed) {
		return fmt.Errorf("unsupported/incomplete declared raw transfer inventory")
	}
	for _, name := range allowed {
		digest := files[name]
		if path.Base(name) != name || !safeTransferPath(name) || !digestRE.MatchString(digest) {
			return fmt.Errorf("unsafe or missing raw evidence checksum")
		}
		if err := p.add(directory+"/"+name, digest, nil, transferRawLimit, false); err != nil {
			return err
		}
	}
	return nil
}
func transferImage(raw json.RawMessage, expected json.RawMessage) bool {
	a, e := transferObjectOf(raw)
	b, f := transferObjectOf(expected)
	if e != nil || f != nil {
		return false
	}
	for _, key := range []string{"platform", "config_digest", "manifest_digest", "archive_digest"} {
		if !transferEqual(a[key], transferString(b[key])) {
			return false
		}
	}
	return true
}

func (p *transferPlan) selectNative(identity scanning.NativeIdentity) (NativeDescriptor, []byte, error) {
	raw, err := p.read("native/native-artifacts.json")
	if err != nil {
		return NativeDescriptor{}, nil, err
	}
	descriptor, err := DecodeNativeDescriptor(raw, identity)
	if err != nil {
		return descriptor, nil, err
	}
	for _, artifact := range descriptor.Artifacts {
		limit := transferFileLimit
		if strings.HasSuffix(artifact.File, ".json") {
			limit = maxJSON
		}
		if err := p.add("native/"+artifact.File, artifact.SHA256, &artifact.Size, limit, true); err != nil {
			return descriptor, nil, err
		}
	}
	measurement, err := p.object("native/native-measurement.json")
	if err != nil {
		return descriptor, nil, err
	}
	if err := checkTransferControl(measurement, identity, "native-release-measurement", 2); err != nil {
		return descriptor, nil, err
	}
	images, err := transferChild(measurement, "images")
	if err != nil || len(images) != 2 {
		return descriptor, nil, fmt.Errorf("native transfer measured image pair missing")
	}
	runtime, err := transferChild(measurement, "runtime")
	if err != nil {
		return descriptor, nil, err
	}
	runtimeAsset, err := transferChild(runtime, "source_asset")
	if err != nil {
		return descriptor, nil, err
	}
	runtimeFile := descriptor.Artifacts["runtime_source"]
	inventoryFile := descriptor.Artifacts["runtime_pack"]
	if transferString(runtime["runtime_pack_sha256"]) != inventoryFile.SHA256 || !transferBool(runtime["distribution_review_required"], true) || transferString(runtimeAsset["name"]) != path.Base(runtimeFile.File) || transferString(runtimeAsset["digest"]) != runtimeFile.SHA256 || transferInt(runtimeAsset["size"]) != runtimeFile.Size {
		return descriptor, nil, fmt.Errorf("native transfer runtime source reference differs")
	}
	smoke, err := p.object("native/smoke-report.json")
	if err != nil {
		return descriptor, nil, err
	}
	if !transferEqual(measurement["smoke"], smoke) || transferString(smoke["execution"]) != "native" || transferString(smoke["platform"]) != identity.Platform || transferString(smoke["version"]) != identity.Version || transferString(smoke["revision"]) != identity.Commit || transferString(smoke["runtime_pack_sha256"]) != inventoryFile.SHA256 || !transferBool(smoke["publication_authorized"], false) {
		return descriptor, nil, fmt.Errorf("native transfer smoke/source binding differs")
	}
	if err := transferPair(smoke, "tested_configs", descriptor.TestedConfigs); err != nil {
		return descriptor, nil, err
	}
	for _, component := range []string{"backend", "web"} {
		image, err := transferObjectOf(images[component])
		if err != nil || transferString(image["platform"]) != identity.Platform || transferString(image["config_digest"]) != descriptor.TestedConfigs[component] || transferString(image["archive_digest"]) != descriptor.Artifacts[component+"_archive"].SHA256 {
			return descriptor, nil, fmt.Errorf("native measured image differs from descriptor")
		}
	}
	pack, err := p.object("native/pack/source-inventory.json")
	if err != nil {
		return descriptor, nil, err
	}
	sourceIdentity := source.Context{Repository: identity.Repository, Version: identity.Version, Commit: identity.Commit}
	if transferInt(pack["schema_version"]) != 2 || transferString(pack["kind"]) != "container-source-inventory" || !transferEqual(pack["source"], sourceIdentity) {
		return descriptor, nil, fmt.Errorf("native runtime source inventory identity differs")
	}
	build, err := p.object("native/build-record.json")
	if err != nil {
		return descriptor, nil, err
	}
	original := descriptor.Artifacts["original_archive"]
	if transferString(build["version"]) != identity.Version || transferString(build["source_commit"]) != identity.Commit || transferString(build["checked_platform"]) != identity.Platform || !transferBool(build["native_execution"], true) || !transferEqual(build["image_archive"], map[string]any{"name": path.Base(original.File), "sha256": strings.TrimPrefix(original.SHA256, "sha256:"), "size": original.Size}) {
		return descriptor, nil, fmt.Errorf("original native build/save binding differs")
	}
	metadata, err := transferChild(build, "build_metadata")
	if err != nil || len(metadata) != 2 {
		return descriptor, nil, fmt.Errorf("original native metadata pair missing")
	}
	for _, component := range []string{"backend", "web"} {
		item, err := transferObjectOf(metadata[component])
		if err != nil || transferString(item["containerimage.config.digest"]) != descriptor.OriginalTestedConfigs[component] {
			return descriptor, nil, fmt.Errorf("original native image configuration differs")
		}
	}
	replay, err := p.object("native/source-completeness-verification.json")
	if err != nil {
		return descriptor, nil, err
	}
	if transferInt(replay["schema_version"]) != 1 || transferString(replay["kind"]) != "runtime-source-completeness" || transferString(replay["repository"]) != identity.Repository || transferString(replay["version"]) != identity.Version || transferString(replay["revision"]) != identity.Commit || transferString(replay["platform"]) != identity.Platform || !transferBool(replay["distribution_authorized"], false) || !transferBool(replay["runtime_source_inputs_verified"], true) || transferString(replay["runtime_pack_sha256"]) != inventoryFile.SHA256 || transferString(replay["runtime_source_asset_sha256"]) != runtimeFile.SHA256 || transferString(replay["native_smoke_report_sha256"]) != descriptor.Artifacts["smoke_report"].SHA256 || !transferEqual(replay["images"], images) {
		return descriptor, nil, fmt.Errorf("runtime replay byte references differ")
	}
	browser, err := p.object("native/browser/browser-verification.json")
	if err != nil {
		return descriptor, nil, err
	}
	if err := checkTransferControl(browser, identity, "native-browser-input-measurement", 2); err != nil {
		return descriptor, nil, err
	}
	if !digestRE.MatchString(transferString(browser["builder_config"])) || transferString(browser["tested_web_config"]) != descriptor.TestedConfigs["web"] || transferString(browser["browser_inputs_sha256"]) != descriptor.Artifacts["browser_inputs"].SHA256 || !transferImage(browser["image"], images["web"]) || !transferBool(browser["distribution_authorized"], false) || !transferBool(browser["source_reproduction_verified"], false) || !transferBool(browser["browser_module_closure_verified"], false) || !transferBool(browser["measurement_authentication_required"], true) {
		return descriptor, nil, fmt.Errorf("browser capture/report byte binding differs")
	}
	scans, err := p.object("source-scans/source-scan-measurement.json")
	if err != nil {
		return descriptor, nil, err
	}
	if err := checkTransferControl(scans, identity, "native-source-scanner-measurement", 1, 2); err != nil {
		return descriptor, nil, err
	}
	schema := transferInt(scans["schema_version"])
	execution := "native"
	if schema == 2 {
		execution = "native-go-common-lock"
	}
	if transferString(scans["execution"]) != execution || !transferBool(scans["source_scanners_gate_pending"], true) || !transferBool(scans["findings_review_required"], true) {
		return descriptor, nil, fmt.Errorf("source scans are incomplete or not native")
	}
	var rows []transferObject
	if json.Unmarshal(scans["scans"], &rows) != nil || len(rows) != 2 {
		return descriptor, nil, fmt.Errorf("source scan target pair missing")
	}
	seen := map[string]bool{}
	for _, scan := range rows {
		target := transferString(scan["target"])
		allowed := append([]string(nil), goTransferRaw...)
		if target == "web-source" {
			allowed = append([]string(nil), nodeTransferRaw...)
			if schema == 2 {
				allowed = append(allowed, "common-npm-audit.json")
			}
		} else if target != "backend-source" {
			return descriptor, nil, fmt.Errorf("unsupported source scanner target")
		}
		exit := transferInt(scan["exit_code"])
		if seen[target] || transferString(scan["subject"]) != "git:"+identity.Repository+"@"+identity.Commit || transferString(scan["status"]) != "complete" || exit != 0 && !(target == "web-source" && exit == 1) {
			return descriptor, nil, fmt.Errorf("source scanner source/execution differs")
		}
		seen[target] = true
		if err := p.raw("source-scans/"+target, scan["raw_files"], allowed); err != nil {
			return descriptor, nil, err
		}
	}
	for _, component := range []string{"backend", "web"} {
		if err := p.selectScanner(identity, component, images[component], descriptor.TestedConfigs[component]); err != nil {
			return descriptor, nil, err
		}
	}
	modules, err := p.object("application-dependencies/source-inventory.json")
	if err != nil {
		return descriptor, nil, err
	}
	if transferInt(modules["schema_version"]) != 2 || transferString(modules["kind"]) != "container-source-inventory" || !transferEqual(modules["source"], sourceIdentity) {
		return descriptor, nil, fmt.Errorf("module source inventory source/schema differs")
	}
	name := "application-dependencies/psst.zip-dependency-inputs-" + identity.Version + "-" + strings.TrimPrefix(identity.Platform, "linux/") + ".tar.gz"
	if err := p.add(name, "", nil, transferFileLimit, true); err != nil {
		return descriptor, nil, err
	}
	return descriptor, raw, nil
}

func (p *transferPlan) selectScanner(identity scanning.NativeIdentity, component string, image json.RawMessage, config string) error {
	directory := "compiler-" + component
	graph, err := p.object(directory + "/compiler-graph-measurement.json")
	if err != nil {
		return err
	}
	if err := checkTransferControl(graph, identity, "native-compiler-graph-measurement", 1, 2); err != nil {
		return err
	}
	if transferString(graph["execution"]) != "native" || transferString(graph["component"]) != component || transferString(graph["status"]) != "complete" || transferInt(graph["exit_code"]) != 0 || !transferBool(graph["finding_dispositions_authorized"], false) || !transferImage(graph["image"], image) {
		return fmt.Errorf("compiler measurement image/native binding differs")
	}
	allowed := append([]string(nil), compilerTransferRaw...)
	if transferInt(graph["schema_version"]) == 1 {
		allowed = append(allowed, "execution.stderr")
		if component == "backend" {
			allowed = append(allowed, "initial-build-info.json")
		} else {
			allowed = append(allowed, "caddy-signature-verification.json")
		}
	}
	if component == "backend" {
		allowed = append(allowed, "rebuilt-backend-sha256.txt")
	}
	advisories, err := transferChild(graph, "advisories")
	if err != nil || len(advisories) > 512 {
		return fmt.Errorf("compiler advisory inventory missing or excessive")
	}
	for identifier, raw := range advisories {
		advisory, err := transferObjectOf(raw)
		if err != nil || !regexp.MustCompile(`^GO-[0-9]{4}-[0-9]+$`).MatchString(identifier) || transferString(advisory["raw_file"]) != identifier+".json" || transferString(advisory["origin"]) != "https://vuln.go.dev/ID/"+identifier+".json" || !digestRE.MatchString(transferString(advisory["sha256"])) {
			return fmt.Errorf("unsafe compiler advisory reference")
		}
		allowed = append(allowed, identifier+".json")
		if err := p.add(directory+"/"+identifier+".json", transferString(advisory["sha256"]), nil, transferRawLimit, false); err != nil {
			return err
		}
	}
	if err := p.raw(directory, graph["raw_files"], allowed); err != nil {
		return err
	}
	sourceGraph, err := transferChild(graph, "source_graph")
	if err != nil {
		return err
	}
	rawFiles, err := transferChild(graph, "raw_files")
	if err != nil {
		return err
	}
	if transferString(sourceGraph["raw_file"]) != "compiler-graph.json" || transferString(sourceGraph["sha256"]) != transferString(rawFiles["compiler-graph.json"]) {
		return fmt.Errorf("compiler graph raw byte reference differs")
	}
	directory = "image-scan-" + component
	scan, err := p.object(directory + "/measurement.json")
	if err != nil {
		return err
	}
	if err := checkTransferControl(scan, identity, "native-image-scanner-measurement", 1); err != nil {
		return err
	}
	if transferString(scan["execution"]) != "native" || transferString(scan["target"]) != component+"-"+strings.TrimPrefix(identity.Platform, "linux/") || transferString(scan["status"]) != "complete" || transferInt(scan["exit_code"]) != 0 || !transferBool(scan["findings_review_required"], true) || !transferBool(scan["final_image_scanners_gate_pending"], true) || !transferImage(scan["image"], image) || !digestRE.MatchString(transferString(scan["raw_report_sha256"])) {
		return fmt.Errorf("image scanner image/source/native binding differs")
	}
	if err := p.add(directory+"/scan.json", transferString(scan["raw_report_sha256"]), nil, maxJSON, true); err != nil {
		return err
	}
	report, err := p.object(directory + "/scan.json")
	if err != nil {
		return err
	}
	metadata, err := transferChild(report, "Metadata")
	if err != nil || transferString(metadata["ImageID"]) != config {
		return fmt.Errorf("image scanner raw configuration differs")
	}
	return nil
}
