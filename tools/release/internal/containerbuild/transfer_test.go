package containerbuild

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/scanning"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

func transferFixture(t *testing.T) (string, scanning.NativeIdentity, NativeDescriptor) {
	t.Helper()
	root := t.TempDir()
	identity := scanning.NativeIdentity{Source: scanning.Source{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}, Platform: "linux/amd64"}
	write := func(name string, value any) TransferFile {
		t.Helper()
		raw, ok := value.([]byte)
		if !ok {
			raw = encoded(t, value)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return TransferFile{assemblyHash(raw), int64(len(raw))}
	}
	artifacts := map[string]NativeArtifact{}
	add := func(key, name string, value any) {
		fact := write("native/"+name, value)
		artifacts[key] = NativeArtifact{name, fact.SHA256, fact.Size}
	}
	original, tested := map[string]string{"backend": dg("1"), "web": dg("2")}, map[string]string{"backend": dg("3"), "web": dg("4")}
	for key, name := range map[string]string{"original_archive": "original/pair.tar", "final_archive": "final-images.docker.tar", "backend_archive": "export/backend-amd64.oci.tar", "web_archive": "export/web-amd64.oci.tar", "browser_inputs": "browser/browser-inputs.tar", "runtime_source": "pack/psst.zip-v1.2.3-runtime-sources-amd64.tar.gz"} {
		add(key, name, []byte("Opaque fixture bytes: no archive parser should execute: "+key))
	}
	inventory := map[string]any{"schema_version": 2, "kind": "container-source-inventory", "source": source.Context{Repository: identity.Repository, Version: identity.Version, Commit: identity.Commit}}
	add("runtime_pack", "pack/source-inventory.json", inventory)
	images := map[string]any{}
	for i, component := range []string{"backend", "web"} {
		images[component] = map[string]any{"platform": identity.Platform, "manifest_digest": dg(string(rune('5' + i))), "manifest_size": 123 + i, "manifest_media_type": "application/vnd.oci.image.manifest.v1+json", "config_digest": tested[component], "archive_digest": artifacts[component+"_archive"].SHA256, "blob_count": 3}
	}
	smoke := map[string]any{"schema_version": 1, "kind": "release-image-smoke", "version": identity.Version, "revision": identity.Commit, "platform": identity.Platform, "execution": "native", "tested_configs": tested, "runtime_pack_sha256": artifacts["runtime_pack"].SHA256, "checks": []string{}, "completed_at": "2026-10-24T00:00:00Z", "publication_authorized": false}
	add("smoke_report", "smoke-report.json", smoke)
	runtime := map[string]any{"runtime_pack_sha256": artifacts["runtime_pack"].SHA256, "source_asset": map[string]any{"name": filepath.Base(artifacts["runtime_source"].File), "digest": artifacts["runtime_source"].SHA256, "size": artifacts["runtime_source"].Size}, "notice_files": map[string]any{}, "distribution_review_required": true}
	add("native_measurement", "native-measurement.json", map[string]any{"schema_version": 2, "kind": "native-release-measurement", "source": identity, "smoke": smoke, "images": images, "runtime": runtime, "publication_authorized": false})
	metadata := map[string]any{}
	for _, component := range []string{"backend", "web"} {
		metadata[component] = map[string]string{"containerimage.config.digest": original[component]}
	}
	add("build_record", "build-record.json", map[string]any{"version": identity.Version, "source_commit": identity.Commit, "checked_platform": identity.Platform, "native_execution": true, "image_archive": map[string]any{"name": "pair.tar", "sha256": strings.TrimPrefix(artifacts["original_archive"].SHA256, "sha256:"), "size": artifacts["original_archive"].Size}, "build_metadata": metadata})
	add("source_verification", "source-completeness-verification.json", map[string]any{"schema_version": 1, "kind": "runtime-source-completeness", "repository": identity.Repository, "version": identity.Version, "revision": identity.Commit, "platform": identity.Platform, "distribution_authorized": false, "runtime_source_inputs_verified": true, "runtime_pack_sha256": artifacts["runtime_pack"].SHA256, "runtime_source_asset_sha256": artifacts["runtime_source"].SHA256, "native_smoke_report_sha256": artifacts["smoke_report"].SHA256, "images": images})
	add("browser_verification", "browser/browser-verification.json", map[string]any{"schema_version": 2, "kind": "native-browser-input-measurement", "source": identity, "publication_authorized": false, "builder_config": dg("7"), "tested_web_config": tested["web"], "browser_inputs_sha256": artifacts["browser_inputs"].SHA256, "image": images["web"], "distribution_authorized": false, "source_reproduction_verified": false, "browser_module_closure_verified": false, "measurement_authentication_required": true})
	descriptor := NativeDescriptor{SchemaVersion: 2, Kind: "native-release-artifacts", Source: identity, OriginalTestedConfigs: original, TestedConfigs: tested, SourceHelperConfig: dg("8"), OCIExporter: "docker-save-byte-preserving-oci-v1", Artifacts: artifacts, MeasurementAuthenticationRequired: true}
	write("native/native-artifacts.json", descriptor)
	var scans []any
	for _, target := range []string{"backend-source", "web-source"} {
		allowed := goTransferRaw
		if target == "web-source" {
			allowed = nodeTransferRaw
		}
		raw := map[string]string{}
		for _, name := range allowed {
			body := []byte("opaque native receipt")
			if strings.HasSuffix(name, ".stderr") {
				body = nil
			}
			fact := write("source-scans/"+target+"/"+name, body)
			raw[name] = fact.SHA256
		}
		scans = append(scans, map[string]any{"target": target, "subject": "git:" + identity.Repository + "@" + identity.Commit, "status": "complete", "exit_code": 0, "raw_files": raw})
	}
	write("source-scans/source-scan-measurement.json", map[string]any{"schema_version": 1, "kind": "native-source-scanner-measurement", "source": identity, "publication_authorized": false, "execution": "native", "source_scanners_gate_pending": true, "findings_review_required": true, "scans": scans})
	for _, component := range []string{"backend", "web"} {
		directory := "compiler-" + component
		allowed := append([]string(nil), compilerTransferRaw...)
		if component == "backend" {
			allowed = append(allowed, "rebuilt-backend-sha256.txt")
		}
		raw := map[string]string{}
		for _, name := range allowed {
			fact := write(directory+"/"+name, []byte("original native compiler receipt"))
			raw[name] = fact.SHA256
		}
		measured := images[component].(map[string]any)
		compilerImage := map[string]any{}
		for _, key := range []string{"platform", "config_digest", "manifest_digest", "archive_digest"} {
			compilerImage[key] = measured[key]
		}
		write(directory+"/compiler-graph-measurement.json", map[string]any{"schema_version": 2, "kind": "native-compiler-graph-measurement", "source": identity, "publication_authorized": false, "execution": "native", "component": component, "image": compilerImage, "status": "complete", "exit_code": 0, "finding_dispositions_authorized": false, "advisories": map[string]any{}, "raw_files": raw, "source_graph": map[string]string{"raw_file": "compiler-graph.json", "sha256": raw["compiler-graph.json"]}})
		directory = "image-scan-" + component
		report := write(directory+"/scan.json", map[string]any{"Metadata": map[string]string{"ImageID": tested[component]}, "Results": []any{}})
		write(directory+"/measurement.json", map[string]any{"schema_version": 1, "kind": "native-image-scanner-measurement", "source": identity, "publication_authorized": false, "execution": "native", "target": component + "-amd64", "image": images[component], "status": "complete", "exit_code": 0, "findings_review_required": true, "final_image_scanners_gate_pending": true, "raw_report_sha256": report.SHA256})
	}
	write("application-dependencies/source-inventory.json", inventory)
	write("application-dependencies/psst.zip-dependency-inputs-v1.2.3-amd64.tar.gz", []byte("opaque Go module offering"))
	write("tools/cosign", []byte("private executable must never transfer"))
	return root, identity, descriptor
}

func TestTransferSharedNPMSourceSchemaRetainsOneDeclaredRawInventory(t *testing.T) {
	private, identity, _ := transferFixture(t)
	name := filepath.Join(private, "source-scans", "source-scan-measurement.json")
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var measurement map[string]any
	if err := json.Unmarshal(raw, &measurement); err != nil {
		t.Fatal(err)
	}
	measurement["schema_version"] = 2
	measurement["execution"] = "native-go-common-lock"
	common := []byte("Opaque bounded common native npm measurement")
	commonName := filepath.Join(private, "source-scans", "web-source", "common-npm-audit.json")
	if err := os.WriteFile(commonName, common, 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range measurement["scans"].([]any) {
		scan := value.(map[string]any)
		if scan["target"] == "web-source" {
			scan["raw_files"].(map[string]any)["common-npm-audit.json"] = assemblyHash(common)
		}
	}
	if err := os.WriteFile(name, encoded(t, measurement), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "retained")
	collected, err := CollectTransfer(context.Background(), private, output, identity, "planned-main-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	if collected.Files["source-scans/web-source/common-npm-audit.json"].SHA256 != assemblyHash(common) {
		t.Fatal("lost common native raw measurement")
	}
	if _, err := VerifyTransfer(context.Background(), output, identity, "planned-main-dispatch"); err != nil {
		t.Fatal(err)
	}
}

func TestTransferCurrentByteTreesRelocateAndReconstructHeavyPayloads(t *testing.T) {
	private, identity, descriptor := transferFixture(t)
	output := filepath.Join(t.TempDir(), "transfer")
	retained, err := CollectTransfer(context.Background(), private, output, identity, "planned-main-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	if retained.PublicationAuthorized || !retained.MeasurementAuthenticationRequired || len(retained.Files) < 50 {
		t.Fatal("changed transfer contract", retained.Transfer)
	}
	if _, err := os.Lstat(filepath.Join(output, "tools/cosign")); !os.IsNotExist(err) {
		t.Fatal("transferred private executable")
	}
	verified, err := VerifyTransfer(context.Background(), output, identity, "planned-main-dispatch")
	if err != nil || !reflectTransferFiles(retained.Files, verified.Files) || !sameRawTransfer(retained.MetadataRaw, verified.MetadataRaw) || !sameRawTransfer(retained.DescriptorRaw, verified.DescriptorRaw) {
		t.Fatal("retained/verified bytes differ", err)
	}
	detached := t.TempDir()
	for _, name := range HeavyTransferPaths(descriptor) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(detached, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(output, name), filepath.Join(detached, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := VerifyTransfer(context.Background(), output, identity, "planned-main-dispatch"); err == nil {
		t.Fatal("accepted metadata without reconstructed image payload")
	}
	for _, name := range HeavyTransferPaths(descriptor) {
		if err := os.Rename(filepath.Join(detached, name), filepath.Join(output, name)); err != nil {
			t.Fatal(err)
		}
	}
	relocated := filepath.Join(t.TempDir(), "relocated")
	if err := os.Rename(output, relocated); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTransfer(context.Background(), relocated, identity, "planned-main-dispatch"); err != nil {
		t.Fatal("relocated complete transfer failed", err)
	}
}

func TestTransferRejectsMissingUnsafeChangedOrWidenedPayload(t *testing.T) {
	for _, mode := range []string{"missing", "symlink", "parent-link", "stale-source", "authorization", "legacy-browser", "raw-secret", "size-bool", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			private, identity, descriptor := transferFixture(t)
			output := filepath.Join(t.TempDir(), "transfer")
			modify := func(name string, change func(map[string]any)) {
				t.Helper()
				p := filepath.Join(private, name)
				raw, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				var value map[string]any
				if err := json.Unmarshal(raw, &value); err != nil {
					t.Fatal(err)
				}
				change(value)
				if err := os.WriteFile(p, encoded(t, value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "missing":
				if err := os.Remove(filepath.Join(private, "compiler-backend/compiler-graph.json")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				p := filepath.Join(private, "compiler-backend/compiler-graph.json")
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(private, "tools/cosign"), p); err != nil {
					t.Fatal(err)
				}
			case "parent-link":
				p := filepath.Join(private, "compiler-backend")
				if err := os.Rename(p, p+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(p+"-original", p); err != nil {
					t.Fatal(err)
				}
			case "stale-source":
				identity.Commit = strings.Repeat("b", 40)
			case "authorization":
				modify("native/native-artifacts.json", func(v map[string]any) { v["publication_authorized"] = true })
			case "legacy-browser":
				modify("native/browser/browser-verification.json", func(v map[string]any) { v["schema_version"] = 1 })
			case "raw-secret":
				modify("compiler-web/compiler-graph-measurement.json", func(v map[string]any) { v["raw_files"].(map[string]any)["runtime-binary"] = dg("0") })
			case "size-bool":
				modify("native/native-artifacts.json", func(v map[string]any) {
					v["artifacts"].(map[string]any)["final_archive"].(map[string]any)["size"] = true
				})
			case "oversize":
				p := filepath.Join(private, "native", descriptor.Artifacts["final_archive"].File)
				file, err := os.OpenFile(p, os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(transferFileLimit + 1); err != nil {
					t.Fatal(err)
				}
				file.Close()
			}
			if _, err := CollectTransfer(context.Background(), private, output, identity, "planned-main-dispatch"); err == nil {
				t.Fatal("accepted invalid input", mode)
			}
			if _, err := os.Lstat(filepath.Join(output, transferMetadata)); !os.IsNotExist(err) {
				t.Fatal("invalid input wrote terminal metadata", mode, err)
			}
		})
	}
}

func TestTransferReceiverRejectsExtraFilesDirectoriesAndForgedInventory(t *testing.T) {
	for _, mode := range []string{"extra", "empty-directory", "linked", "missing", "changed", "signed-extra", "source-kind", "bool-size"} {
		t.Run(mode, func(t *testing.T) {
			private, identity, _ := transferFixture(t)
			output := filepath.Join(t.TempDir(), "transfer")
			retained, err := CollectTransfer(context.Background(), private, output, identity, "version-tag")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "extra":
				os.WriteFile(filepath.Join(output, "cookie"), []byte("extra"), 0600)
			case "empty-directory":
				os.Mkdir(filepath.Join(output, "unused"), 0700)
			case "linked":
				os.Symlink(filepath.Join(private, "tools/cosign"), filepath.Join(output, "cookie"))
			case "missing":
				os.Remove(filepath.Join(output, "compiler-web/compiler-graph.json"))
			case "changed":
				os.WriteFile(filepath.Join(output, "compiler-web/compiler-graph.json"), []byte("changed"), 0600)
			case "signed-extra":
				body := []byte("private data even with a correct hash is outside the allowlist")
				os.WriteFile(filepath.Join(output, "cookie"), body, 0600)
				retained.Files["cookie"] = TransferFile{assemblyHash(body), int64(len(body))}
				os.WriteFile(filepath.Join(output, transferMetadata), encoded(t, retained.Transfer), 0600)
			case "source-kind":
				retained.SourceKind = "planned-main-dispatch"
				os.WriteFile(filepath.Join(output, transferMetadata), encoded(t, retained.Transfer), 0600)
			case "bool-size":
				var value map[string]any
				if err := json.Unmarshal(retained.MetadataRaw, &value); err != nil {
					t.Fatal(err)
				}
				for _, record := range value["files"].(map[string]any) {
					record.(map[string]any)["size"] = true
					break
				}
				os.WriteFile(filepath.Join(output, transferMetadata), encoded(t, value), 0600)
			}
			if _, err := VerifyTransfer(context.Background(), output, identity, "version-tag"); err == nil {
				t.Fatal("receiver accepted invalid/widened transfer", mode)
			}
		})
	}
}

func TestTransferDetectsMutationAndCancellationWithoutTerminalRecord(t *testing.T) {
	private, identity, _ := transferFixture(t)
	plan, err := openTransferPlan(private)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.root.Close()
	if _, _, err := plan.selectNative(identity); err != nil {
		t.Fatal(err)
	}
	p := "native/final-images.docker.tar"
	file, err := os.OpenFile(filepath.Join(private, p), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("changed"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := plan.stream(context.Background(), p, nil); err == nil {
		t.Fatal("did not bind selected-file stamp before copy")
	}
	private, identity, _ = transferFixture(t)
	output := filepath.Join(t.TempDir(), "transfer")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CollectTransfer(ctx, private, output, identity, "version-tag"); err == nil {
		t.Fatal("cancelled copy completed")
	}
	if _, err := os.Lstat(filepath.Join(output, transferMetadata)); !os.IsNotExist(err) {
		t.Fatal("cancelled transfer wrote terminal metadata", err)
	}
	if err := writeRecord(filepath.Join(t.TempDir(), "oversized.json"), make([]byte, maxJSON+1)); err == nil {
		t.Fatal("retained JSON larger than the receiving reader bound")
	}
}
