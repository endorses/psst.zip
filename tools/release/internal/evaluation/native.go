package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/containerbuild"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
)

type nativeInput struct {
	Tree         string
	Transfer     containerbuild.TransferVerification
	Assembly     containerbuild.NativeAssemblyInput
	Measurements map[string][]byte
}
type transferVerifier func(context.Context, string, scanning.NativeIdentity, string) (containerbuild.TransferVerification, error)

func authenticateNative(ctx context.Context, cache *authenticationCache, verify transferVerifier, binding github.Binding, platform, tree string) (nativeInput, error) {
	result := nativeInput{Tree: tree, Measurements: map[string][]byte{}}
	// Authenticate small descriptors before parsing their source/configuration or
	// artifact claims. Unsigned transfer metadata is never a trust root.
	metadata, err := cache.file(ctx, filepath.Join(tree, "candidate-transfer.json"))
	if err != nil {
		return result, err
	}
	descriptor, err := cache.file(ctx, filepath.Join(tree, "native/native-artifacts.json"))
	if err != nil {
		return result, err
	}
	identity := scanning.NativeIdentity{Source: scanning.Source{Repository: binding.Repository, Version: binding.Version, Commit: binding.Commit}, Platform: platform}
	result.Transfer, err = verify(ctx, tree, identity, "version-tag")
	if err != nil {
		return result, err
	}
	if !bytes.Equal(metadata, result.Transfer.MetadataRaw) || !bytes.Equal(descriptor, result.Transfer.DescriptorRaw) {
		return result, fmt.Errorf("native verified descriptors differ from authenticated bytes")
	}
	for _, name := range []string{"native/native-measurement.json", "native/source-completeness-verification.json", "native/browser/browser-verification.json", "source-scans/source-scan-measurement.json", "compiler-backend/compiler-graph-measurement.json", "compiler-web/compiler-graph-measurement.json", "image-scan-backend/measurement.json", "image-scan-web/measurement.json"} {
		raw, e := cache.file(ctx, filepath.Join(tree, filepath.FromSlash(name)))
		if e != nil {
			return result, e
		}
		fact, ok := result.Transfer.Files[name]
		if !ok || fact.SHA256 != digest(raw) || fact.Size != int64(len(raw)) {
			return result, fmt.Errorf("authenticated native record differs from signed transfer inventory")
		}
		result.Measurements[name] = raw
	}
	arch := strings.TrimPrefix(platform, "linux/")
	result.Assembly = containerbuild.NativeAssemblyInput{BuildRecord: filepath.Join(tree, "native/build-record.json"), Measurement: filepath.Join(tree, "native/native-measurement.json"), RuntimeCollection: filepath.Join(tree, "native/pack"), ModuleCollection: filepath.Join(tree, "application-dependencies"), SourceScan: filepath.Join(tree, "source-scans/source-scan-measurement.json"), BrowserCapture: filepath.Join(tree, "native/browser/browser-inputs.tar"), BrowserVerification: filepath.Join(tree, "native/browser/browser-verification.json"), Archives: map[string]string{"backend": filepath.Join(tree, "native/export/backend-"+arch+".oci.tar"), "web": filepath.Join(tree, "native/export/web-"+arch+".oci.tar")}}
	return result, nil
}

func checkFoundationNative(foundation containerbuild.ArtifactFoundation, inputs map[string]nativeInput) error {
	for platform, input := range inputs {
		facts, ok := foundation.Native[platform]
		if !ok || !reflect.DeepEqual(facts.TestedConfigs, input.Transfer.Descriptor.TestedConfigs) {
			return fmt.Errorf("assembled native configurations differ from authenticated descriptor")
		}
		for key, name := range map[string]string{"build_record": "native/build-record.json", "measurement": "native/native-measurement.json", "source_scan": "source-scans/source-scan-measurement.json", "browser_verification": "native/browser/browser-verification.json", "browser_capture": "native/browser/browser-inputs.tar", "module_inventory": "application-dependencies/source-inventory.json", "runtime_inventory": "native/pack/source-inventory.json"} {
			if facts.Inputs[key] != input.Transfer.Files[name].SHA256 {
				return fmt.Errorf("assembled native input differs from authenticated payload")
			}
		}
		for _, component := range []string{"backend", "web"} {
			name := component + "_archive"
			artifact := input.Transfer.Descriptor.Artifacts[name]
			image := foundation.Runtime[platform].Images[component]
			if image.ArchiveDigest != artifact.SHA256 || image.ConfigDigest != input.Transfer.Descriptor.TestedConfigs[component] {
				return fmt.Errorf("assembled final OCI differs from authenticated native export")
			}
		}
		for _, asset := range []struct{ name, digest string }{{foundation.Runtime[platform].Asset.Path, foundation.Runtime[platform].Asset.SHA256}} {
			if input.Transfer.Files["native/pack/"+asset.name].SHA256 != asset.digest {
				return fmt.Errorf("assembled runtime offering differs from authenticated input")
			}
		}
		arch := strings.TrimPrefix(platform, "linux/")
		moduleName := "psst.zip-dependency-inputs-" + foundation.Version + "-" + arch + ".tar.gz"
		if foundation.Assets[moduleName] != input.Transfer.Files["application-dependencies/"+moduleName].SHA256 {
			return fmt.Errorf("assembled module offering differs from authenticated native payload")
		}
	}
	return nil
}

func smokeDetails(foundation containerbuild.ArtifactFoundation, inputs map[string]nativeInput) (map[string]any, map[string]any, error) {
	execution := map[string]string{}
	configs := map[string]string{}
	records := map[string]any{}
	for platform, input := range inputs {
		var measurement struct {
			Smoke   json.RawMessage            `json:"smoke"`
			Images  map[string]json.RawMessage `json:"images"`
			Runtime json.RawMessage            `json:"runtime"`
		}
		if err := json.Unmarshal(input.Measurements["native/native-measurement.json"], &measurement); err != nil {
			return nil, nil, err
		}
		// Assemble independently checked all smoke fields and each runtime byte.
		// Record that completed substantive replay once in the final result.
		execution[platform] = "native"
		arch := strings.TrimPrefix(platform, "linux/")
		for component, config := range foundation.Native[platform].TestedConfigs {
			configs[component+"-"+arch] = config
		}
		records[platform] = map[string]any{"measurement_digest": digest(input.Measurements["native/native-measurement.json"]), "smoke": measurement.Smoke, "images": measurement.Images, "runtime": measurement.Runtime}
	}
	return map[string]any{"execution": execution, "tested_configs": configs, "native_measurements": records}, map[string]any{"native_measurements": records, "distribution_review_required": true}, nil
}
