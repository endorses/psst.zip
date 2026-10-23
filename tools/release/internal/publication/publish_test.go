package publication

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	releasegithub "github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
)

func releaseJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, e := contract.JSON(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func releaseWrite(t *testing.T, name string, raw []byte) {
	t.Helper()
	if os.MkdirAll(filepath.Dir(name), 0700) != nil || os.WriteFile(name, raw, 0600) != nil {
		t.Fatal("fixture write failed")
	}
}
func tarBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := files[name]
		if e := w.WriteHeader(&tar.Header{Name: name, Mode: 0644, Typeflag: tar.TypeReg, Size: int64(len(raw))}); e != nil {
			t.Fatal(e)
		}
		if _, e := w.Write(raw); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func descriptor(raw []byte, media string) map[string]any {
	return map[string]any{"mediaType": media, "digest": digest(raw), "size": len(raw)}
}

type publicationFixture struct {
	o         PublishOptions
	env       map[string]string
	result    Result
	payloads  map[string][]byte
	registry  map[string][]byte
	release   releasegithub.Release
	published bool
	mutations int
	failure   string
}

func newPublicationFixture(t *testing.T) *publicationFixture {
	t.Helper()
	root := t.TempDir()
	version, commit, repository := "v1.2.3", strings.Repeat("a", 40), "endorses/psst.zip"
	f := &publicationFixture{o: PublishOptions{Root: root, Inputs: filepath.Join(root, "inputs"), State: filepath.Join(root, "state"), ReviewedCommit: commit, Checksums: filepath.Join(root, "subjects.txt")}, payloads: map[string][]byte{}, registry: map[string][]byte{}}
	if e := os.Mkdir(f.o.State, 0700); e != nil {
		t.Fatal(e)
	}
	f.env = map[string]string{"GH_TOKEN": "disposable-fixture-token", "PSST_IMMUTABLE_INSPECTION_TOKEN": "disposable-inspection-token", "GITHUB_ACTOR": "endorses", "GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted", "GITHUB_REPOSITORY": repository, "GITHUB_JOB": "publish", "GITHUB_REF": "refs/tags/" + version, "GITHUB_SHA": commit, "GITHUB_EVENT_NAME": "push", "GITHUB_WORKFLOW_REF": repository + "/" + releasegithub.ReleaseWorkflow + "@refs/tags/" + version, "GITHUB_WORKFLOW_SHA": commit, "GITHUB_RUN_ID": "12", "GITHUB_RUN_ATTEMPT": "2"}
	prepared := filepath.Join(f.o.Inputs, "prepared")
	m := contract.ManifestV1{SchemaVersion: 1, Version: version, PayloadProfile: "deployment-ready", Source: contract.SourceV1{Repository: repository, Commit: commit, ArchiveURL: "https://github.com/" + repository + "/archive/" + commit + ".tar.gz"}, Platforms: []string{"linux/amd64", "linux/arm64"}, Images: map[string]contract.ImageV1{}, Requirements: contract.RequirementsV1{Docker: "27.0.0", Compose: "2.24.4"}, Notes: contract.NotesV1{CheckpointRequired: true, Migration: "fixture migration", Rollback: "fixture rollback"}, Build: contract.BuildV1{BaseImages: map[string]string{}, Toolchains: map[string]string{"go": "go1.27.3", "node": "26.12.0"}}}
	for _, name := range []string{"golang", "node", "alpine", "caddy"} {
		m.Build.BaseImages[name] = "docker.io/library/" + name + "@sha256:" + strings.Repeat("f", 64)
	}
	configs := map[string]string{}
	subjects := map[string]string{}
	for _, component := range []string{"backend", "web"} {
		children := []any{}
		image := contract.ImageV1{PlatformDigests: map[string]string{}}
		for _, arch := range []string{"amd64", "arm64"} {
			key := component + "-" + arch
			layer := tarBytes(t, map[string][]byte{"app/fixture": []byte(key)})
			labels := map[string]string{"org.opencontainers.image.title": "psst.zip " + component, "org.opencontainers.image.source": "https://github.com/" + repository, "org.opencontainers.image.version": version, "org.opencontainers.image.revision": commit, "org.opencontainers.image.licenses": "AGPL-3.0-only", "zip.psst.source.archive": m.Source.ArchiveURL}
			config := releaseJSON(t, map[string]any{"os": "linux", "architecture": arch, "config": map[string]any{"Labels": labels}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{digest(layer)}}})
			manifest := releaseJSON(t, map[string]any{"schemaVersion": 2, "mediaType": oci.ManifestMediaType, "config": descriptor(config, oci.ConfigMediaType), "layers": []any{descriptor(layer, oci.LayerMediaType)}})
			child := descriptor(manifest, oci.ManifestMediaType)
			child["platform"] = map[string]string{"os": "linux", "architecture": arch}
			index := releaseJSON(t, map[string]any{"schemaVersion": 2, "mediaType": oci.IndexMediaType, "manifests": []any{child}})
			files := map[string][]byte{"index.json": index, "oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`)}
			for _, raw := range [][]byte{config, manifest, layer} {
				files["blobs/sha256/"+strings.TrimPrefix(digest(raw), "sha256:")] = raw
			}
			releaseWrite(t, filepath.Join(f.o.Inputs, arch, "native/export", key+".oci.tar"), tarBytes(t, files))
			configs[key] = digest(config)
			image.PlatformDigests["linux/"+arch] = digest(manifest)
			children = append(children, child)
			subjects[key] = "oci://ghcr.io/endorses/psst-zip-" + component + "@" + digest(manifest)
		}
		index := releaseJSON(t, map[string]any{"schemaVersion": 2, "mediaType": oci.IndexMediaType, "manifests": children})
		image.Index = "ghcr.io/endorses/psst-zip-" + component + "@" + digest(index)
		m.Images[component] = image
		subjects[component+"-index"] = "oci://" + image.Index
		releaseWrite(t, filepath.Join(prepared, component+"-index.json"), index)
	}
	files := map[string][]byte{}
	for _, name := range append(append([]string{}, contract.DeploymentRequiredFiles...), "deploy/update.py") {
		files[name] = []byte("inert fixture")
	}
	files["release-bundle.json"] = releaseJSON(t, map[string]any{"schema_version": 1, "version": version, "source_commit": commit, "payload_profile": "deployment-ready"})
	var packed bytes.Buffer
	zip := gzip.NewWriter(&packed)
	_, _ = zip.Write(tarBytes(t, files))
	if e := zip.Close(); e != nil {
		t.Fatal(e)
	}
	m.Bundle = contract.BundleV1{Name: "psst.zip-deployment-" + version + ".tar.gz", SHA256: strings.TrimPrefix(digest(packed.Bytes()), "sha256:")}
	f.payloads["release-manifest.json"] = releaseJSON(t, m)
	f.payloads[m.Bundle.Name] = packed.Bytes()
	f.payloads["corresponding-source.tar.gz"] = []byte("inert source fixture")
	for name, raw := range f.payloads {
		releaseWrite(t, filepath.Join(prepared, name), raw)
		key := "source:" + name
		if name == "release-manifest.json" {
			key = "manifest"
		} else if name == m.Bundle.Name {
			key = "bundle"
		}
		subjects[key] = "file:" + name + "@" + digest(raw)
	}
	binding := digest(releaseJSON(t, map[string]any{"repository": repository, "ref": "refs/tags/" + version, "commit": commit, "workflow": releasegithub.ReleaseWorkflow, "subjects": subjects}))
	r := Result{SchemaVersion: 1, Kind: "container-release-result", Repository: repository, Version: version, Commit: commit, RunID: 12, Attempt: 2, Binding: binding, Subjects: subjects, Checks: map[string]json.RawMessage{}, Inputs: map[string]Input{"corresponding-source-replay": {SHA256: "sha256:" + strings.Repeat("b", 64)}, "source-result": {SHA256: "sha256:" + strings.Repeat("c", 64)}}, Platforms: map[string]Platform{}}
	for _, gate := range []string{"corresponding-source", "runtime-notices"} {
		r.Checks[gate] = json.RawMessage(`{"checked":true}`)
	}
	r.Checks["source-ci"] = releaseJSON(t, map[string]any{"run_id": 12, "run_attempt": 2, "jobs": map[string]string{"security": "success", "backend": "success", "web": "success"}})
	execution := map[string]string{"linux/amd64": "native", "linux/arm64": "native"}
	r.Checks["final-image-smoke"] = releaseJSON(t, map[string]any{"execution": execution, "tested_configs": configs})
	r.Checks["upgrade-recovery"] = releaseJSON(t, map[string]any{"execution": execution})
	for platform := range execution {
		r.Platforms[platform] = Platform{Execution: "native", Checks: []string{"final-image-smoke", "upgrade-recovery"}}
	}
	for _, gate := range []string{"source-scanners", "final-image-scanners"} {
		scans := []any{}
		names := []string{"backend-source", "web-source"}
		if gate == "final-image-scanners" {
			names = []string{"backend-amd64", "backend-arm64", "web-amd64", "web-arm64"}
		}
		for _, name := range names {
			subject := "git:" + repository + "@" + commit
			if gate == "final-image-scanners" {
				subject = subjects[name]
			}
			scans = append(scans, map[string]any{"target": name, "subject": subject, "status": "complete", "exit_code": 0, "scanner": "fixture", "version": "1", "database": "fixture-db", "scanned_at": "2026-10-24T00:00:00Z", "findings": []any{}})
		}
		r.Checks[gate] = releaseJSON(t, map[string]any{"scans": scans})
	}
	f.result = r
	releaseWrite(t, filepath.Join(f.o.Inputs, "recovery/release-result.json"), releaseJSON(t, r))
	return f
}

func (f *publicationFixture) attestation(t *testing.T, content []byte) []byte {
	t.Helper()
	repository, ref := "https://github.com/"+f.result.Repository, "refs/tags/"+f.result.Version
	subjects := []any{map[string]any{"name": "release-result.json", "digest": map[string]string{"sha256": strings.TrimPrefix(digest(content), "sha256:")}}}
	if !bytes.Contains(content, []byte(`"container-release-result"`)) {
		subjects = nil
		for _, subject := range f.result.Subjects {
			name, hash, _ := subjectParts(subject)
			name = strings.TrimPrefix(strings.TrimPrefix(name, "file:"), "oci://")
			subjects = append(subjects, map[string]any{"name": name, "digest": map[string]string{"sha256": strings.TrimPrefix(hash, "sha256:")}})
		}
	}
	return releaseJSON(t, []any{map[string]any{"verificationResult": map[string]any{"signature": map[string]any{"certificate": map[string]bool{"fixture": true}}, "verifiedTimestamps": []any{map[string]bool{"fixture": true}}, "statement": map[string]any{"_type": "https://in-toto.io/Statement/v1", "predicateType": releasegithub.Predicate, "subject": subjects, "predicate": map[string]any{"buildDefinition": map[string]any{"buildType": "https://actions.github.io/buildtypes/workflow/v1", "externalParameters": map[string]any{"workflow": map[string]string{"repository": repository, "ref": ref, "path": releasegithub.ReleaseWorkflow}}, "internalParameters": map[string]any{"github": map[string]string{"event_name": "push", "runner_environment": "github-hosted"}}, "resolvedDependencies": []any{map[string]any{"uri": "git+" + repository + "@" + ref, "digest": map[string]string{"gitCommit": f.result.Commit}}}}, "runDetails": map[string]any{"builder": map[string]string{"id": repository + "/" + releasegithub.ReleaseWorkflow + "@" + ref}, "metadata": map[string]string{"invocationId": repository + "/actions/runs/12/attempts/2"}}}}}}})
}

func (f *publicationFixture) executor(t *testing.T) releasegithub.Client {
	t.Helper()
	workflow := []byte("concurrency:\n  group: container-release-publication\n  cancel-in-progress: false\njobs:\n  publish:\n")
	return releasegithub.Client{Runner: commandFixture(func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
		if args[0] == "git" {
			switch args[1] {
			case "remote":
				return []byte("https://github.com/" + f.result.Repository + ".git"), nil
			case "rev-parse":
				return []byte(f.result.Commit), nil
			case "diff", "merge-base":
				return nil, nil
			case "show":
				return workflow, nil
			}
		}
		if args[1] == "attestation" {
			raw, e := os.ReadFile(args[3])
			if e != nil {
				return nil, e
			}
			return f.attestation(t, raw), nil
		}
		if args[1] == "release" {
			switch args[2] {
			case "create":
				f.mutations++
				f.release = releasegithub.Release{ID: 42, Tag: f.result.Version, Draft: true}
				for _, path := range args[4:] {
					if path == "--repo" {
						break
					}
					raw, e := os.ReadFile(path)
					if e != nil {
						return nil, e
					}
					name := filepath.Base(path)
					f.payloads[name] = raw
					f.release.Assets = append(f.release.Assets, releasegithub.ReleaseAsset{ID: int64(len(f.release.Assets) + 1), Name: name, Size: int64(len(raw)), Digest: digest(raw), State: "uploaded"})
				}
				return nil, nil
			case "download":
				var dir, name string
				for i := range args[:len(args)-1] {
					if args[i] == "--dir" {
						dir = args[i+1]
					}
					if args[i] == "--pattern" {
						name = args[i+1]
					}
				}
				return nil, os.WriteFile(filepath.Join(dir, name), f.payloads[name], 0600)
			case "edit":
				f.mutations++
				f.published = true
				f.release.Draft, f.release.Immutable = false, true
				return nil, nil
			}
		}
		endpoint := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "repos/") || strings.HasPrefix(arg, "users/") {
				endpoint = arg
			}
		}
		switch {
		case strings.Contains(endpoint, "/git/ref/"):
			return releaseJSON(t, map[string]any{"ref": "refs/tags/" + f.result.Version, "object": map[string]string{"type": "commit", "sha": f.result.Commit}}), nil
		case strings.HasSuffix(endpoint, "/immutable-releases"):
			return []byte(`{"enabled":true}`), nil
		case strings.Contains(endpoint, "/actions/runs/"):
			return releaseJSON(t, map[string]any{"id": 12, "run_attempt": 2, "status": "in_progress", "event": "push", "head_sha": f.result.Commit, "path": releasegithub.ReleaseWorkflow, "repository": map[string]string{"full_name": f.result.Repository}, "head_repository": map[string]string{"full_name": f.result.Repository}}), nil
		case strings.Contains(endpoint, "/contents/"):
			return releaseJSON(t, map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(workflow)}), nil
		case strings.Contains(endpoint, "/releases?per_page="):
			if f.release.ID > 0 {
				return releaseJSON(t, []releasegithub.Release{f.release}), nil
			}
			return []byte(`[]`), nil
		case strings.Contains(endpoint, "/releases/tags/"):
			return releaseJSON(t, f.release), nil
		case endpoint == "users/endorses":
			return []byte(`{"login":"endorses","type":"User"}`), nil
		case strings.Contains(endpoint, "/versions?"):
			return []byte(`[]`), nil
		case strings.Contains(endpoint, "/packages/container/"):
			name := "psst-zip-backend"
			if strings.Contains(endpoint, "psst-zip-web") {
				name = "psst-zip-web"
			}
			body := fmt.Sprintf(`{"name":%q,"package_type":"container","visibility":"public","owner":{"login":"endorses"},"repository":{"full_name":"endorses/psst.zip"}}`, name)
			return []byte("HTTP/2.0 200 OK\r\n\r\n" + body), nil
		}
		return nil, fmt.Errorf("unexpected fixture GitHub operation")
	})}
}
func (f *publicationFixture) skopeo(t *testing.T) commandFixture {
	return func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
		if args[1] == "copy" {
			from, to := args[len(args)-2], args[len(args)-1]
			f.mutations++
			if strings.HasPrefix(from, "oci:") {
				if f.failure == "registry" && strings.Contains(to, "psst-zip-web") {
					return nil, fmt.Errorf("private fixture transport diagnostic")
				}
				layout := strings.TrimPrefix(from, "oci:")
				var catalog struct{ Manifests []oci.Descriptor }
				raw, _ := os.ReadFile(filepath.Join(layout, "index.json"))
				if json.Unmarshal(raw, &catalog) != nil || len(catalog.Manifests) != 1 {
					t.Fatal("pair OCI catalog does not select one exact index")
				}
				entries, _ := os.ReadDir(filepath.Join(layout, "blobs/sha256"))
				repository, _, _ := strings.Cut(strings.TrimPrefix(to, "docker://"), "@")
				for _, entry := range entries {
					raw, _ := os.ReadFile(filepath.Join(layout, "blobs/sha256", entry.Name()))
					f.registry[repository+"@sha256:"+entry.Name()] = raw
				}
				return nil, nil
			}
			body := f.registry[strings.TrimPrefix(from, "docker://")]
			if len(body) == 0 {
				return nil, fmt.Errorf("fixture registry subject missing")
			}
			if strings.HasPrefix(to, "oci:") {
				f.registry[to] = body
			} else {
				f.registry[strings.TrimPrefix(to, "docker://")] = body
			}
			return nil, nil
		}
		if args[1] == "inspect" {
			name := args[len(args)-1]
			if strings.HasPrefix(name, "docker://") {
				name = strings.TrimPrefix(name, "docker://")
			}
			raw := f.registry[name]
			if len(raw) == 0 {
				return nil, fmt.Errorf("missing fixture readback")
			}
			return raw, nil
		}
		return nil, fmt.Errorf("unexpected fixture image operation")
	}
}
func (f *publicationFixture) publicReader(t *testing.T) *publicReader {
	return &publicReader{&http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "" {
			t.Fatal("public fixture attached credentials")
		}
		if f.failure == "public" {
			return nil, fmt.Errorf("private anonymous failure")
		}
		var raw []byte
		if strings.HasSuffix(request.URL.Path, "/releases/42") {
			raw = releaseJSON(t, f.release)
		} else {
			at := strings.LastIndex(request.URL.Path, "/")
			id, _ := strconv.ParseInt(request.URL.Path[at+1:], 10, 64)
			for _, asset := range f.release.Assets {
				if asset.ID == id {
					raw = f.payloads[asset.Name]
				}
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
	})}}
}

func TestPublicationDisabledRehearsalAndPartialRegistryFailure(t *testing.T) {
	for _, failure := range []string{"", "registry", "public"} {
		t.Run("failure-"+failure, func(t *testing.T) {
			f := newPublicationFixture(t)
			f.failure = failure
			client := f.executor(t)
			if err := Prepare(context.Background(), f.o, f.env, client); err != nil {
				t.Fatal(err)
			}
			factory := func(i Inputs, c releasegithub.Client, j *journal, state string, env map[string]string, out io.Writer) (*registry, error) {
				r, e := newRegistry(i, c, j, state, env, out)
				if e == nil {
					r.runner = f.skopeo(t)
				}
				return r, e
			}
			err := executePublication(context.Background(), f.o, f.env, client, client, stageImages, factory, f.publicReader(t), io.Discard)
			if (err == nil) != (failure == "") {
				t.Fatalf("unexpected publication outcome: %v", err)
			}
			if failure == "registry" && f.published {
				t.Fatal("partial image push exposed a published release")
			}
			_, receiptErr := os.Stat(filepath.Join(f.o.State, "v1.2.3-publication-receipt.json"))
			if (receiptErr == nil) != (failure == "") {
				t.Fatal("unverified publication received a success receipt")
			}
			if failure != "" {
				before := f.mutations
				_ = executePublication(context.Background(), f.o, f.env, client, client, stageImages, factory, f.publicReader(t), io.Discard)
				if f.mutations != before {
					t.Fatal("uncertain publication automatically replayed mutations")
				}
			}
			entries, _ := os.ReadDir(f.o.State)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "registry-credentials-") {
					t.Fatal("publication retained image credentials")
				}
			}
		})
	}
}

func TestAuthenticatedResultCannotSubstituteAssetsOrSkippedSourceCI(t *testing.T) {
	for _, change := range []string{"source-bytes", "skipped-ci", "run-attempt"} {
		t.Run(change, func(t *testing.T) {
			f := newPublicationFixture(t)
			switch change {
			case "source-bytes":
				releaseWrite(t, filepath.Join(f.o.Inputs, "prepared/corresponding-source.tar.gz"), []byte("substituted source"))
			case "skipped-ci":
				f.result.Checks["source-ci"] = releaseJSON(t, map[string]any{"run_id": 12, "run_attempt": 2, "jobs": map[string]string{"security": "success", "backend": "success", "web": "skipped"}})
				releaseWrite(t, filepath.Join(f.o.Inputs, "recovery/release-result.json"), releaseJSON(t, f.result))
			case "run-attempt":
				f.env["GITHUB_RUN_ATTEMPT"] = "3"
			}
			if err := Prepare(context.Background(), f.o, f.env, f.executor(t)); err == nil {
				t.Fatal("substituted/failed preparation produced final attestation subjects")
			}
			if f.mutations != 0 {
				t.Fatal("failed preparation mutated external state")
			}
			if _, err := os.Stat(f.o.Checksums); !os.IsNotExist(err) {
				t.Fatal("failed preparation emitted action checksum input")
			}
		})
	}
}
