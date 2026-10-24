package evaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/containerbuild"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/publication"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
)

type RecoveryOptions struct {
	Binding                        github.Binding
	Prepared, SourceResult, Output string
	Native, Measurements           map[string]string
}
type RecoveryEvaluator struct {
	Root   string
	GitHub github.Client
	Now    func() time.Time
}
type recoveryInputs struct {
	foundation        containerbuild.ArtifactFoundation
	result            publication.Result
	manifest          contract.ManifestV1
	descriptors       map[string]containerbuild.NativeDescriptor
	descriptorDigests map[string]string
	snapshots         map[string]string
	sourceRaw         []byte
	bundle            string
}

// Recover reads only the authenticated source result, its exact small artifact
// foundation and the two completed native measurements. It never repeats source,
// OCI, Docker or recovery experiments and never authorizes public delivery.
func (e RecoveryEvaluator) Recover(ctx context.Context, o RecoveryOptions) (publication.Result, error) {
	var empty publication.Result
	if err := o.Binding.Validate(); err != nil {
		return empty, err
	}
	if len(o.Native) != 2 || len(o.Measurements) != 2 || o.Native["linux/amd64"] == "" || o.Native["linux/arm64"] == "" || o.Measurements["linux/amd64"] == "" || o.Measurements["linux/arm64"] == "" || o.Prepared == "" || o.SourceResult == "" || o.Output == "" {
		return empty, fmt.Errorf("recovery evaluation requires exact source, small prepared binding and both native measurements")
	}
	if _, err := os.Lstat(o.Output); !os.IsNotExist(err) {
		return empty, fmt.Errorf("completed recovery output already exists")
	}
	if err := realDirectory(filepath.Dir(o.Output)); err != nil {
		return empty, err
	}
	if err := realDirectory(e.Root); err != nil {
		return empty, err
	}
	client := e.GitHub
	if client.Runner == nil {
		client = github.New(e.Root)
	}
	cache := newCache(ctx, client, o.Binding)
	inputs, err := loadRecoveryInputs(ctx, cache, o)
	if err != nil {
		return empty, err
	}
	result := inputs.result
	if err := committedRecoveryInputs(ctx, e.Root, client, o.Binding.Commit, inputs.snapshots); err != nil {
		return empty, err
	}
	helperInputs := map[string]string{}
	for _, name := range recoveryFixtureFiles {
		helperInputs[name] = inputs.snapshots[filepath.Join(e.Root, filepath.FromSlash(name))]
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	measured, experiments, tools := map[string]any{}, map[string]any{}, map[string]any{}
	var images map[string]recoveryImage
	previous := ""
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		raw, err := cache.file(ctx, o.Measurements[platform])
		if err != nil {
			return empty, err
		}
		var record recoveryMeasurement
		if err = decodeComplete(raw, &record); err != nil {
			return empty, err
		}
		identity := scanning.NativeIdentity{Source: scanning.Source{Repository: o.Binding.Repository, Version: o.Binding.Version, Commit: o.Binding.Commit}, Platform: platform}
		descriptor := inputs.descriptors[platform]
		if record.SchemaVersion != 1 || record.Kind != "native-upgrade-recovery-measurement" || record.Source != identity || record.Execution != "native" || record.Manifest != inputs.snapshots[filepath.Join(o.Prepared, "release-manifest.json")] || record.Bundle != inputs.snapshots[inputs.bundle] || !reflect.DeepEqual(record.NativeDescriptors, inputs.descriptorDigests) || !reflect.DeepEqual(record.TestedConfigs, descriptor.TestedConfigs) || record.SavedPair != descriptor.Artifacts["final_archive"] || !reflect.DeepEqual(record.ExecutionInputs, helperInputs) || record.PublicProvenance || record.OffHostProvider || record.BrowserMobile || record.Publication || !record.GatePending || !record.Authentication {
			return empty, fmt.Errorf("terminal recovery identity, inputs or scope differ")
		}
		start, err := instant(record.Started)
		if err != nil {
			return empty, err
		}
		end, err := instant(record.Completed)
		if err != nil || start.After(end) || end.After(now) {
			return empty, fmt.Errorf("terminal recovery timestamps are inconsistent")
		}
		if !github.CommitPattern.MatchString(record.HistoricalCommit) || record.HistoricalCommit == o.Binding.Commit || previous != "" && previous != record.HistoricalCommit {
			return empty, fmt.Errorf("native recovery historical sources differ")
		}
		if previous == "" {
			if _, err := client.Command(ctx, 20*time.Second, "git", "merge-base", "--is-ancestor", record.HistoricalCommit, o.Binding.Commit); err != nil {
				return empty, fmt.Errorf("historical recovery source is not a retained ancestor")
			}
			previous = record.HistoricalCommit
		}
		if err := checkRecoveryImages(record.Images, inputs); err != nil {
			return empty, err
		}
		if images != nil && !reflect.DeepEqual(images, record.Images) {
			return empty, fmt.Errorf("native recovery final images differ")
		}
		images = record.Images
		if !toolsValid(record.Tools, platform) || len(record.Experiments) != 3 {
			return empty, fmt.Errorf("recovery native tool or experiment coverage is incomplete")
		}
		for _, profile := range recoveryExperimentProfiles() {
			body, ok := record.Experiments[profile.name]
			if !ok {
				return empty, fmt.Errorf("recovery experiment missing")
			}
			if err := checkRecoveryExperiment(body, identity, previous, descriptor.TestedConfigs, profile.failure, profile.paused, start, end); err != nil {
				return empty, err
			}
		}
		measured[platform] = map[string]any{"record_digest": digest(raw), "completed_at": record.Completed}
		experiments[platform] = record.Experiments
		tools[platform] = record.Tools
		result.Inputs["recovery-"+strings.TrimPrefix(platform, "linux/")] = publication.Input{SHA256: digest(raw)}
	}
	if err := cache.recheck(); err != nil {
		return empty, err
	}
	if err := recheckRecoveryInputs(ctx, inputs.snapshots); err != nil {
		return empty, err
	}
	details := map[string]any{"schema_version": 1, "execution": map[string]string{"linux/amd64": "native", "linux/arm64": "native"}, "checks": recoveryChecks, "native_measurements": measured, "manifest_sha256": inputs.snapshots[filepath.Join(o.Prepared, "release-manifest.json")], "bundle_sha256": inputs.snapshots[inputs.bundle], "native_descriptors": inputs.descriptorDigests, "historical_source_commit": previous, "execution_inputs": helperInputs, "images": images, "tools": tools, "experiments": experiments, "public_provenance_verified": false, "off_host_provider_verified": false, "browser_mobile_flows_verified": false, "publication_authorized": false}
	body, err := contract.JSON(details)
	if err != nil {
		return empty, err
	}
	result.Checks["upgrade-recovery"] = body
	result.Inputs["source-result"] = publication.Input{SHA256: digest(inputs.sourceRaw)}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		result.Platforms[platform] = publication.Platform{Execution: "native", Checks: []string{"final-image-smoke", "upgrade-recovery"}}
	}
	encoded, err := contract.JSON(result)
	if err != nil {
		return empty, err
	}
	result, err = publication.Decode(encoded)
	if err != nil {
		return empty, err
	}
	if _, err := writeNew(filepath.Dir(o.Output), filepath.Base(o.Output), result); err != nil {
		return empty, err
	}
	return result, nil
}

func loadRecoveryInputs(ctx context.Context, cache *authenticationCache, o RecoveryOptions) (recoveryInputs, error) {
	in := recoveryInputs{snapshots: map[string]string{}, descriptors: map[string]containerbuild.NativeDescriptor{}, descriptorDigests: map[string]string{}}
	if err := realDirectory(o.Prepared); err != nil {
		return in, err
	}
	raw, err := cache.file(ctx, o.SourceResult)
	if err != nil {
		return in, err
	}
	in.sourceRaw = raw
	in.result, err = publication.Decode(raw)
	if err != nil || in.result.Repository != o.Binding.Repository || in.result.Version != o.Binding.Version || in.result.Commit != o.Binding.Commit || in.result.RunID != o.Binding.RunID || in.result.Attempt != o.Binding.Attempt || len(in.result.Checks) != 6 {
		return in, fmt.Errorf("recovery requires the authenticated six-check source result for this exact invocation")
	}
	name := filepath.Join(o.Prepared, "release-inputs.json")
	raw, err = read(name)
	if err != nil || in.result.Inputs["artifact-foundation"].SHA256 != digest(raw) {
		return in, fmt.Errorf("prepared recovery foundation differs from authenticated source result")
	}
	in.snapshots[name] = digest(raw)
	if err = contract.Decode(raw, jsonLimit, &in.foundation); err != nil {
		return in, err
	}
	f := in.foundation
	if f.SchemaVersion != 2 || f.Kind != "prepared-release-inputs" || f.SourceKind != "version-tag" || f.Repository != o.Binding.Repository || f.Version != o.Binding.Version || f.SourceCommit != o.Binding.Commit || f.BindingSHA256 != in.result.Binding || f.TaggedSourceCIGateVerified || f.SignerIdentityVerified || f.PublicationAuthorized || !f.MeasurementAuthenticationRequired || !reflect.DeepEqual(f.Subjects, in.result.Subjects) || len(f.Native) != 2 {
		return in, fmt.Errorf("current recovery artifact foundation context differs")
	}
	manifestPath := filepath.Join(o.Prepared, "release-manifest.json")
	raw, err = read(manifestPath)
	if err != nil {
		return in, err
	}
	in.snapshots[manifestPath] = digest(raw)
	in.manifest, err = contract.DecodeManifestV1(raw, o.Binding.Repository)
	if err != nil || in.manifest.Version != o.Binding.Version || in.manifest.Source.Commit != o.Binding.Commit || in.manifest.PayloadProfile != "deployment-ready" || f.Assets["release-manifest.json"] != digest(raw) || in.result.Subjects["manifest"] != "file:release-manifest.json@"+digest(raw) {
		return in, fmt.Errorf("recovery manifest differs from exact source subjects")
	}
	in.bundle = filepath.Join(o.Prepared, in.manifest.Bundle.Name)
	bundleHash, err := hashRecoveryFile(ctx, in.bundle, 2<<30)
	if err != nil || bundleHash != "sha256:"+in.manifest.Bundle.SHA256 || f.Assets[in.manifest.Bundle.Name] != bundleHash || in.result.Subjects["bundle"] != "file:"+in.manifest.Bundle.Name+"@"+bundleHash {
		return in, fmt.Errorf("recovery bundle differs from exact source subjects")
	}
	in.snapshots[in.bundle] = bundleHash
	entries, err := os.ReadDir(o.Prepared)
	if err != nil || len(entries) != 3 {
		return in, fmt.Errorf("small recovery binding file set differs")
	}
	for _, entry := range entries {
		if entry.Name() != "release-inputs.json" && entry.Name() != "release-manifest.json" && entry.Name() != in.manifest.Bundle.Name {
			return in, fmt.Errorf("unexpected small recovery binding file")
		}
	}
	if err := checkRecoverySourceFacts(in.result, f, in.manifest); err != nil {
		return in, err
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		if err := realDirectory(o.Native[platform]); err != nil {
			return in, err
		}
		name := filepath.Join(o.Native[platform], "native/native-artifacts.json")
		raw, err := cache.file(ctx, name)
		if err != nil {
			return in, err
		}
		arch := strings.TrimPrefix(platform, "linux/")
		if in.result.Inputs["native-descriptor-"+arch].SHA256 != digest(raw) {
			return in, fmt.Errorf("recovery native descriptor differs from authenticated parent")
		}
		var descriptor containerbuild.NativeDescriptor
		if err := decodeComplete(raw, &descriptor); err != nil {
			return in, err
		}
		identity := scanning.NativeIdentity{Source: scanning.Source{Repository: o.Binding.Repository, Version: o.Binding.Version, Commit: o.Binding.Commit}, Platform: platform}
		if descriptor.SchemaVersion != 2 || descriptor.Kind != "native-release-artifacts" || descriptor.Source != identity || descriptor.OCIExporter != "docker-save-byte-preserving-oci-v1" || descriptor.PublicationAuthorized || !descriptor.MeasurementAuthenticationRequired || !pairValid(descriptor.TestedConfigs) || !pairValid(descriptor.OriginalTestedConfigs) || descriptor.TestedConfigs["backend"] == descriptor.TestedConfigs["web"] || descriptor.OriginalTestedConfigs["backend"] == descriptor.OriginalTestedConfigs["web"] || !recoveryDigest.MatchString(descriptor.SourceHelperConfig) || !reflect.DeepEqual(descriptor.TestedConfigs, f.Native[platform].TestedConfigs) {
			return in, fmt.Errorf("recovery native descriptor context or configurations differ")
		}
		for _, artifact := range descriptor.Artifacts {
			if !safeRecoveryArtifact(artifact) {
				return in, fmt.Errorf("unsafe native recovery artifact metadata")
			}
		}
		requiredArtifacts := []string{"build_record", "original_archive", "final_archive", "native_measurement", "smoke_report", "source_verification", "runtime_pack", "runtime_source", "backend_archive", "web_archive", "browser_inputs", "browser_verification"}
		if len(descriptor.Artifacts) != len(requiredArtifacts) {
			return in, fmt.Errorf("native recovery descriptor artifact set differs")
		}
		for _, key := range requiredArtifacts {
			if descriptor.Artifacts[key].File == "" {
				return in, fmt.Errorf("native recovery descriptor lacks required retained artifact")
			}
		}
		if descriptor.Artifacts["runtime_pack"].SHA256 != f.Native[platform].Inputs["runtime_inventory"] {
			return in, fmt.Errorf("recovery runtime inventory differs from authenticated foundation")
		}
		in.descriptors[platform] = descriptor
		in.descriptorDigests[platform] = digest(raw)
	}
	return in, nil
}

func committedRecoveryInputs(ctx context.Context, root string, client github.Client, commit string, snapshots map[string]string) error {
	names := append(append([]string{}, recoveryFixtureFiles...), "deploy/update.py")
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		raw, err := read(path)
		if err != nil {
			return err
		}
		original, err := client.Command(ctx, 20*time.Second, "git", "show", commit+":"+name)
		if err != nil || !bytes.Equal(raw, original) {
			return fmt.Errorf("recovery fixture differs from exact committed candidate")
		}
		snapshots[path] = digest(raw)
	}
	return nil
}
func recheckRecoveryInputs(ctx context.Context, snapshots map[string]string) error {
	for name, want := range snapshots {
		got, err := hashRecoveryFile(ctx, name, 2<<30)
		if err != nil || got != want {
			return fmt.Errorf("recovery binding inputs changed during authentication")
		}
	}
	return nil
}
func realDirectory(name string) error {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	for {
		info, err := os.Lstat(absolute)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("recovery requires a real directory tree")
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			break
		}
		absolute = parent
	}
	return nil
}
func hashRecoveryFile(ctx context.Context, name string, limit int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return "", fmt.Errorf("recovery input must be a bounded regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", fmt.Errorf("recovery file changed before hashing")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(recoveryContextReader{ctx, file}, limit+1))
	after, statErr := file.Stat()
	pathAfter, pathErr := os.Lstat(name)
	if err != nil || statErr != nil || pathErr != nil || n != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || !os.SameFile(info, pathAfter) {
		return "", fmt.Errorf("recovery file changed or exceeds bounds")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

type recoveryContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r recoveryContextReader) Read(raw []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(raw)
}
