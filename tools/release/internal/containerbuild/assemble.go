package containerbuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/browser"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

// NativeAssemblyInput contains original local measurements, never success flags.
// Archives maps the exact backend/web pair for this native platform.
type NativeAssemblyInput struct {
	BuildRecord         string            `json:"build_record"`
	Measurement         string            `json:"measurement"`
	RuntimeCollection   string            `json:"runtime_collection"`
	ModuleCollection    string            `json:"module_collection"`
	SourceScan          string            `json:"source_scan"`
	BrowserCapture      string            `json:"browser_capture"`
	BrowserVerification string            `json:"browser_verification"`
	Archives            map[string]string `json:"archives"`
}

// AssembleOptions selects one exact reviewed source and both native outputs.
// ApplicationCollection optionally names an existing exact application archive;
// otherwise assembly creates it directly from Git objects.
type AssembleOptions struct {
	Repository            string                         `json:"repository"`
	Ref                   string                         `json:"ref"`
	EventSHA              string                         `json:"event_sha"`
	ReviewedCommit        string                         `json:"reviewed_commit"`
	PlannedVersion        string                         `json:"planned_version,omitempty"`
	EventName             string                         `json:"event_name"`
	Candidate             string                         `json:"candidate"`
	CommonCollection      string                         `json:"common_collection"`
	ApplicationCollection string                         `json:"application_collection,omitempty"`
	Output                string                         `json:"output"`
	Cosign                string                         `json:"cosign"`
	MigrationNotes        string                         `json:"migration_notes"`
	RollbackNotes         string                         `json:"rollback_notes"`
	Native                map[string]NativeAssemblyInput `json:"native"`
}

// ModuleVerification shares authenticated retained members with higher checks.
type ModuleVerification struct {
	Inventory source.Inventory
	Files     map[string][]byte
}

// NativeAssemblyFacts binds raw measurements and their substantive replay once.
type NativeAssemblyFacts struct {
	Inputs        map[string]string          `json:"inputs"`
	TestedConfigs map[string]string          `json:"tested_configs"`
	Images        map[string]map[string]any  `json:"images"`
	SourceScan    scanning.SourceMeasurement `json:"source_scan"`
	Browser       browser.NativeFacts        `json:"browser"`
	Preferred     browser.PreferredFacts     `json:"preferred_sources"`
}

// ArtifactFoundation preserves the legacy public-input keys. Its unapproved
// structural checks become publication gates only after authentication/review.
// Large verified bytes remain in memory for higher checks, not another envelope.
type ArtifactFoundation struct {
	SchemaVersion                     int                                   `json:"schema_version"`
	Kind                              string                                `json:"kind"`
	Repository                        string                                `json:"repository"`
	Version                           string                                `json:"version"`
	SourceCommit                      string                                `json:"source_commit"`
	SourceKind                        string                                `json:"source_kind"`
	BindingSHA256                     string                                `json:"binding_sha256"`
	Assets                            map[string]string                     `json:"assets"`
	Subjects                          map[string]string                     `json:"subjects"`
	Inputs                            map[string]string                     `json:"inputs"`
	Native                            map[string]NativeAssemblyFacts        `json:"native"`
	TaggedSourceCIGateVerified        bool                                  `json:"tagged_source_ci_gate_verified"`
	SignerIdentityVerified            bool                                  `json:"signer_identity_verified"`
	PublicationAuthorized             bool                                  `json:"publication_authorized"`
	MeasurementAuthenticationRequired bool                                  `json:"measurement_authentication_required"`
	Manifest                          contract.ManifestV1                   `json:"-"`
	Application                       source.ApplicationVerification        `json:"-"`
	Common                            source.Inventory                      `json:"-"`
	CommonFiles                       map[string][]byte                     `json:"-"`
	Modules                           map[string]ModuleVerification         `json:"-"`
	Runtime                           map[string]source.RuntimeVerification `json:"-"`
	Browsers                          map[string]browser.NativeFacts        `json:"-"`
}

var releasePlatforms = []string{"linux/amd64", "linux/arm64"}

// Assemble prepares immutable local v1 deployment artifacts and source assets.
// It performs no scanner experiments, registry access, signing or publication.
// A release-inputs.json completion record is written only after every check.
func (b Builder) Assemble(ctx context.Context, o AssembleOptions) (ArtifactFoundation, error) {
	var result ArtifactFoundation
	identity, err := b.assemblyIdentity(ctx, o)
	if err != nil {
		return result, err
	}
	selected := source.Context{Repository: o.Repository, Version: identity.Version, Commit: identity.Commit}
	raw, err := readJSONFile(o.Candidate)
	if err != nil {
		return result, err
	}
	candidate, err := DecodeCandidate(raw)
	if err != nil || candidate.SourceCommit != selected.Commit || candidate.Version != selected.Version {
		return result, fmt.Errorf("assembled base candidate belongs to another source")
	}
	if o.CommonCollection == "" || o.Output == "" || len(o.Native) != 2 || strings.TrimSpace(o.MigrationNotes) == "" || strings.TrimSpace(o.RollbackNotes) == "" || len(o.MigrationNotes) > 8192 || len(o.RollbackNotes) > 8192 {
		return result, fmt.Errorf("assembly requires both complete native inputs, common source originals and deployment notes")
	}
	for _, platform := range releasePlatforms {
		n, ok := o.Native[platform]
		if !ok || n.BuildRecord == "" || n.Measurement == "" || n.RuntimeCollection == "" || n.ModuleCollection == "" || n.SourceScan == "" || n.BrowserCapture == "" || n.BrowserVerification == "" || len(n.Archives) != 2 || n.Archives["backend"] == "" || n.Archives["web"] == "" || n.Archives["backend"] == n.Archives["web"] {
			return result, fmt.Errorf("missing exact native assembly pair/evidence")
		}
	}
	if err := os.Mkdir(o.Output, 0700); err != nil {
		return result, fmt.Errorf("assembly output must be a new directory")
	}
	private, err := os.MkdirTemp(o.Output, ".assembly-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(private)
	result = ArtifactFoundation{SchemaVersion: 2, Kind: "prepared-release-inputs", Repository: selected.Repository, Version: selected.Version, SourceCommit: selected.Commit, SourceKind: identity.SourceKind, Assets: map[string]string{}, Subjects: map[string]string{}, Inputs: map[string]string{"candidate_bases": assemblyHash(raw)}, Native: map[string]NativeAssemblyFacts{}, Modules: map[string]ModuleVerification{}, Runtime: map[string]source.RuntimeVerification{}, Browsers: map[string]browser.NativeFacts{}, MeasurementAuthenticationRequired: true}
	if identity.PlannedCandidateOnly {
		result.Kind = "planned-candidate-inputs"
	}
	applicationName := "psst.zip-source-" + selected.Version + ".tar.gz"
	if o.ApplicationCollection == "" {
		result.Application, err = source.CreateApplication(ctx, b.Root, selected, filepath.Join(o.Output, applicationName))
	} else {
		input := o.ApplicationCollection
		if info, e := os.Stat(input); e == nil && info.IsDir() {
			input = filepath.Join(input, applicationName)
		}
		if filepath.Base(input) != applicationName {
			return result, fmt.Errorf("application offering name differs from exact source")
		}
		if _, err = retainAssemblyAsset(ctx, input, filepath.Join(o.Output, applicationName), source.ApplicationLimit); err != nil {
			return result, err
		}
		result.Application, err = source.ReadApplication(ctx, b.Root, selected, filepath.Join(o.Output, applicationName))
	}
	if err != nil {
		return result, err
	}
	result.Assets[applicationName] = result.Application.Asset.SHA256
	commonName := "psst.zip-upstream-inputs-" + selected.Version + ".tar.gz"
	commonSnapshot, commonAsset, err := snapshotCollection(ctx, o.CommonCollection, commonName, o.Output, filepath.Join(private, "common"))
	if err != nil {
		return result, err
	}
	result.Common, result.CommonFiles, err = source.ReadCommon(ctx, source.CommonOptions{Root: b.Root, Collection: commonSnapshot, Source: selected})
	if err != nil {
		return result, err
	}
	result.Assets[commonName] = commonAsset.SHA256
	result.Inputs["common_inventory"] = assemblyHash(result.CommonFiles["source-inventory.json"])
	images := map[string]oci.Image{}
	var tools map[string]string
	for _, platform := range releasePlatforms {
		native := o.Native[platform]
		arch := strings.TrimPrefix(platform, "linux/")
		build, buildRaw, observedTools, err := readAssemblyBuild(native.BuildRecord, candidate, platform)
		if err != nil {
			return result, err
		}
		if tools == nil {
			tools = observedTools
		} else if !reflect.DeepEqual(tools, observedTools) {
			return result, fmt.Errorf("native builders used different actual Go/Node versions")
		}
		measurement, measurementRaw, err := readNativeMeasurement(native.Measurement, selected, platform)
		if err != nil {
			return result, err
		}
		configs, err := measuredConfigs(measurement.Smoke)
		if err != nil {
			return result, err
		}
		moduleName := "psst.zip-dependency-inputs-" + selected.Version + "-" + arch + ".tar.gz"
		moduleSnapshot, moduleAsset, err := snapshotCollection(ctx, native.ModuleCollection, moduleName, o.Output, filepath.Join(private, "modules-"+arch))
		if err != nil {
			return result, err
		}
		moduleInventory, moduleFiles, err := source.ReadModules(ctx, source.ModuleOptions{Root: b.Root, Collection: moduleSnapshot, Source: selected, Platform: platform, GoImage: candidate.BaseImages["golang"]})
		if err != nil {
			return result, err
		}
		result.Modules[platform] = ModuleVerification{moduleInventory, moduleFiles}
		result.Assets[moduleName] = moduleAsset.SHA256
		scanRaw, err := readJSONFile(native.SourceScan)
		if err != nil {
			return result, err
		}
		scan, err := scanning.ReadSourceMeasurement(scanRaw, scanning.NativeIdentity{Source: scanning.Source{Repository: selected.Repository, Version: selected.Version, Commit: selected.Commit}, Platform: platform}, candidate.BaseImages["golang"], candidate.BaseImages["node"])
		if err != nil {
			return result, err
		}
		if err := checkScanApplication(scan, result.Application.Files); err != nil {
			return result, err
		}
		runtimeName := "psst.zip-" + selected.Version + "-runtime-sources-" + arch + ".tar.gz"
		runtimeSnapshot, runtimeAsset, err := snapshotCollection(ctx, native.RuntimeCollection, runtimeName, o.Output, filepath.Join(private, "runtime-"+arch))
		if err != nil {
			return result, err
		}
		runtimeOptions := source.RuntimeOptions{Root: b.Root, Collection: runtimeSnapshot, Source: selected, Platform: platform, Cosign: o.Cosign, BackendConfig: configs["backend"], WebConfig: configs["web"]}
		verified, err := source.VerifyRuntimeImages(ctx, runtimeOptions, native.Archives, configs)
		if err != nil {
			return result, err
		}
		if verified.Asset != runtimeAsset {
			return result, fmt.Errorf("verified runtime offering differs from retained source bytes")
		}
		if err := source.CheckRuntimeSmoke(measurement.Smoke, runtimeOptions, verified.InventoryFile.SHA256); err != nil {
			return result, err
		}
		if err := checkNativeCorrespondence(measurement, build, verified); err != nil {
			return result, err
		}
		result.Runtime[platform] = verified
		result.Assets[runtimeName] = runtimeAsset.SHA256
		browserRaw, err := readJSONFile(native.BrowserVerification)
		if err != nil {
			return result, err
		}
		var observed browser.NativeFacts
		if err := contract.Decode(browserRaw, maxJSON, &observed); err != nil {
			return result, err
		}
		var observedFields map[string]any
		if err := contract.Decode(browserRaw, maxJSON, &observedFields); err != nil {
			return result, err
		}
		captured, capture, err := browser.ReadCapture(native.BrowserCapture)
		if err != nil {
			return result, err
		}
		facts, err := browser.VerifyNativeFacts(ctx, browser.BindingOptions{Root: b.Root, Source: selected, Captured: captured, Common: result.Common, CommonFiles: result.CommonFiles, GitFiles: result.Application.Files}, verified, platform, observed.BuilderConfig)
		if err != nil {
			return result, err
		}
		facts.BrowserInputsSHA256 = capture.SHA256
		if !sameAssemblyJSON(facts, observedFields) {
			return result, fmt.Errorf("native browser measurement differs from shared original/image replay")
		}
		if len(facts.Verification.UnresolvedJavaScript) != 0 {
			return result, fmt.Errorf("native browser output has unresolved JavaScript sources")
		}
		preferred, err := browser.VerifyPreferred(facts.Verification.Binding, captured, result.Common, result.CommonFiles)
		if err != nil {
			return result, err
		}
		result.Browsers[platform] = facts
		measuredImages := map[string]map[string]any{}
		for _, component := range []string{"backend", "web"} {
			image := verified.Images[component]
			images[component+"-"+arch] = image
			measuredImages[component] = source.OCIMeasurement(image)
		}
		result.Native[platform] = NativeAssemblyFacts{Inputs: map[string]string{"build_record": assemblyHash(buildRaw), "measurement": assemblyHash(measurementRaw), "source_scan": assemblyHash(scanRaw), "browser_verification": assemblyHash(browserRaw), "browser_capture": capture.SHA256, "module_inventory": assemblyHash(moduleFiles["source-inventory.json"]), "runtime_inventory": verified.InventoryFile.SHA256}, TestedConfigs: configs, Images: measuredImages, SourceScan: scan, Browser: facts, Preferred: preferred}
	}
	indexRecords, indexBytes, err := AssembleIndexes(selected, images)
	if err != nil {
		return result, err
	}
	bundle, err := b.BuildBundle(ctx, BundleOptions{Output: o.Output, Version: selected.Version, Profile: "deployment-ready", ExpectedCommit: selected.Commit})
	if err != nil {
		return result, err
	}
	manifest := contract.ManifestV1{SchemaVersion: 1, Version: selected.Version, PayloadProfile: "deployment-ready", Source: contract.SourceV1{Repository: selected.Repository, Commit: selected.Commit, ArchiveURL: "https://github.com/" + selected.Repository + "/archive/" + selected.Commit + ".tar.gz"}, Platforms: append([]string(nil), releasePlatforms...), Images: indexRecords, Bundle: contract.BundleV1{Name: filepath.Base(bundle.Path), SHA256: bundle.SHA256}, Requirements: contract.RequirementsV1{Docker: "27.0.0", Compose: "2.24.4"}, Notes: contract.NotesV1{Migration: o.MigrationNotes, Rollback: o.RollbackNotes, CheckpointRequired: true}, Build: contract.BuildV1{BaseImages: candidate.BaseImages, Toolchains: tools}}
	if err := manifest.Validate(selected.Repository); err != nil {
		return result, err
	}
	manifestRaw, err := contract.JSON(manifest)
	if err != nil {
		return result, err
	}
	for _, component := range []string{"backend", "web"} {
		if err := writeRecord(filepath.Join(o.Output, component+"-index.json"), indexBytes[component]); err != nil {
			return result, err
		}
	}
	if err := writeRecord(filepath.Join(o.Output, "release-manifest.json"), manifestRaw); err != nil {
		return result, err
	}
	result.Manifest = manifest
	result.Assets["release-manifest.json"] = assemblyHash(manifestRaw)
	result.Assets[manifest.Bundle.Name] = "sha256:" + bundle.SHA256
	for _, component := range []string{"backend", "web"} {
		image := manifest.Images[component]
		result.Subjects[component+"-index"] = "oci://" + image.Index
		repository, _, _ := strings.Cut(image.Index, "@")
		for _, platform := range releasePlatforms {
			result.Subjects[component+"-"+strings.TrimPrefix(platform, "linux/")] = "oci://" + repository + "@" + image.PlatformDigests[platform]
		}
	}
	for name, digest := range result.Assets {
		key := "source:" + name
		if name == "release-manifest.json" {
			key = "manifest"
		} else if name == manifest.Bundle.Name {
			key = "bundle"
		}
		result.Subjects[key] = "file:" + name + "@" + digest
	}
	result.BindingSHA256, err = FoundationBinding(selected, result.Subjects)
	if err != nil {
		return result, err
	}
	if _, err := b.assemblyIdentity(ctx, o); err != nil {
		return result, err
	}
	foundation, err := contract.JSON(result)
	if err != nil {
		return result, err
	}
	if err := writeRecord(filepath.Join(o.Output, "release-inputs.json"), foundation); err != nil {
		return result, err
	}
	return result, nil
}

func (b Builder) assemblyIdentity(ctx context.Context, o AssembleOptions) (SourceIdentity, error) {
	var identity SourceIdentity
	var err error
	if o.PlannedVersion == "" {
		if o.EventName != "push" {
			return identity, fmt.Errorf("tag assembly requires its actual push event")
		}
		identity, err = b.ValidateTag(ctx, o.Ref, o.EventSHA)
	} else {
		if o.EventName != "workflow_dispatch" {
			return identity, fmt.Errorf("planned assembly requires main dispatch")
		}
		identity, err = b.ValidateSource(ctx, o.Ref, o.EventSHA, o.PlannedVersion)
	}
	if err != nil || identity.Commit != o.ReviewedCommit {
		return identity, fmt.Errorf("assembly source differs from exact reviewed trigger")
	}
	if err := (source.Context{Repository: o.Repository, Version: identity.Version, Commit: identity.Commit}).Validate(); err != nil {
		return identity, err
	}
	head, err := b.commit(ctx, "HEAD")
	if err != nil || head != identity.Commit {
		return identity, fmt.Errorf("assembly checkout is not reviewed source HEAD")
	}
	origin, err := b.git(ctx, "remote", "get-url", "origin")
	allowed := map[string]bool{"https://github.com/" + o.Repository: true, "https://github.com/" + o.Repository + ".git": true, "git@github.com:" + o.Repository: true, "git@github.com:" + o.Repository + ".git": true}
	if err != nil || !allowed[strings.TrimSpace(string(origin))] {
		return identity, fmt.Errorf("assembly checkout origin differs")
	}
	for _, cached := range []bool{false, true} {
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--name-only"}
		if cached {
			args = append(args, "--cached")
		}
		args = append(args, identity.Commit, "--")
		raw, err := b.git(ctx, args...)
		if err != nil || len(raw) != 0 {
			return identity, fmt.Errorf("tracked assembly checkout differs from reviewed source")
		}
	}
	return identity, nil
}

// AssembleIndexes preserves each verified raw child digest/media/size and creates
// only the two canonical multi-platform descriptor documents, never image blobs.
func AssembleIndexes(selected source.Context, images map[string]oci.Image) (map[string]contract.ImageV1, map[string][]byte, error) {
	if err := selected.Validate(); err != nil {
		return nil, nil, err
	}
	if len(images) != 4 {
		return nil, nil, fmt.Errorf("indexes require exactly four validated native images")
	}
	records, documents := map[string]contract.ImageV1{}, map[string][]byte{}
	seen := map[string]bool{}
	owner, _, _ := strings.Cut(selected.Repository, "/")
	for _, component := range []string{"backend", "web"} {
		var descriptors []any
		pair := map[string]string{}
		for _, platform := range releasePlatforms {
			arch := strings.TrimPrefix(platform, "linux/")
			image := images[component+"-"+arch]
			if image.Platform != platform || !digestRE.MatchString(image.ManifestDigest) || !digestRE.MatchString(image.ConfigDigest) || !digestRE.MatchString(image.ArchiveDigest) || image.ManifestSize <= 0 || image.ManifestSize > 1<<20 || (image.ManifestMediaType != oci.ManifestMediaType && image.ManifestMediaType != "application/vnd.docker.distribution.manifest.v2+json") || seen[image.ManifestDigest] || image.Labels["org.opencontainers.image.version"] != selected.Version || image.Labels["org.opencontainers.image.revision"] != selected.Commit || image.Labels["org.opencontainers.image.source"] != "https://github.com/"+selected.Repository {
				return nil, nil, fmt.Errorf("invalid or overlapping validated native child identity")
			}
			seen[image.ManifestDigest] = true
			pair[platform] = image.ManifestDigest
			descriptors = append(descriptors, map[string]any{"mediaType": image.ManifestMediaType, "digest": image.ManifestDigest, "size": image.ManifestSize, "platform": map[string]string{"os": "linux", "architecture": arch}})
		}
		raw, err := contract.JSON(map[string]any{"schemaVersion": 2, "mediaType": oci.IndexMediaType, "manifests": descriptors})
		if err != nil {
			return nil, nil, err
		}
		digest := assemblyHash(raw)
		if seen[digest] {
			return nil, nil, fmt.Errorf("overlapping release image subjects")
		}
		seen[digest] = true
		record := contract.ImageV1{Index: "ghcr.io/" + owner + "/psst-zip-" + component + "@" + digest, PlatformDigests: pair}
		if err := oci.ValidateIndex(raw, record); err != nil {
			return nil, nil, err
		}
		records[component], documents[component] = record, raw
	}
	return records, documents, nil
}

// FoundationBinding is byte-compatible with the legacy ASCII subject binding.
func FoundationBinding(selected source.Context, subjects map[string]string) (string, error) {
	if err := selected.Validate(); err != nil {
		return "", err
	}
	if len(subjects) < 9 || len(subjects) > 128 {
		return "", fmt.Errorf("incomplete foundation subjects")
	}
	for key, value := range subjects {
		if !regexp.MustCompile(`^[a-zA-Z0-9:._-]+$`).MatchString(key) || strings.ContainsAny(value, "\r\n\x00") || !regexp.MustCompile(`^[\x20-\x7e]+@sha256:[0-9a-f]{64}$`).MatchString(value) {
			return "", fmt.Errorf("invalid foundation subject")
		}
	}
	raw, err := contract.JSON(map[string]any{"repository": selected.Repository, "ref": "refs/tags/" + selected.Version, "commit": selected.Commit, "workflow": ".github/workflows/release.yml", "subjects": subjects})
	if err != nil {
		return "", err
	}
	return assemblyHash(raw), nil
}
