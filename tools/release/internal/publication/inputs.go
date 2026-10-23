package publication

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	releasegithub "github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
)

type PublishOptions struct {
	Root, Inputs, State, ReviewedCommit, Checksums string
}
type Inputs struct {
	Result        Result
	Binding       releasegithub.Binding
	Manifest      contract.ManifestV1
	Assets        []releasegithub.Asset
	Snapshot      string
	ResultPath    string
	Indexes       map[string]string
	Archives      map[string]string
	TestedConfigs map[string]string
	Expected      map[string]string
}

func workflowBinding(environment map[string]string, reviewed string) (releasegithub.Binding, error) {
	version := strings.TrimPrefix(environment["GITHUB_REF"], "refs/tags/")
	run, _ := strconv.ParseInt(environment["GITHUB_RUN_ID"], 10, 64)
	attempt, _ := strconv.ParseInt(environment["GITHUB_RUN_ATTEMPT"], 10, 64)
	b := releasegithub.Binding{Repository: environment["GITHUB_REPOSITORY"], Version: version, Commit: reviewed, RunID: run, Attempt: attempt}
	if b.Validate() != nil {
		return b, fmt.Errorf("invalid hosted publication identity")
	}
	expected := map[string]string{
		"GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted", "GITHUB_JOB": "publish", "GITHUB_REF": "refs/tags/" + version,
		"GITHUB_SHA": reviewed, "GITHUB_EVENT_NAME": "push", "GITHUB_WORKFLOW_REF": b.Repository + "/" + releasegithub.ReleaseWorkflow + "@refs/tags/" + version, "GITHUB_WORKFLOW_SHA": reviewed,
	}
	for key, value := range expected {
		if environment[key] != value {
			return b, fmt.Errorf("publication requires the exact trusted hosted tag workflow")
		}
	}
	return b, nil
}

func subjectParts(subject string) (string, string, error) {
	name, digest, ok := strings.Cut(subject, "@")
	if !ok || !digestPattern.MatchString(digest) || strings.ContainsAny(name, " \t\r\n") || strings.Contains(digest, "@") {
		return "", "", fmt.Errorf("invalid exact publication subject")
	}
	return name, digest, nil
}

// LoadInputs consumes the authenticated result and public deployment contract.
// It rehashes source artifacts without rerunning the completed experiments or
// producing another serialized approval/gate wrapper.
func LoadInputs(ctx context.Context, o PublishOptions, environment map[string]string, verifier releasegithub.Client) (Inputs, error) {
	var inputs Inputs
	b, err := workflowBinding(environment, o.ReviewedCommit)
	if err != nil {
		return inputs, err
	}
	inputs.Binding = b
	if err = checkedCheckout(ctx, verifier, b); err != nil {
		return inputs, err
	}
	if !filepath.IsAbs(o.Inputs) || !filepath.IsAbs(o.State) {
		return inputs, fmt.Errorf("publication requires absolute input and private state directories")
	}
	resultPath := filepath.Join(o.Inputs, "recovery/release-result.json")
	raw, err := read(resultPath)
	if err != nil {
		return inputs, fmt.Errorf("completed release result unavailable")
	}
	r, err := Decode(raw)
	if err != nil {
		return inputs, err
	}
	if r.Repository != b.Repository || r.Version != b.Version || r.Commit != b.Commit || r.RunID != b.RunID || r.Attempt != b.Attempt || len(r.Checks) != 7 {
		return inputs, fmt.Errorf("publication result differs from complete current tag attempt")
	}
	var ci struct {
		Jobs map[string]string `json:"jobs"`
	}
	if json.Unmarshal(r.Checks["source-ci"], &ci) != nil || len(ci.Jobs) != 3 || ci.Jobs["security"] != "success" || ci.Jobs["backend"] != "success" || ci.Jobs["web"] != "success" {
		return inputs, fmt.Errorf("exact source security/backend/web checks did not all pass")
	}
	if err = verifier.VerifyBytes(ctx, raw, b, nil); err != nil {
		return inputs, err
	}
	// Preserve the authenticated bytes beside the frozen inputs for diagnostics.
	inputs.Result, inputs.ResultPath = r, resultPath
	prepared := filepath.Join(o.Inputs, "prepared")
	manifestRaw, err := read(filepath.Join(prepared, "release-manifest.json"))
	if err != nil {
		return inputs, fmt.Errorf("public deployment manifest unavailable")
	}
	m, err := contract.DecodeManifestV1(manifestRaw, b.Repository)
	if err != nil || m.Version != b.Version || m.Source.Commit != b.Commit || m.PayloadProfile != "deployment-ready" {
		return inputs, fmt.Errorf("deployment manifest differs from reviewed release")
	}
	inputs.Manifest = m
	expectedImages := map[string]string{}
	for _, component := range []string{"backend", "web"} {
		image := m.Images[component]
		repository, _, _ := strings.Cut(image.Index, "@")
		expectedImages[component+"-index"] = "oci://" + image.Index
		for _, arch := range []string{"amd64", "arm64"} {
			expectedImages[component+"-"+arch] = "oci://" + repository + "@" + image.PlatformDigests["linux/"+arch]
		}
	}
	fileDigests, names := map[string]string{}, []string{}
	inputs.Expected = map[string]string{}
	for key, subject := range r.Subjects {
		name, digest, err := subjectParts(subject)
		if err != nil {
			return inputs, err
		}
		switch {
		case strings.HasPrefix(name, "file:"):
			name = strings.TrimPrefix(name, "file:")
			if filepath.Base(name) != name || !safeAssetName(name) || fileDigests[name] != "" {
				return inputs, fmt.Errorf("invalid or duplicate file publication subject")
			}
			if key == "manifest" && name != "release-manifest.json" || key == "bundle" && name != m.Bundle.Name || key != "manifest" && key != "bundle" && key != "source:"+name {
				return inputs, fmt.Errorf("file subject key differs from its release asset")
			}
			fileDigests[name] = digest
			names = append(names, name)
		case strings.HasPrefix(name, "oci://"):
			if expectedImages[key] != subject {
				return inputs, fmt.Errorf("image subject differs from deployment manifest")
			}
			delete(expectedImages, key)
			name = strings.TrimPrefix(name, "oci://")
		default:
			return inputs, fmt.Errorf("unsupported publication subject type")
		}
		pair := name + "@" + digest
		if _, exists := inputs.Expected[pair]; exists {
			return inputs, fmt.Errorf("duplicate final publication subject")
		}
		inputs.Expected[pair] = strings.TrimPrefix(digest, "sha256:")
	}
	if len(expectedImages) != 0 || fileDigests[m.Bundle.Name] != "sha256:"+m.Bundle.SHA256 || fileDigests["release-manifest.json"] == "" || len(names) < 3 {
		return inputs, fmt.Errorf("publication subject coverage is incomplete")
	}
	sort.Strings(names)
	snapshot, assets, err := releasegithub.SnapshotAssetsAt(prepared, names, o.State)
	if err != nil {
		return inputs, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(snapshot)
		}
	}()
	for _, asset := range assets {
		if "sha256:"+asset.SHA256 != fileDigests[asset.Name] {
			return inputs, fmt.Errorf("publication asset differs from authenticated result")
		}
	}
	if _, err = contract.ReadBundleV1(filepath.Join(snapshot, m.Bundle.Name), m); err != nil {
		return inputs, err
	}
	if err = os.Mkdir(filepath.Join(snapshot, ".evidence"), 0700); err != nil {
		return inputs, fmt.Errorf("private evidence directory unavailable")
	}
	if err = os.WriteFile(filepath.Join(snapshot, ".evidence/release-result.json"), raw, 0400); err != nil {
		return inputs, fmt.Errorf("authenticated result snapshot could not be retained")
	}
	inputs.ResultPath = filepath.Join(snapshot, ".evidence/release-result.json")
	inputs.Assets, inputs.Snapshot = assets, snapshot
	inputs.Indexes, inputs.Archives = map[string]string{}, map[string]string{}
	var smoke struct {
		Configs map[string]string `json:"tested_configs"`
	}
	if json.Unmarshal(r.Checks["final-image-smoke"], &smoke) != nil || len(smoke.Configs) != 4 {
		return inputs, fmt.Errorf("all four actually tested native image configurations are required")
	}
	inputs.TestedConfigs = smoke.Configs
	for _, component := range []string{"backend", "web"} {
		indexRaw, indexErr := read(filepath.Join(prepared, component+"-index.json"))
		if indexErr != nil || oci.ValidateIndex(indexRaw, m.Images[component]) != nil {
			return inputs, fmt.Errorf("prepared index differs from authenticated native image pair")
		}
		inputs.Indexes[component] = filepath.Join(snapshot, ".evidence", component+"-index.json")
		if err = writePrivate(inputs.Indexes[component], indexRaw); err != nil {
			return inputs, err
		}
		for _, arch := range []string{"amd64", "arm64"} {
			key := component + "-" + arch
			if !digestPattern.MatchString(smoke.Configs[key]) {
				return inputs, fmt.Errorf("invalid tested native image configuration")
			}
			inputs.Archives[key] = filepath.Join(o.Inputs, arch, "native/export", key+".oci.tar")
		}
	}
	ok = true
	return inputs, nil
}

func checkedCheckout(ctx context.Context, client releasegithub.Client, b releasegithub.Binding) error {
	read := func(args ...string) (string, error) {
		raw, err := client.Command(ctx, 15*time.Second, append([]string{"git"}, args...)...)
		return strings.TrimSpace(string(raw)), err
	}
	origin, err := read("remote", "get-url", "origin")
	if err != nil || origin != "https://github.com/"+b.Repository+".git" && origin != "https://github.com/"+b.Repository && origin != "git@github.com:"+b.Repository+".git" && origin != "git@github.com:"+b.Repository {
		return fmt.Errorf("checkout origin differs from the trusted repository")
	}
	head, err := read("rev-parse", "HEAD")
	if err != nil || head != b.Commit {
		return fmt.Errorf("checkout is not the reviewed publication source")
	}
	changed, err := read("diff", "--name-only", b.Commit, "--")
	if err != nil || changed != "" {
		return fmt.Errorf("tracked checkout differs from reviewed publication source")
	}
	tag, err := read("rev-parse", "--verify", "refs/tags/"+b.Version+"^{commit}")
	if err != nil || tag != b.Commit {
		return fmt.Errorf("local publication tag differs from reviewed source")
	}
	if _, err = read("merge-base", "--is-ancestor", b.Commit, "origin/main"); err != nil {
		return fmt.Errorf("publication tag is outside reviewed main ancestry")
	}
	return nil
}

func safeAssetName(name string) bool {
	if len(name) < 1 || len(name) > 200 || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
