package evaluation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/containerbuild"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/deployment"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/publication"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

const recoveryStart = "2020-01-01T00:00:00Z"
const recoveryObserved = "2020-01-01T00:01:00Z"
const recoveryEnd = "2020-01-01T00:02:00Z"

func recoveryJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := contract.JSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func recoveryWrite(t *testing.T, name string, value any) []byte {
	t.Helper()
	raw := recoveryJSON(t, value)
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return raw
}
func recoveryObject(t *testing.T, value any) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(recoveryJSON(t, value), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func recoveryExperimentFixture(t *testing.T, identity scanning.NativeIdentity, previous string, configs map[string]string, failure, paused bool) recoveryExperiment {
	t.Helper()
	first, last := "20200101T000001Z-"+strings.Repeat("a", 12), "20200101T000002Z-"+strings.Repeat("b", 12)
	stages := []string{"candidate-activation", "repeat-activation", "isolated-restore-activation"}
	if failure {
		stages = []string{"post-startup-failure", "isolated-restore-activation"}
		last = first
	}
	e := recoveryExperiment{SchemaVersion: 1, Kind: "disposable-updater-experiment", Scenario: "normal", Candidate: recoveryVersion{identity.Version, identity.Commit, configs}, Baseline: recoveryVersion{"v0.0.0", previous, map[string]string{"backend": digest([]byte("old-backend")), "web": digest([]byte("old-web"))}}, PriorPause: paused, RepeatMode: "same-exact-candidate", Schema: recoverySchema{10, 11, 11, true}, Completed: recoveryEnd, Acquisition: "fixture-local-exact-loaded-configs", BackupProvider: "same-host-separated-store-encryption-simulation", Tools: recoveryTools{"29.1.0", "v2.45.0", "amd64"}}
	if identity.Platform == "linux/arm64" {
		e.Tools.Architecture = "arm64"
	}
	if failure {
		e.Scenario = "post-startup-failure"
		e.Schema.Restored = 10
	}
	for i, stage := range stages {
		transaction := last
		if i == 0 {
			transaction = first
		}
		restoring, failed := stage == "isolated-restore-activation", stage == "post-startup-failure"
		version, commit := identity.Version, identity.Commit
		if restoring {
			commit = "checkpoint:" + transaction
			if failure {
				version = "v0.0.0"
			}
		}
		flow := recoveryFlow{Checks: map[string]string{}, Observed: recoveryObserved, Version: version, Commit: commit}
		for _, name := range deployment.FullFlowCheckNames(restoring) {
			flow.Checks[name] = `{"fixture_status":200}`
		}
		previousVersion := "v0.0.0"
		observation := recoveryObservation{Stage: stage, Transaction: transaction, Version: identity.Version, Active: &version, Previous: &previousVersion, Phase: "completed", Mutation: true, PriorPause: paused, Restoring: restoring, Verification: recoveryJSON(t, flow), PublicIngress: recoveryJSON(t, map[string]any{"https_port": 18443, "config_status": 200, "public_transfers_paused": paused})}
		if failed {
			observation.Active = nil
			observation.Phase = "failed-closed"
			observation.Verification = json.RawMessage("null")
			observation.PublicIngress = json.RawMessage(`{"running_services":0}`)
		}
		e.Observations = append(e.Observations, observation)
		e.Startups = append(e.Startups, recoveryStartup{transaction, restoring, recoveryObserved})
	}
	e.Checkpoint.SHA256 = strings.Repeat("c", 64)
	e.Checkpoint.Preserved = e.Checkpoint.SHA256
	e.Checkpoint.CorruptRefused = true
	e.Checkpoint.Records.Image = 2
	e.Checkpoint.Records.Volume = 3
	e.Checkpoint.Records.Configuration = 3
	e.Checkpoint.Receipt = recoveryReceipt{e.Checkpoint.SHA256, strings.Repeat("receipt", 12), strings.Repeat("exercise", 12), recoveryObserved}
	e.Restore = recoveryRestore{Mapping: map[string]string{"old-data": "new-data", "old-tls": "new-tls", "old-config": "new-config"}, OriginalVolumes: map[string]string{"backend:/app/data": "old-data", "caddy:/data": "old-tls", "caddy:/config": "old-config"}, Payload: strings.Repeat("d", 64), PreservedPayload: strings.Repeat("d", 64), Certificate: strings.Repeat("e", 64), RestoredCertificate: strings.Repeat("e", 64)}
	return e
}

func TestRecoveryExperimentDerivesRealScopeAndRejectsUnsafeObservations(t *testing.T) {
	identity := scanning.NativeIdentity{Source: scanning.Source{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}, Platform: "linux/amd64"}
	previous := strings.Repeat("b", 40)
	configs := map[string]string{"backend": digest([]byte("backend")), "web": digest([]byte("web"))}
	lower, _ := instant(recoveryStart)
	upper, _ := instant(recoveryEnd)
	for _, profile := range recoveryExperimentProfiles() {
		t.Run(profile.name, func(t *testing.T) {
			e := recoveryExperimentFixture(t, identity, previous, configs, profile.failure, profile.paused)
			if err := checkRecoveryExperiment(recoveryJSON(t, e), identity, previous, configs, profile.failure, profile.paused, lower, upper); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, mutate := range map[string]func(*recoveryExperiment){
		"missing-flow": func(e *recoveryExperiment) {
			var flow recoveryFlow
			json.Unmarshal(e.Observations[0].Verification, &flow)
			delete(flow.Checks, "client_authenticated_decryption")
			e.Observations[0].Verification = recoveryJSON(t, flow)
		},
		"changed-restore-volume":      func(e *recoveryExperiment) { e.Restore.Mapping["old-data"] = "old-data" },
		"corrupt-checkpoint-accepted": func(e *recoveryExperiment) { e.Checkpoint.CorruptRefused = false },
		"missing-startup":             func(e *recoveryExperiment) { e.Startups = nil },
		"late-receipt":                func(e *recoveryExperiment) { e.Checkpoint.Receipt.Verified = "2020-01-02T00:00:00Z" },
		"old-cli-was-not-tried":       func(e *recoveryExperiment) { e.Schema.OldRejected = false },
		"different-native-tools":      func(e *recoveryExperiment) { e.Tools.Architecture = "arm64" },
		"public-proof-overclaim":      func(e *recoveryExperiment) { e.PublicProvenance = true },
	} {
		t.Run(name, func(t *testing.T) {
			e := recoveryExperimentFixture(t, identity, previous, configs, false, false)
			mutate(&e)
			if err := checkRecoveryExperiment(recoveryJSON(t, e), identity, previous, configs, false, false, lower, upper); err == nil {
				t.Fatal("unsafe recovery observation accepted")
			}
		})
	}
	e := recoveryExperimentFixture(t, identity, previous, configs, true, false)
	e.Observations[0].PublicIngress = json.RawMessage(`{"running_services":1}`)
	if checkRecoveryExperiment(recoveryJSON(t, e), identity, previous, configs, true, false, lower, upper) == nil {
		t.Fatal("failed candidate left public ingress running")
	}
	value := recoveryObject(t, recoveryExperimentFixture(t, identity, previous, configs, false, false))
	delete(value, "publication_authorized")
	if checkRecoveryExperiment(recoveryJSON(t, value), identity, previous, configs, false, false, lower, upper) == nil {
		t.Fatal("absent false scope flag was defaulted")
	}
}

type recoveryExecutor struct {
	t            *testing.T
	binding      github.Binding
	files        map[string][]byte
	accepted     map[string]bool
	wrongAttempt bool
}

func (e *recoveryExecutor) Output(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
	if len(args) > 3 && args[0] == "gh" && args[1] == "attestation" && args[2] == "verify" {
		raw, err := os.ReadFile(args[3])
		if err != nil {
			return nil, err
		}
		sha := digest(raw)
		if !e.accepted[sha] {
			return nil, fmt.Errorf("fixture unsigned bytes")
		}
		b := e.binding
		if e.wrongAttempt {
			b.Attempt++
		}
		repo := "https://github.com/" + b.Repository
		ref := "refs/tags/" + b.Version
		return recoveryJSON(e.t, []any{map[string]any{"verificationResult": map[string]any{"signature": map[string]any{"certificate": map[string]string{"identity": "fixture verified by gh"}}, "verifiedTimestamps": []any{map[string]string{"type": "tlog"}}, "statement": map[string]any{"_type": "https://in-toto.io/Statement/v1", "predicateType": github.Predicate, "subject": []any{map[string]any{"name": "fixture.json", "digest": map[string]string{"sha256": strings.TrimPrefix(sha, "sha256:")}}}, "predicate": map[string]any{"buildDefinition": map[string]any{"buildType": "https://actions.github.io/buildtypes/workflow/v1", "externalParameters": map[string]any{"workflow": map[string]string{"repository": repo, "ref": ref, "path": github.ReleaseWorkflow}}, "internalParameters": map[string]any{"github": map[string]string{"event_name": "push", "runner_environment": "github-hosted"}}, "resolvedDependencies": []any{map[string]any{"uri": "git+" + repo + "@" + ref, "digest": map[string]string{"gitCommit": b.Commit}}}}, "runDetails": map[string]any{"builder": map[string]string{"id": repo + "/" + github.ReleaseWorkflow + "@" + ref}, "metadata": map[string]string{"invocationId": fmt.Sprintf("%s/actions/runs/%d/attempts/%d", repo, b.RunID, b.Attempt)}}}}}}}), nil
	}
	if len(args) == 3 && args[0] == "git" && args[1] == "show" {
		_, name, ok := strings.Cut(args[2], ":")
		if ok && e.files[name] != nil {
			return e.files[name], nil
		}
	}
	if len(args) == 5 && args[0] == "git" && args[1] == "merge-base" && args[2] == "--is-ancestor" {
		return nil, nil
	}
	if len(args) > 1 && args[0] == "git" {
		switch args[1] {
		case "remote":
			return []byte("https://github.com/" + e.binding.Repository + ".git"), nil
		case "rev-parse":
			return []byte(e.binding.Commit), nil
		case "diff":
			return nil, nil
		}
	}
	if len(args) > 1 && args[0] == "gh" && args[1] == "api" {
		for i, arg := range args[:len(args)-1] {
			if arg == "--method" && args[i+1] != "GET" {
				return nil, fmt.Errorf("remote mutation disabled in composite fixture")
			}
		}
		endpoint := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "repos/") {
				endpoint = arg
			}
		}
		if strings.Contains(endpoint, "/actions/runs/") {
			return recoveryJSON(e.t, map[string]any{"id": e.binding.RunID, "run_attempt": e.binding.Attempt, "status": "in_progress", "event": "push", "head_sha": e.binding.Commit, "path": github.ReleaseWorkflow, "repository": map[string]string{"full_name": e.binding.Repository}, "head_repository": map[string]string{"full_name": e.binding.Repository}}), nil
		}
		if strings.Contains(endpoint, "/contents/") {
			return recoveryJSON(e.t, map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(e.files[github.ReleaseWorkflow])}), nil
		}
	}
	return nil, fmt.Errorf("unexpected fixture operation")
}

func recoveryAggregationFixture(t *testing.T) (RecoveryEvaluator, RecoveryOptions, *recoveryExecutor) {
	t.Helper()
	root := t.TempDir()
	binding := github.Binding{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40), RunID: 123, Attempt: 2}
	o := RecoveryOptions{Binding: binding, Prepared: filepath.Join(root, "prepared"), SourceResult: filepath.Join(root, "source-result.json"), Output: filepath.Join(root, "complete-result.json"), Native: map[string]string{}, Measurements: map[string]string{}}
	executor := &recoveryExecutor{t: t, binding: binding, accepted: map[string]bool{}, files: map[string][]byte{}}
	executor.files[github.ReleaseWorkflow] = []byte("concurrency:\n  group: container-release-publication\n  cancel-in-progress: false\njobs:\n  publish:\n")
	for _, name := range append(append([]string{}, recoveryFixtureFiles...), "deploy/update.py") {
		body := []byte("committed disposable helper " + name)
		executor.files[name] = body
		path := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, body, 0600)
	}
	sha := func(value string) string { return digest([]byte(value)) }
	bundle := recoveryBundle(t, binding)
	manifest := contract.ManifestV1{SchemaVersion: 1, Version: binding.Version, PayloadProfile: "deployment-ready", Source: contract.SourceV1{Repository: binding.Repository, Commit: binding.Commit, ArchiveURL: "https://github.com/" + binding.Repository + "/archive/" + binding.Commit + ".tar.gz"}, Platforms: []string{"linux/amd64", "linux/arm64"}, Images: map[string]contract.ImageV1{}, Bundle: contract.BundleV1{Name: "psst.zip-deployment-" + binding.Version + ".tar.gz", SHA256: strings.TrimPrefix(digest(bundle), "sha256:")}, Requirements: contract.RequirementsV1{Docker: "27.0.0", Compose: "2.24.4"}, Notes: contract.NotesV1{Migration: "checkpoint before activation", Rollback: "isolated checkpoint restore", CheckpointRequired: true}, Build: contract.BuildV1{BaseImages: map[string]string{}, Toolchains: map[string]string{"go": "go1.27.3", "node": "26.12.1"}}}
	for _, base := range []string{"golang", "node", "alpine", "caddy"} {
		manifest.Build.BaseImages[base] = "docker.io/library/" + base + "@" + sha(base)
	}
	actualImages := map[string]recoveryImage{}
	for _, component := range []string{"backend", "web"} {
		image := contract.ImageV1{PlatformDigests: map[string]string{}}
		children := []any{}
		for _, arch := range []string{"amd64", "arm64"} {
			actual, child := recoveryNativeImage(t, root, binding, component, arch)
			actualImages[component+"-"+arch] = actual
			image.PlatformDigests["linux/"+arch] = actual.Manifest
			children = append(children, child)
		}
		index := recoveryWrite(t, filepath.Join(root, "indexes", component+"-index.json"), map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": children})
		image.Index = "ghcr.io/endorses/psst-zip-" + component + "@" + digest(index)
		manifest.Images[component] = image
	}
	manifestRaw := recoveryWrite(t, filepath.Join(o.Prepared, "release-manifest.json"), manifest)
	os.WriteFile(filepath.Join(o.Prepared, manifest.Bundle.Name), bundle, 0600)
	f := containerbuild.ArtifactFoundation{SchemaVersion: 2, Kind: "prepared-release-inputs", Repository: binding.Repository, Version: binding.Version, SourceCommit: binding.Commit, SourceKind: "version-tag", Assets: map[string]string{"release-manifest.json": digest(manifestRaw), manifest.Bundle.Name: digest(bundle)}, Subjects: map[string]string{"manifest": "file:release-manifest.json@" + digest(manifestRaw), "bundle": "file:" + manifest.Bundle.Name + "@" + digest(bundle)}, Native: map[string]containerbuild.NativeAssemblyFacts{}, MeasurementAuthenticationRequired: true}
	details := recoverySourceDetails{Schema: 2, Subjects: map[string]string{}, Coverage: map[string]recoveryCoverage{}}
	details.Images = map[string]struct {
		Subject string `json:"subject"`
		Notices string `json:"notice_inventory_digest"`
	}{}
	details.Policy.Path = "tools/container-distribution-policy.json"
	details.Policy.Commit = binding.Commit
	details.Policy.Digest = sha("policy")
	details.Policy.Blob = strings.Repeat("c", 40)
	add := func(name string) {
		f.Assets[name] = sha(name)
		f.Subjects["source:"+name] = "file:" + name + "@" + f.Assets[name]
		details.Subjects["source:"+name] = f.Subjects["source:"+name]
	}
	add("psst.zip-source-" + binding.Version + ".tar.gz")
	add("psst.zip-upstream-inputs-" + binding.Version + ".tar.gz")
	descriptors := map[string]containerbuild.NativeDescriptor{}
	descriptorHashes := map[string]string{}
	images := map[string]recoveryImage{}
	for _, arch := range []string{"amd64", "arm64"} {
		platform := "linux/" + arch
		identity := scanning.NativeIdentity{Source: scanning.Source{Repository: binding.Repository, Version: binding.Version, Commit: binding.Commit}, Platform: platform}
		configs := map[string]string{"backend": actualImages["backend-"+arch].Config, "web": actualImages["web-"+arch].Config}
		runtimeName := "psst.zip-" + binding.Version + "-runtime-sources-" + arch + ".tar.gz"
		moduleName := "psst.zip-dependency-inputs-" + binding.Version + "-" + arch + ".tar.gz"
		add(runtimeName)
		add(moduleName)
		descriptor := containerbuild.NativeDescriptor{SchemaVersion: 2, Kind: "native-release-artifacts", Source: identity, OriginalTestedConfigs: map[string]string{"backend": sha("old-backend"), "web": sha("old-web")}, TestedConfigs: configs, SourceHelperConfig: sha("source-helper"), OCIExporter: "docker-save-byte-preserving-oci-v1", Artifacts: map[string]containerbuild.NativeArtifact{}, MeasurementAuthenticationRequired: true}
		for _, key := range []string{"build_record", "original_archive", "final_archive", "native_measurement", "smoke_report", "source_verification", "runtime_pack", "runtime_source", "backend_archive", "web_archive", "browser_inputs", "browser_verification"} {
			descriptor.Artifacts[key] = containerbuild.NativeArtifact{File: key + ".fixture", SHA256: sha(key + arch), Size: 100}
		}
		for _, component := range []string{"backend", "web"} {
			path := filepath.Join(root, arch, "native/export", component+"-"+arch+".oci.tar")
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			descriptor.Artifacts[component+"_archive"] = containerbuild.NativeArtifact{File: "export/" + component + "-" + arch + ".oci.tar", SHA256: digest(body), Size: int64(len(body))}
		}
		native := containerbuild.NativeAssemblyFacts{TestedConfigs: configs, Inputs: map[string]string{"runtime_inventory": descriptor.Artifacts["runtime_pack"].SHA256}, Images: map[string]map[string]any{}}
		for _, component := range []string{"backend", "web"} {
			target := component + "-" + arch
			image := actualImages[target]
			images[target] = image
			native.Images[component] = recoveryObject(t, image)
			f.Subjects[target] = "oci://ghcr.io/endorses/psst-zip-" + component + "@" + image.Manifest
			details.Coverage[target] = recoveryCoverage{Application: "psst.zip-source-" + binding.Version + ".tar.gz", Common: "psst.zip-upstream-inputs-" + binding.Version + ".tar.gz", Runtime: runtimeName, Modules: moduleName, RuntimeInventory: native.Inputs["runtime_inventory"], PreferredKey: sha("preferred"), Notices: sha("notices"), Config: image.Config, Manifest: image.Manifest, Archive: image.Archive, Verified: true}
			details.Images[target] = struct {
				Subject string `json:"subject"`
				Notices string `json:"notice_inventory_digest"`
			}{f.Subjects[target], sha("notices")}
		}
		f.Native[platform] = native
		descriptors[platform] = descriptor
		o.Native[platform] = filepath.Join(root, arch)
		raw := recoveryWrite(t, filepath.Join(o.Native[platform], "native/native-artifacts.json"), descriptor)
		descriptorHashes[platform] = digest(raw)
		executor.accepted[digest(raw)] = true
	}
	for _, component := range []string{"backend", "web"} {
		f.Subjects[component+"-index"] = "oci://" + manifest.Images[component].Index
	}
	f.BindingSHA256, _ = containerbuild.FoundationBinding(source.Context{Repository: binding.Repository, Version: binding.Version, Commit: binding.Commit}, f.Subjects)
	foundationRaw := recoveryWrite(t, filepath.Join(o.Prepared, "release-inputs.json"), f)
	configs := map[string]string{}
	for target, image := range images {
		configs[target] = image.Config
	}
	checks := map[string]json.RawMessage{"corresponding-source": recoveryJSON(t, details), "source-ci": recoveryJSON(t, map[string]any{"run_id": binding.RunID, "run_attempt": binding.Attempt, "jobs": map[string]string{"security": "success", "backend": "success", "web": "success"}}), "runtime-notices": json.RawMessage(`{}`), "final-image-smoke": recoveryJSON(t, map[string]any{"execution": map[string]string{"linux/amd64": "native", "linux/arm64": "native"}, "tested_configs": configs})}
	for _, gate := range []string{"source-scanners", "final-image-scanners"} {
		scans := []any{}
		targets := []string{"backend-source", "web-source"}
		if gate == "final-image-scanners" {
			targets = []string{"backend-amd64", "backend-arm64", "web-amd64", "web-arm64"}
		}
		for _, target := range targets {
			subject := "git:" + binding.Repository + "@" + binding.Commit
			if gate == "final-image-scanners" {
				subject = f.Subjects[target]
			}
			scans = append(scans, map[string]any{"target": target, "subject": subject, "status": "complete", "exit_code": 0, "scanner": "disposable-boundary-fixture", "version": "1", "database": "fixture-only", "scanned_at": "2026-10-24T00:00:00Z", "findings": []any{}})
		}
		checks[gate] = recoveryJSON(t, map[string]any{"scans": scans})
	}
	inputs := map[string]publication.Input{"corresponding-source-replay": {SHA256: sha("replay")}, "artifact-foundation": {SHA256: digest(foundationRaw)}, "native-descriptor-amd64": {SHA256: descriptorHashes["linux/amd64"]}, "native-descriptor-arm64": {SHA256: descriptorHashes["linux/arm64"]}}
	result, err := publication.NewSourceResult(publication.SourceResultOptions{Repository: binding.Repository, Version: binding.Version, Commit: binding.Commit, RunID: binding.RunID, Attempt: binding.Attempt, Binding: f.BindingSHA256, Subjects: f.Subjects, Checks: checks, Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	raw := recoveryWrite(t, o.SourceResult, result)
	executor.accepted[digest(raw)] = true
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		descriptor := descriptors[platform]
		record := recoveryMeasurement{SchemaVersion: 1, Kind: "native-upgrade-recovery-measurement", Source: descriptor.Source, Execution: "native", Manifest: digest(manifestRaw), Bundle: digest(bundle), NativeDescriptors: descriptorHashes, TestedConfigs: descriptor.TestedConfigs, SavedPair: descriptor.Artifacts["final_archive"], Images: images, HistoricalCommit: strings.Repeat("b", 40), ExecutionInputs: map[string]string{}, Tools: recoveryTools{"29.1.0", "v2.45.0", strings.TrimPrefix(platform, "linux/")}, Experiments: map[string]json.RawMessage{}, Started: recoveryStart, Completed: recoveryEnd, GatePending: true, Authentication: true}
		for _, name := range recoveryFixtureFiles {
			record.ExecutionInputs[name] = digest(executor.files[name])
		}
		for _, profile := range recoveryExperimentProfiles() {
			record.Experiments[profile.name] = recoveryJSON(t, recoveryExperimentFixture(t, descriptor.Source, record.HistoricalCommit, descriptor.TestedConfigs, profile.failure, profile.paused))
		}
		o.Measurements[platform] = filepath.Join(root, strings.TrimPrefix(platform, "linux/"), "recovery.json")
		raw := recoveryWrite(t, o.Measurements[platform], record)
		executor.accepted[digest(raw)] = true
	}
	return RecoveryEvaluator{Root: root, GitHub: github.Client{Runner: executor}, Now: func() time.Time { return time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC) }}, o, executor
}

func TestRecoveryExtendsOneAuthenticatedResultWithoutReplayingInputs(t *testing.T) {
	e, o, _ := recoveryAggregationFixture(t)
	result, err := e.Recover(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Checks) != 7 || result.Inputs["source-result"].SHA256 == "" || result.Inputs["recovery-amd64"].SHA256 == "" || result.Inputs["recovery-arm64"].SHA256 == "" {
		t.Fatal("terminal result lacks direct recovery or parent facts")
	}
	raw, err := os.ReadFile(o.Output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publication.Decode(raw); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Recover(context.Background(), o); err == nil {
		t.Fatal("overwrote a completed result")
	}
}
func TestRecoveryRejectsUnauthenticatedOrChangedBindingBeforeExperiments(t *testing.T) {
	for _, kind := range []string{"wrong-attempt", "changed-foundation", "changed-helper", "different-native-image"} {
		t.Run(kind, func(t *testing.T) {
			e, o, executor := recoveryAggregationFixture(t)
			switch kind {
			case "wrong-attempt":
				executor.wrongAttempt = true
			case "changed-foundation":
				path := filepath.Join(o.Prepared, "release-inputs.json")
				raw, _ := os.ReadFile(path)
				os.WriteFile(path, append(raw, ' '), 0600)
			case "changed-helper":
				os.WriteFile(filepath.Join(e.Root, recoveryFixtureFiles[0]), []byte("changed helper"), 0600)
			case "different-native-image":
				var record recoveryMeasurement
				raw, _ := os.ReadFile(o.Measurements["linux/arm64"])
				json.Unmarshal(raw, &record)
				image := record.Images["backend-amd64"]
				image.Config = digest([]byte("substituted"))
				record.Images["backend-amd64"] = image
				raw = recoveryWrite(t, o.Measurements["linux/arm64"], record)
				executor.accepted[digest(raw)] = true
			}
			if _, err := e.Recover(context.Background(), o); err == nil {
				t.Fatal("unsafe recovery binding accepted")
			}
			if _, err := os.Stat(o.Output); !os.IsNotExist(err) {
				t.Fatal("failed recovery emitted a terminal result")
			}
		})
	}
}
