package containerbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
	"github.com/endorses/psst.zip/tools/release/internal/process"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

func assemblyImages() (source.Context, map[string]oci.Image) {
	selected := source.Context{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}
	images := map[string]oci.Image{}
	for i, key := range []string{"backend-amd64", "backend-arm64", "web-amd64", "web-arm64"} {
		_, arch, _ := strings.Cut(key, "-")
		media := oci.ManifestMediaType
		if i == 1 {
			media = "application/vnd.docker.distribution.manifest.v2+json"
		}
		images[key] = oci.Image{Platform: "linux/" + arch, ManifestDigest: dg(string(rune('2' + i))), ManifestSize: int64(123 + i), ManifestMediaType: media, ConfigDigest: dg("8"), ArchiveDigest: dg("9"), Labels: map[string]string{"org.opencontainers.image.source": "https://github.com/" + selected.Repository, "org.opencontainers.image.version": selected.Version, "org.opencontainers.image.revision": selected.Commit}}
	}
	return selected, images
}

func TestAssemblyIndexesPreserveRawDescriptorsAndRejectOverlap(t *testing.T) {
	selected, images := assemblyImages()
	records, documents, err := AssembleIndexes(selected, images)
	if err != nil {
		t.Fatal(err)
	}
	for component, raw := range documents {
		if err := oci.ValidateIndex(raw, records[component]); err != nil {
			t.Fatal(err)
		}
		var index struct {
			Manifests []struct {
				Digest, MediaType string
				Size              int64
			}
		}
		if err := json.Unmarshal(raw, &index); err != nil {
			t.Fatal(err)
		}
		if len(index.Manifests) != 2 {
			t.Fatal("lost native child pair")
		}
		for i, arch := range []string{"amd64", "arm64"} {
			actual := images[component+"-"+arch]
			if index.Manifests[i].Digest != actual.ManifestDigest || index.Manifests[i].Size != actual.ManifestSize || index.Manifests[i].MediaType != actual.ManifestMediaType {
				t.Fatal("rewrote tested child metadata")
			}
		}
	}
	for _, mutation := range []string{"missing", "platform", "digest", "source", "media"} {
		_, wrong := assemblyImages()
		image := wrong["web-arm64"]
		switch mutation {
		case "missing":
			delete(wrong, "web-arm64")
		case "platform":
			image.Platform = "linux/amd64"
			wrong["web-arm64"] = image
		case "digest":
			image.ManifestDigest = wrong["backend-amd64"].ManifestDigest
			wrong["web-arm64"] = image
		case "source":
			image.Labels["org.opencontainers.image.revision"] = strings.Repeat("b", 40)
		case "media":
			image.ManifestMediaType = oci.IndexMediaType
			wrong["web-arm64"] = image
		}
		if _, _, err := AssembleIndexes(selected, wrong); err == nil {
			t.Fatal("accepted wrong image graph", mutation)
		}
	}
}

func TestAssemblyOwnSnapshotSurvivesMutableInputAndBoundsCopies(t *testing.T) {
	input, output := t.TempDir(), t.TempDir()
	name, original := "sources.tar.gz", []byte("original source bytes")
	if err := os.WriteFile(filepath.Join(input, name), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "source-inventory.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, asset, err := snapshotCollection(context.Background(), input, name, output, filepath.Join(t.TempDir(), "snapshot"))
	if err != nil || asset.SHA256 != assemblyHash(original) || asset.Size != int64(len(original)) || asset.Path != name {
		t.Fatal(asset, err)
	}
	retained, err := os.Stat(filepath.Join(output, name))
	if err != nil {
		t.Fatal(err)
	}
	linked, err := os.Stat(filepath.Join(snapshot, name))
	if err != nil || !os.SameFile(retained, linked) {
		t.Fatal("copied source twice instead of sharing owned snapshot", err)
	}
	if err := os.WriteFile(filepath.Join(input, name), []byte("changed external input"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(snapshot, name))
	if err != nil || !bytes.Equal(raw, original) {
		t.Fatal("snapshot followed mutable caller input", err)
	}
	for _, kind := range []string{"symlink", "bound", "cancel", "existing"} {
		from, to := filepath.Join(input, name), filepath.Join(t.TempDir(), "retained")
		ctx, limit := context.Background(), int64(1024)
		switch kind {
		case "symlink":
			link := filepath.Join(t.TempDir(), "linked")
			if err := os.Symlink(from, link); err != nil {
				t.Fatal(err)
			}
			from = link
		case "bound":
			limit = 2
		case "cancel":
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			ctx = cancelled
		case "existing":
			if err := os.WriteFile(to, []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := retainAssemblyAsset(ctx, from, to, limit); err == nil {
			t.Fatal("accepted unsafe source copy", kind)
		}
		if kind == "existing" {
			raw, _ := os.ReadFile(to)
			if string(raw) != "existing" {
				t.Fatal("overwrote existing asset")
			}
		} else if _, err := os.Lstat(to); !os.IsNotExist(err) {
			t.Fatal("left invalid owned output", kind, err)
		}
	}
}

func TestAssemblyIdentityAndScannerInputsStayBoundToReviewedGit(t *testing.T) {
	b, git := gitFixture(t)
	git("remote", "add", "origin", "https://github.com/endorses/psst.zip.git")
	commit := git("rev-parse", "HEAD")
	o := AssembleOptions{Repository: "endorses/psst.zip", Ref: "refs/tags/v1.2.3", EventSHA: commit, ReviewedCommit: commit, EventName: "push"}
	identity, err := b.assemblyIdentity(context.Background(), o)
	if err != nil || identity.Commit != commit {
		t.Fatal(identity, err)
	}
	o.EventName = "workflow_dispatch"
	if _, err := b.assemblyIdentity(context.Background(), o); err == nil {
		t.Fatal("tag assembly borrowed dispatch event")
	}
	o.EventName, o.ReviewedCommit = "push", strings.Repeat("b", 40)
	if _, err := b.assemblyIdentity(context.Background(), o); err == nil {
		t.Fatal("assembled unreviewed source")
	}
	if err := checkScanApplication(scanning.SourceMeasurement{Inputs: map[string]string{"backend/go.mod": dg("1")}}, map[string][]byte{"backend/go.mod": []byte("actual Git input")}); err == nil {
		t.Fatal("scanner used another Git input")
	}
	var stdout bytes.Buffer
	inputs := filepath.Join(t.TempDir(), "inputs.json")
	if err := os.WriteFile(inputs, []byte(`{"publication_authorized":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), b, []string{"assemble", "--inputs", inputs}, &stdout); err == nil || stdout.Len() != 0 {
		t.Fatal("unknown approval field emitted foundation")
	}
}

func TestAssemblyRequiresExplicitUnapprovedSmokeAndCurrentNativeCompiler(t *testing.T) {
	smoke := map[string]any{"schema_version": 1, "kind": "release-image-smoke", "version": "v1.2.3", "revision": strings.Repeat("a", 40), "platform": "linux/amd64", "execution": "native", "tested_configs": map[string]string{"backend": dg("5"), "web": dg("6")}, "runtime_pack_sha256": dg("7"), "checks": []string{}, "completed_at": "2026-10-24T00:00:00Z", "publication_authorized": false}
	if _, err := measuredConfigs(encoded(t, smoke)); err != nil {
		t.Fatal(err)
	}
	delete(smoke, "publication_authorized")
	if _, err := measuredConfigs(encoded(t, smoke)); err == nil {
		t.Fatal("omitted false authorization became a successful smoke field")
	}
	b, _ := resolver(t, baseFixture())
	candidate, err := b.ResolveBases(context.Background(), "v1.2.3", strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]json.RawMessage{}
	for i, component := range []string{"backend", "web"} {
		metadata[component] = encoded(t, map[string]string{"containerimage.config.digest": dg(string(rune('5' + i))), "containerimage.digest": dg(string(rune('5' + i)))})
	}
	record := BuildRecord{Candidate: candidate, CheckedPlatform: "linux/amd64", NativeExecution: true, ToolchainOutput: map[string]string{"go": "go version go1.27.3 linux/amd64", "node": "v26.12.1", "docker": "actual", "compose": "actual", "buildx": "actual", "builder": "actual"}, BuildMetadata: metadata, ImageArchive: ArchiveRecord{"original.tar", strings.Repeat("1", 64), 123}, Limitations: []string{"No publication approval."}}
	name := filepath.Join(t.TempDir(), "build.json")
	if err := os.WriteFile(name, encoded(t, record), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, tools, err := readAssemblyBuild(name, candidate, "linux/amd64"); err != nil || tools["go"] != "go1.27.3" {
		t.Fatal(tools, err)
	}
	record.ToolchainOutput["go"] = "go version go1.27.2 linux/amd64"
	if err := os.WriteFile(name, encoded(t, record), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readAssemblyBuild(name, candidate, "linux/amd64"); err == nil {
		t.Fatal("accepted stale application compiler producer")
	}
}

func TestFoundationBindingMatchesLegacyPythonAndRetainsUnapprovedKeys(t *testing.T) {
	selected, images := assemblyImages()
	records, _, err := AssembleIndexes(selected, images)
	if err != nil {
		t.Fatal(err)
	}
	subjects := map[string]string{"manifest": "file:release-manifest.json@" + dg("a"), "bundle": "file:psst.zip-deployment-v1.2.3.tar.gz@" + dg("b"), "source:psst.zip-source-v1.2.3.tar.gz": "file:psst.zip-source-v1.2.3.tar.gz@" + dg("c")}
	for component, record := range records {
		subjects[component+"-index"] = "oci://" + record.Index
		repository, _, _ := strings.Cut(record.Index, "@")
		for platform, digest := range record.PlatformDigests {
			subjects[component+"-"+strings.TrimPrefix(platform, "linux/")] = "oci://" + repository + "@" + digest
		}
	}
	binding, err := FoundationBinding(selected, subjects)
	if err != nil {
		t.Fatal(err)
	}
	foundation := ArtifactFoundation{SchemaVersion: 2, Kind: "prepared-release-inputs", Repository: selected.Repository, Version: selected.Version, SourceCommit: selected.Commit, SourceKind: "version-tag", BindingSHA256: binding, Assets: map[string]string{}, Subjects: subjects, MeasurementAuthenticationRequired: true, CommonFiles: map[string][]byte{"private": []byte("must not serialize")}}
	raw := encoded(t, foundation)
	var retained map[string]json.RawMessage
	if err := contract.Decode(raw, maxJSON, &retained); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"repository", "version", "source_commit", "source_kind", "assets", "subjects", "binding_sha256"} {
		if len(retained[key]) == 0 {
			t.Fatal("lost legacy input key", key)
		}
	}
	if !bytes.Equal(retained["publication_authorized"], []byte("false")) || bytes.Contains(raw, []byte("must not serialize")) {
		t.Fatal("changed approval or raw payload boundary")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("legacy Python binding reader unavailable")
	}
	payload := encoded(t, map[string]any{"repository": selected.Repository, "ref": "refs/tags/" + selected.Version, "commit": selected.Commit, "workflow": ".github/workflows/release.yml", "subjects": subjects})
	script := `import hashlib,json,sys; print('sha256:'+hashlib.sha256((json.dumps(json.loads(sys.argv[1]),indent=2,sort_keys=True)+'\n').encode()).hexdigest())`
	actual, err := (process.Runner{}).Output(context.Background(), 10*time.Second, python, "-I", "-c", script, string(payload))
	if err != nil || strings.TrimSpace(string(actual)) != binding {
		t.Fatal("changed legacy subject binding bytes", string(actual), binding, err)
	}
}
