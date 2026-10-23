package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/containerbuild"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/publication"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

type Options struct {
	Binding                                                                                              github.Binding
	EventSHA, Candidate, CommonCollection, Prepared, Output, CaddyGoImage, MigrationNotes, RollbackNotes string
	Native                                                                                               map[string]string
}
type Evaluator struct {
	Root   string
	GitHub github.Client
	// Tests may observe the trust boundary without fabricating completed source
	// facts. Normal commands always select the real shared assembler/verifier.
	Assemble       func(context.Context, containerbuild.AssembleOptions) (containerbuild.ArtifactFoundation, error)
	VerifyTransfer transferVerifier
}

// Evaluate authenticates current tagged inputs before touching their claims,
// performs shared source/image/browser replay once and writes one source result.
// It never signs, publishes, deploys or creates additional gate envelope files.
func (e Evaluator) Evaluate(ctx context.Context, o Options) (publication.Result, error) {
	var result publication.Result
	if err := o.Binding.Validate(); err != nil {
		return result, err
	}
	if len(o.Native) != 2 || o.Native["linux/amd64"] == "" || o.Native["linux/arm64"] == "" || o.Candidate == "" || o.CommonCollection == "" || o.Prepared == "" || o.Output == "" || o.CaddyGoImage == "" || !github.CommitPattern.MatchString(o.EventSHA) {
		return result, fmt.Errorf("evaluation requires exact current tag/attempt, both complete native trees and new prepared/output directories")
	}
	if _, err := os.Lstat(o.Output); !os.IsNotExist(err) {
		return result, fmt.Errorf("evaluation output already exists")
	}
	client := e.GitHub
	if client.Runner == nil {
		client = github.New(e.Root)
	}
	cache := newCache(ctx, client, o.Binding)
	remote, err := client.ResolveTag(ctx, o.Binding.Repository, o.Binding.Version)
	if err != nil || remote != o.Binding.Commit {
		return result, fmt.Errorf("evaluated remote version tag differs from reviewed source")
	}
	ci, err := client.ContainerCI(ctx, o.Binding)
	if err != nil {
		return result, err
	}
	bases, err := cache.file(ctx, o.Candidate)
	if err != nil {
		return result, err
	}
	candidate, err := containerbuild.DecodeCandidate(bases)
	if err != nil || candidate.SourceCommit != o.Binding.Commit || candidate.Version != o.Binding.Version {
		return result, fmt.Errorf("authenticated base resolution differs from reviewed source")
	}
	verify := e.VerifyTransfer
	if verify == nil {
		verify = containerbuild.VerifyTransfer
	}
	native := map[string]nativeInput{}
	assemblyInputs := map[string]containerbuild.NativeAssemblyInput{}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		input, err := authenticateNative(ctx, cache, verify, o.Binding, platform, o.Native[platform])
		if err != nil {
			return result, err
		}
		native[platform] = input
		assemblyInputs[platform] = input.Assembly
	}
	assemble := e.Assemble
	if assemble == nil {
		assemble = containerbuild.New(e.Root).Assemble
	}
	foundation, err := assemble(ctx, containerbuild.AssembleOptions{Repository: o.Binding.Repository, Ref: "refs/tags/" + o.Binding.Version, EventSHA: o.EventSHA, ReviewedCommit: o.Binding.Commit, EventName: "push", Candidate: o.Candidate, CommonCollection: o.CommonCollection, Output: o.Prepared, MigrationNotes: o.MigrationNotes, RollbackNotes: o.RollbackNotes, Native: assemblyInputs})
	if err != nil {
		return result, err
	}
	if foundation.SchemaVersion != 2 || foundation.Kind != "prepared-release-inputs" || foundation.SourceKind != "version-tag" || foundation.Repository != o.Binding.Repository || foundation.Version != o.Binding.Version || foundation.SourceCommit != o.Binding.Commit || foundation.PublicationAuthorized || !foundation.MeasurementAuthenticationRequired || foundation.Inputs["candidate_bases"] != digest(bases) {
		return result, fmt.Errorf("assembled artifact foundation differs from authenticated source/bases")
	}
	if err = checkFoundationNative(foundation, native); err != nil {
		return result, err
	}
	corresponding, evidence, err := correspondingSource(ctx, e.Root, foundation)
	if err != nil {
		return result, err
	}
	sourceInputs := map[string]scanning.SourceEvidence{}
	for platform, input := range native {
		sourceInputs[platform] = scanning.SourceEvidence{Measurement: input.Measurements["source-scans/source-scan-measurement.json"], Directory: filepath.Join(input.Tree, "source-scans")}
	}
	sourceScans, err := scanning.AggregateSources(ctx, scanning.SourceAggregationOptions{Root: e.Root, Binding: o.Binding, Subjects: foundation.Subjects, Sources: sourceInputs, Application: foundation.Application, GoImage: candidate.BaseImages["golang"], NodeImage: candidate.BaseImages["node"], ResolvedBases: bases, Authenticate: cache.bytes})
	if err != nil {
		return result, err
	}
	images, err := imageInputs(foundation, native, candidate.BaseImages["golang"], o.CaddyGoImage)
	if err != nil {
		return result, err
	}
	imageScans, err := scanning.AggregateImages(ctx, scanning.ImageAggregationOptions{Root: e.Root, Binding: o.Binding, Subjects: foundation.Subjects, Images: images, ResolvedBases: bases, Authenticate: cache.bytes})
	if err != nil {
		return result, err
	}
	smoke, notices, err := smokeDetails(foundation, native)
	if err != nil {
		return result, err
	}
	if err = cache.recheck(); err != nil {
		return result, err
	}
	remote, err = client.ResolveTag(ctx, o.Binding.Repository, o.Binding.Version)
	if err != nil || remote != o.Binding.Commit {
		return result, fmt.Errorf("remote source tag changed during source evaluation")
	}
	if err = os.Mkdir(o.Output, 0700); err != nil {
		return result, err
	}
	evidenceDigest, err := writeNew(o.Output, "corresponding-source-evidence.json", evidence)
	if err != nil {
		return result, err
	}
	checks := map[string]json.RawMessage{}
	for name, value := range map[string]any{"corresponding-source": corresponding, "source-ci": ci, "source-scanners": sourceScans, "final-image-scanners": imageScans, "final-image-smoke": smoke, "runtime-notices": notices} {
		raw, err := contract.JSON(value)
		if err != nil {
			return result, err
		}
		checks[name] = raw
	}
	inputFacts := map[string]publication.Input{"corresponding-source-replay": {SHA256: evidenceDigest}, "candidate-bases": {SHA256: digest(bases)}}
	for platform, input := range native {
		arch := strings.TrimPrefix(platform, "linux/")
		inputFacts["native-transfer-"+arch] = publication.Input{SHA256: digest(input.Transfer.MetadataRaw)}
		inputFacts["native-descriptor-"+arch] = publication.Input{SHA256: digest(input.Transfer.DescriptorRaw)}
		for name, raw := range input.Measurements {
			label := strings.NewReplacer("/", "-", ".json", "").Replace(name)
			inputFacts[arch+"-"+label] = publication.Input{SHA256: digest(raw)}
		}
	}
	result, err = publication.NewSourceResult(publication.SourceResultOptions{Repository: o.Binding.Repository, Version: o.Binding.Version, Commit: o.Binding.Commit, RunID: o.Binding.RunID, Attempt: o.Binding.Attempt, Binding: foundation.BindingSHA256, Subjects: foundation.Subjects, Checks: checks, Inputs: inputFacts})
	if err != nil {
		return result, err
	}
	if _, err = writeNew(o.Output, "release-result.json", result); err != nil {
		return result, err
	}
	return result, nil
}

func imageInputs(f containerbuild.ArtifactFoundation, native map[string]nativeInput, goImage, caddyImage string) (map[string]scanning.ImageEvidence, error) {
	result := map[string]scanning.ImageEvidence{}
	type vendor struct{ sum, modules []byte }
	vendors := map[string]vendor{}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		input := native[platform]
		runtime := f.Runtime[platform]
		var caddy source.Source
		for _, item := range runtime.Inventory.Sources {
			if item.Ecosystem == "caddy" {
				if caddy.ID != "" {
					return nil, fmt.Errorf("ambiguous original Caddy source")
				}
				caddy = item
			}
		}
		if caddy.ID == "" {
			return nil, fmt.Errorf("original Caddy source missing")
		}
		v, ok := vendors[caddy.Original.SHA256]
		if !ok {
			sum, modules, err := source.CaddyCompilerModules(runtime.SourceFiles[caddy.Original.Path])
			if err != nil {
				return nil, err
			}
			v = vendor{sum, modules}
			vendors[caddy.Original.SHA256] = v
		}
		for _, component := range []string{"backend", "web"} {
			metadata := runtime.Images[component]
			target := component + "-" + strings.TrimPrefix(platform, "linux/")
			builder := goImage
			if component == "web" {
				builder = caddyImage
			}
			expected := scanning.CompilerExpected{Source: scanning.NativeIdentity{Source: scanning.Source{Repository: f.Repository, Version: f.Version, Commit: f.SourceCommit}, Platform: platform}, Component: component, Image: scanning.ImageIdentity{Archive: metadata.ArchiveDigest, Manifest: metadata.ManifestDigest, Config: metadata.ConfigDigest, Platform: metadata.Platform}, SourceSHA256: runtime.Asset.SHA256, GoImage: builder, Binary: runtime.Files[component][compilerPath(component)], GoPolicy: runtime.SourceFiles["inputs/tools/go-runtime-sources.json"], VendorSum: v.sum, VendorModules: v.modules, Caddy: caddy, Signatures: runtime.Inventory.Signatures}
			report, err := read(filepath.Join(input.Tree, "image-scan-"+component, "scan.json"))
			if err != nil {
				return nil, err
			}
			result[target] = scanning.ImageEvidence{Measurement: input.Measurements["image-scan-"+component+"/measurement.json"], Report: report, CompilerDirectory: filepath.Join(input.Tree, "compiler-"+component), Expected: expected}
		}
	}
	return result, nil
}
func compilerPath(component string) string {
	if component == "backend" {
		return "app/server"
	}
	return "usr/bin/caddy"
}
