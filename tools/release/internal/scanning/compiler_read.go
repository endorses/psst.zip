package scanning

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

// CompilerExpected comes from the independently replayed runtime source and
// final OCI image, not fields asserted by the compiler report itself.
type CompilerExpected struct {
	Source                                     NativeIdentity
	Component                                  string
	Image                                      ImageIdentity
	SourceSHA256, GoImage                      string
	Binary, GoPolicy, VendorSum, VendorModules []byte
	Caddy                                      source.Source
	Signatures                                 []source.SignatureVerification
}

// ReadCompiler re-derives compiler correspondence from retained bounded raw
// reports after the caller authenticates the measurement's exact bytes. Runtime
// source/OCI replay is shared with the caller rather than repeated per finding.
func ReadCompiler(directory string, expected CompilerExpected) (CompilerMeasurement, error) {
	raw, err := bounded(filepath.Join(directory, "compiler-graph-measurement.json"), scannerRawLimit)
	if err != nil {
		return CompilerMeasurement{}, err
	}
	return decodeCompiler(directory, raw, expected)
}

// DecodeCompiler validates the same immutable bytes authenticated by an
// assembler, avoiding a second path read after signature verification.
func DecodeCompiler(directory string, raw []byte, expected CompilerExpected) (CompilerMeasurement, error) {
	return decodeCompiler(directory, raw, expected)
}
func decodeCompiler(directory string, raw []byte, expected CompilerExpected) (CompilerMeasurement, error) {
	var result CompilerMeasurement
	if err := expected.Source.Validate(); err != nil {
		return result, err
	}
	if err := contract.Decode(raw, scannerRawLimit, &result); err != nil {
		return result, err
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields["publication_authorized"] != false || fields["finding_dispositions_authorized"] != false || fields["exit_code"] != float64(0) || result.Schema != 2 || result.Kind != "native-compiler-graph-measurement" || result.Source != expected.Source || result.Execution != "native" || result.Component != expected.Component || result.Target != compilerTarget(expected.Component) || result.Status != "complete" || result.Authorized || result.Dispositions || result.Image != expected.Image || result.Inputs["runtime_source_asset_sha256"] != expected.SourceSHA256 {
		return result, fmt.Errorf("compiler measurement source/image/completion differs")
	}
	if _, err := time.Parse(time.RFC3339Nano, result.Completed); err != nil {
		return result, fmt.Errorf("invalid compiler completion time")
	}
	b := result.Builder
	arch := strings.TrimPrefix(expected.Source.Platform, "linux/")
	if b.Reference != expected.GoImage || !strings.HasPrefix(b.Reference, "docker.io/library/golang@") || !digestPattern.MatchString(strings.TrimPrefix(b.Reference, "docker.io/library/golang@")) || !digestPattern.MatchString(b.Config) || b.Architecture != arch || len(b.Layers) == 0 || b.Daemon != arch && !(arch == "amd64" && b.Daemon == "x86_64") && !(arch == "arm64" && b.Daemon == "aarch64") {
		return result, fmt.Errorf("compiler measurement used a different or emulated builder")
	}
	for _, layer := range b.Layers {
		if !digestPattern.MatchString(layer) {
			return result, fmt.Errorf("compiler builder layer is invalid")
		}
	}
	build, err := buildinfo.Read(bytes.NewReader(expected.Binary))
	if err != nil {
		return result, fmt.Errorf("expected compiler executable metadata unavailable")
	}
	if result.Binary.SHA256 != hash(expected.Binary) || result.Binary.BuildSHA256 != hash(legacyJSON(build)) || !reflect.DeepEqual(result.Binary.Build, build) {
		return result, fmt.Errorf("compiler metadata differs from actual final executable")
	}
	if _, err = source.GoRuntimeSourcePin(expected.GoPolicy, build.GoVersion); err != nil {
		return result, err
	}
	reports := map[string][]byte{}
	names := []string{"go-version.txt", "binary-build-info.json", "compiler-graph.json", "compiler-environment.json", "compiler-execution.complete"}
	if expected.Component == "backend" {
		names = append(names, "rebuilt-backend-sha256.txt")
	}
	for id, advisory := range result.Advisories {
		if !regexp.MustCompile(`^GO-[0-9]{4}-[0-9]+$`).MatchString(id) || advisory.RawFile != id+".json" || advisory.Origin != "https://vuln.go.dev/ID/"+id+".json" || !digestPattern.MatchString(advisory.SHA256) {
			return result, fmt.Errorf("invalid official compiler advisory identity")
		}
		names = append(names, advisory.RawFile)
	}
	if len(names) != len(result.RawFiles) || len(names) > 64 {
		return result, fmt.Errorf("compiler raw file inventory differs")
	}
	for _, name := range names {
		data, e := bounded(filepath.Join(directory, name), scannerRawLimit)
		if e != nil || result.RawFiles[name] != hash(data) {
			return result, fmt.Errorf("compiler raw evidence was substituted")
		}
		reports[name] = data
	}
	graph, err := CompilerCorrespondence(reports, result.Component, result.Source.Platform, expected.Binary, build, expected.VendorSum, expected.VendorModules)
	if err != nil {
		return result, err
	}
	if !reflect.DeepEqual(result.Graph, graph) {
		return result, fmt.Errorf("compiler graph summary differs from raw execution")
	}
	for _, name := range []string{"go_mod_sha256", "go_sum_sha256"} {
		if !digestPattern.MatchString(result.Inputs[name]) {
			return result, fmt.Errorf("compiler exact source module lock missing")
		}
	}
	if expected.Component == "backend" {
		if !digestPattern.MatchString(result.Inputs["application_archive_sha256"]) || len(result.Signatures) != 0 {
			return result, fmt.Errorf("backend exact source snapshot/signature scope differs")
		}
	} else {
		if result.Inputs["wrapper_revision"] != expected.Caddy.Authentication["source_commit"] || result.Inputs["authenticated_wrapper_sha256"] != expected.Caddy.Original.SHA256 || result.Inputs["vendor_modules_sha256"] != hash(expected.VendorModules) || result.Inputs["go_sum_sha256"] != hash(expected.VendorSum) {
			return result, fmt.Errorf("Caddy compiler source differs from retained original")
		}
		if err = compilerSignatureProofs(result.Signatures, expected.Signatures, result.Source.Platform, expected.Caddy); err != nil {
			return result, err
		}
	}
	return result, nil
}
func compilerTarget(component string) string {
	if component == "backend" {
		return "app/server"
	}
	if component == "web" {
		return "usr/bin/caddy"
	}
	return ""
}
func compilerSignatureProofs(actual, expected []source.SignatureVerification, platform string, caddy source.Source) error {
	if len(actual) != 2 || len(expected) != 2 {
		return fmt.Errorf("Caddy compiler lacks both official signature executions")
	}
	seen := map[string]bool{}
	for _, proof := range actual {
		name := proof.Artifact.Path
		version, _, digest, err := source.CaddyVerifierPolicy(platform)
		if err != nil {
			return err
		}
		if seen[name] || proof.Platform != platform || proof.VerifierVersion != version || proof.Verifier.Path != "cosign" || proof.Verifier.SHA256 != digest || proof.Signer != "https://github.com/caddyserver/caddy/.github/workflows/release.yml@refs/tags/"+caddy.Version || proof.Issuer != "https://token.actions.githubusercontent.com" || proof.WorkflowCommit != caddy.Authentication["source_commit"] || !digestPattern.MatchString(proof.Log.SHA256) || proof.Log.Size <= 0 {
			return fmt.Errorf("Caddy compiler proof differs from official signer/transparency profile")
		}
		seen[name] = true
		count := 0
		for _, prior := range expected {
			if prior.Artifact.Path != name {
				continue
			}
			count++
			if prior.Artifact != proof.Artifact || prior.Certificate != proof.Certificate || prior.Signature != proof.Signature || prior.Verifier != proof.Verifier || !reflect.DeepEqual(prior.SHA512Bindings, proof.SHA512Bindings) {
				return fmt.Errorf("Caddy compiler proof substituted original or verifier bytes")
			}
		}
		if count != 1 || !slices.Contains([]string{caddy.Original.Path, "caddy/caddy_" + strings.TrimPrefix(caddy.Version, "v") + "_checksums.txt"}, name) {
			return fmt.Errorf("Caddy compiler verified wrong upstream artifact")
		}
	}
	return nil
}
