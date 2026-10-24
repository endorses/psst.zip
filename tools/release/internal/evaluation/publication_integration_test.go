package evaluation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
	"github.com/endorses/psst.zip/tools/release/internal/publication"
)

func recoveryTar(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
func recoveryBundle(t *testing.T, binding github.Binding) []byte {
	t.Helper()
	files := map[string][]byte{}
	for _, name := range append(append([]string{}, contract.DeploymentRequiredFiles...), "deploy/update.py") {
		files[name] = []byte("inert source-owned fixture; never executed")
	}
	files["release-bundle.json"] = recoveryJSON(t, map[string]any{"schema_version": 1, "version": binding.Version, "source_commit": binding.Commit, "payload_profile": "deployment-ready"})
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	if _, err := compressed.Write(recoveryTar(t, files)); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func recoveryNativeImage(t *testing.T, root string, binding github.Binding, component, arch string) (recoveryImage, map[string]any) {
	t.Helper()
	layer := recoveryTar(t, map[string][]byte{"app/fixture": []byte(component + "-" + arch)})
	config := recoveryJSON(t, map[string]any{"os": "linux", "architecture": arch, "config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.title": "psst.zip " + component, "org.opencontainers.image.source": "https://github.com/" + binding.Repository, "org.opencontainers.image.version": binding.Version, "org.opencontainers.image.revision": binding.Commit, "org.opencontainers.image.licenses": "AGPL-3.0-only", "zip.psst.source.archive": "https://github.com/" + binding.Repository + "/archive/" + binding.Commit + ".tar.gz"}}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{digest(layer)}}})
	descriptor := func(raw []byte, media string) map[string]any {
		return map[string]any{"mediaType": media, "digest": digest(raw), "size": len(raw)}
	}
	manifest := recoveryJSON(t, map[string]any{"schemaVersion": 2, "mediaType": oci.ManifestMediaType, "config": descriptor(config, oci.ConfigMediaType), "layers": []any{descriptor(layer, oci.LayerMediaType)}})
	child := descriptor(manifest, oci.ManifestMediaType)
	child["platform"] = map[string]string{"os": "linux", "architecture": arch}
	files := map[string][]byte{"oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`), "index.json": recoveryJSON(t, map[string]any{"schemaVersion": 2, "mediaType": oci.IndexMediaType, "manifests": []any{child}})}
	for _, body := range [][]byte{layer, config, manifest} {
		files["blobs/sha256/"+strings.TrimPrefix(digest(body), "sha256:")] = body
	}
	name := filepath.Join(root, arch, "native/export", component+"-"+arch+".oci.tar")
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, recoveryTar(t, files), 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := oci.InspectArchive(name, oci.Options{Repository: binding.Repository, Version: binding.Version, Commit: binding.Commit, Platform: "linux/" + arch, Component: component, TestedConfig: digest(config)})
	if err != nil {
		t.Fatal(err)
	}
	return recoveryImage{Platform: actual.Platform, Manifest: actual.ManifestDigest, ManifestSize: actual.ManifestSize, MediaType: actual.ManifestMediaType, Config: actual.ConfigDigest, Archive: actual.ArchiveDigest, BlobCount: actual.BlobCount}, child
}

// This composite checks the new typed handoffs, not the independent substantive
// source/scanner/experiment collectors. Their facts and hosted signatures are
// injected here; all native OCI/index/bundle bytes are real tiny fixtures.
func TestCurrentFoundationRecoveryResultFeedsPublicationPreparationWithoutMutation(t *testing.T) {
	e, o, executor := recoveryAggregationFixture(t)
	result, err := e.Recover(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	root := e.Root
	inputs := filepath.Join(root, "publication")
	prepared := filepath.Join(inputs, "prepared")
	if err := os.MkdirAll(prepared, 0700); err != nil {
		t.Fatal(err)
	}
	for key, subject := range result.Subjects {
		if key == "manifest" || key == "bundle" {
			name, _, _ := strings.Cut(strings.TrimPrefix(subject, "file:"), "@")
			body, err := os.ReadFile(filepath.Join(o.Prepared, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(prepared, name), body, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if strings.HasPrefix(key, "source:") {
			name := strings.TrimPrefix(key, "source:")
			if err := os.WriteFile(filepath.Join(prepared, name), []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, component := range []string{"backend", "web"} {
		body, err := os.ReadFile(filepath.Join(root, "indexes", component+"-index.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(prepared, component+"-index.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
		for _, arch := range []string{"amd64", "arm64"} {
			name := filepath.Join(arch, "native/export", component+"-"+arch+".oci.tar")
			body, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(inputs, name)
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, body, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	raw, _ := os.ReadFile(o.Output)
	recoveryWrite(t, filepath.Join(inputs, "recovery/release-result.json"), result)
	executor.accepted[digest(raw)] = true
	state := filepath.Join(root, "publication-state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	options := publication.PublishOptions{Root: root, Inputs: inputs, State: state, ReviewedCommit: o.Binding.Commit, Checksums: filepath.Join(root, "final-subjects.txt")}
	b := o.Binding
	environment := map[string]string{"GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted", "GITHUB_REPOSITORY": b.Repository, "GITHUB_JOB": "publish", "GITHUB_REF": "refs/tags/" + b.Version, "GITHUB_SHA": b.Commit, "GITHUB_EVENT_NAME": "push", "GITHUB_WORKFLOW_REF": b.Repository + "/" + github.ReleaseWorkflow + "@refs/tags/" + b.Version, "GITHUB_WORKFLOW_SHA": b.Commit, "GITHUB_RUN_ID": strconv.FormatInt(b.RunID, 10), "GITHUB_RUN_ATTEMPT": strconv.FormatInt(b.Attempt, 10)}
	if err := publication.Prepare(context.Background(), options, environment, e.GitHub); err != nil {
		t.Fatal(err)
	}
	checksums, err := os.ReadFile(options.Checksums)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(strings.TrimSpace(string(checksums)), "\n")) != 14 {
		t.Fatal("prepared attestation subjects lost a source offering or native image/index")
	}
	for _, subject := range result.Subjects {
		name, sha, _ := strings.Cut(subject, "@")
		name = strings.TrimPrefix(strings.TrimPrefix(name, "file:"), "oci://")
		if !strings.Contains(string(checksums), strings.TrimPrefix(sha, "sha256:")+"  "+name+"\n") {
			t.Fatal("publisher preparation changed an authenticated subject")
		}
	}
	loaded, err := publication.LoadInputs(context.Background(), options, environment, e.GitHub)
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{"backend", "web"} {
		for _, arch := range []string{"amd64", "arm64"} {
			target := component + "-" + arch
			actual, err := oci.InspectArchive(loaded.Archives[target], oci.Options{Repository: b.Repository, Version: b.Version, Commit: b.Commit, Component: component, Platform: "linux/" + arch, TestedConfig: loaded.TestedConfigs[target]})
			if err != nil || loaded.Result.Subjects[target] != "oci://ghcr.io/endorses/psst-zip-"+component+"@"+actual.ManifestDigest {
				t.Fatalf("publication input path/config differs from retained actual native fixture: %v", err)
			}
		}
	}
	if err := os.RemoveAll(loaded.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared, "psst.zip-source-"+b.Version+".tar.gz"), []byte("changed source offering"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := publication.LoadInputs(context.Background(), options, environment, e.GitHub); err == nil {
		t.Fatal("publisher accepted source asset substitution after completed recovery")
	}
	// Executor rejects every unrecognized command, including all remote mutation
	// operations. Preparation therefore cannot silently publish in this test.
	var completed publication.Result
	if json.Unmarshal(raw, &completed) != nil || len(completed.Checks) != 7 {
		t.Fatal("composite did not consume the direct completed result")
	}
}
