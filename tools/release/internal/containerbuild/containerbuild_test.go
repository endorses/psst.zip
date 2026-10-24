package containerbuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

type commandFunc func(context.Context, time.Duration, ...string) ([]byte, error)

func (f commandFunc) Output(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	return f(ctx, timeout, args...)
}

func encoded(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := contract.JSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func sha(raw []byte) string { sum := sha256.Sum256(raw); return "sha256:" + hex.EncodeToString(sum[:]) }
func dg(c string) string    { return "sha256:" + strings.Repeat(c, 64) }

func baseFixture() map[string]any {
	return map[string]any{"schemaVersion": 2, "mediaType": oci.IndexMediaType, "manifests": []any{
		map[string]any{"mediaType": oci.ManifestMediaType, "digest": dg("2"), "size": 123, "platform": map[string]string{"os": "linux", "architecture": "amd64"}},
		map[string]any{"mediaType": oci.ManifestMediaType, "digest": dg("3"), "size": 124, "platform": map[string]string{"os": "linux", "architecture": "arm64", "variant": "v8"}},
		map[string]any{"mediaType": oci.ManifestMediaType, "digest": dg("4"), "size": 125, "platform": map[string]string{"os": "unknown", "architecture": "unknown"}},
	}}
}

func resolver(t *testing.T, value map[string]any) (Builder, *[]string) {
	t.Helper()
	raw := encoded(t, value)
	summary := map[string]any{}
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatal(err)
	}
	summary["digest"] = sha(raw)
	calls := []string{}
	b := Builder{Runner: commandFunc(func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if len(args) != 6 && len(args) != 7 || args[0] != "docker" || args[1] != "buildx" || args[2] != "imagetools" || args[3] != "inspect" {
			return nil, fmt.Errorf("unexpected fixture command")
		}
		if args[len(args)-1] == "--raw" {
			return raw, nil
		}
		return encoded(t, summary), nil
	})}
	return b, &calls
}

func TestOfficialBaseResolutionAndCandidateBoundary(t *testing.T) {
	b, calls := resolver(t, baseFixture())
	c, err := b.ResolveBases(context.Background(), "v1.2.3", strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	if !c.CandidateOnly || len(*calls) != 10 || len(c.BaseImages) != 5 {
		t.Fatal("wrong candidate", c, *calls)
	}
	for _, call := range *calls {
		if strings.Contains(call, " --raw") && !strings.Contains(call, "@sha256:") {
			t.Fatal("did not query immutable index", call)
		}
	}
	raw := encoded(t, c)
	got, err := DecodeCandidate(raw)
	if err != nil || got.SourceCommit != c.SourceCommit {
		t.Fatal(got, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 8 || fields["candidate_only"] != true {
		t.Fatal("changed candidate v1 shape")
	}
	fields["payload_profile"] = "deployment-ready"
	if _, err := DecodeCandidate(encoded(t, fields)); err == nil {
		t.Fatal("candidate gained readiness fields")
	}
	c.BaseImages["golang"] = "docker.io/attacker/golang@" + dg("1")
	if err := c.Validate(); err == nil {
		t.Fatal("substituted official base repository")
	}
}

func TestRejectsWrongBaseGraphAndImmutableBytes(t *testing.T) {
	mutations := []func(map[string]any){
		func(v map[string]any) { v["schemaVersion"] = true },
		func(v map[string]any) { v["mediaType"] = oci.ManifestMediaType },
		func(v map[string]any) { v["manifests"] = v["manifests"].([]any)[:1] },
		func(v map[string]any) { v["manifests"] = append(v["manifests"].([]any), v["manifests"].([]any)[0]) },
		func(v map[string]any) { v["manifests"].([]any)[1].(map[string]any)["digest"] = dg("2") },
		func(v map[string]any) {
			v["manifests"].([]any)[1].(map[string]any)["platform"] = map[string]string{"os": "linux", "architecture": "arm64", "variant": "v9"}
		},
		func(v map[string]any) { v["manifests"].([]any)[0].(map[string]any)["mediaType"] = oci.IndexMediaType },
		func(v map[string]any) { v["manifests"].([]any)[0].(map[string]any)["size"] = false },
		func(v map[string]any) { v["manifests"] = append(v["manifests"].([]any), nil) },
		func(v map[string]any) { v["manifests"].([]any)[2].(map[string]any)["platform"] = nil },
	}
	for i, mutate := range mutations {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			v := baseFixture()
			mutate(v)
			b, _ := resolver(t, v)
			if _, err := b.ResolveBases(context.Background(), "v1.2.3", strings.Repeat("a", 40)); err == nil {
				t.Fatal("accepted wrong base platform graph")
			}
		})
	}
	b, _ := resolver(t, baseFixture())
	original := b.Runner
	b.Runner = commandFunc(func(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
		raw, err := original.Output(ctx, timeout, args...)
		if args[len(args)-1] == "--raw" {
			raw = append(raw, ' ')
		}
		return raw, err
	})
	if _, err := b.ResolveBases(context.Background(), "v1.2.3", strings.Repeat("a", 40)); err == nil {
		t.Fatal("trusted formatter digest instead of exact immutable bytes")
	}
}

func TestRecordNativeOriginalBuildAndSaveFacts(t *testing.T) {
	b, _ := resolver(t, baseFixture())
	c, err := b.ResolveBases(context.Background(), "v1.2.3", strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "original-pair.tar")
	if err := os.WriteFile(archive, []byte("original saved pair"), 0600); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]json.RawMessage{}
	for _, name := range []string{"backend", "web"} {
		metadata[name] = encoded(t, map[string]any{"containerimage.config.digest": dg("6"), "containerimage.digest": dg("6"), "buildx.build.ref": "fixture/native"})
	}
	calls := []string{}
	b.Runner = commandFunc(func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[1] == "info" {
			return []byte("aarch64\n"), nil
		}
		return []byte("actual fixture toolchain output\n"), nil
	})
	record, err := b.RecordBuild(context.Background(), c, "linux/arm64", metadata, archive)
	if err != nil || !record.NativeExecution || record.ImageArchive.SHA256 != strings.TrimPrefix(sha([]byte("original saved pair")), "sha256:") || record.ImageArchive.Size != 19 || !bytes.Equal(record.BuildMetadata["web"], metadata["web"]) || !strings.Contains(strings.Join(calls, "\n"), "--network none --platform linux/arm64 "+c.BaseImages["golang"]) {
		t.Fatal(record, err, calls)
	}
	if _, err := b.RecordBuild(context.Background(), c, "linux/amd64", metadata, archive); err == nil {
		t.Fatal("claimed emulation as native")
	}
	metadata["web"] = []byte(`{"containerimage.config.digest":"bad"}`)
	if _, err := b.RecordBuild(context.Background(), c, "linux/arm64", metadata, archive); err == nil {
		t.Fatal("accepted missing build identity")
	}
}

func gitFixture(t *testing.T) (Builder, func(...string) string) {
	t.Helper()
	root := t.TempDir()
	runner := process.Runner{Dir: root, Env: map[string]string{"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1"}}
	git := func(args ...string) string {
		t.Helper()
		raw, err := runner.Output(context.Background(), 10*time.Second, append([]string{"git"}, args...)...)
		if err != nil {
			t.Fatal(args, err)
		}
		return strings.TrimSpace(string(raw))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Candidate fixture")
	git("config", "user.email", "candidate@example.invalid")
	git("config", "core.hooksPath", os.DevNull)
	for _, name := range append(append([]string{}, contract.DeploymentRequiredFiles...), "deploy/update.py", "backend/licenses/example/LICENSE", ".env") {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("Fixture "+name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-qm", "Candidate fixture")
	commit := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/main", commit)
	git("tag", "v1.2.3")
	return Builder{Root: root, Runner: runner}, git
}

func TestExactTagAndPlannedMainIdentity(t *testing.T) {
	b, git := gitFixture(t)
	commit := git("rev-parse", "HEAD")
	for _, tag := range []string{"v1.2.3", "v1.2.4"} {
		event := commit
		if tag == "v1.2.4" {
			git("tag", "-a", tag, "-m", "Annotated fixture")
			event = git("rev-parse", "refs/tags/"+tag)
		}
		identity, err := b.ValidateTag(context.Background(), "refs/tags/"+tag, event)
		if err != nil || identity.Commit != commit || identity.SourceKind != "version-tag" || identity.PublicationAuthorized || identity.TaggedSourceCIVerified {
			t.Fatal(identity, err)
		}
	}
	planned, err := b.ValidateSource(context.Background(), "refs/heads/main", commit, "v1.2.5")
	if err != nil || !planned.PlannedCandidateOnly || planned.PublicationAuthorized || planned.TaggedSourceCIVerified || git("tag", "--list", "v1.2.5") != "" {
		t.Fatal(planned, err)
	}
	if _, err := b.ValidateSource(context.Background(), "refs/heads/main", commit, "v1.2.3"); err == nil {
		t.Fatal("borrowed an existing version tag")
	}
	for _, ref := range []string{"refs/heads/main", "refs/tags/v01.2.3", "refs/tags/v1.2.3;id"} {
		if _, err := b.ValidateTag(context.Background(), ref, commit); err == nil {
			t.Fatal("accepted invalid ref", ref)
		}
	}
	git("checkout", "-qb", "unreviewed")
	if err := os.WriteFile(filepath.Join(b.Root, "unreviewed"), []byte("unreviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "Unreviewed")
	other := git("rev-parse", "HEAD")
	git("tag", "v2.0.0")
	if _, err := b.ValidateTag(context.Background(), "refs/tags/v2.0.0", other); err == nil {
		t.Fatal("accepted off-main tag")
	}
	if _, err := b.ValidateTag(context.Background(), "refs/tags/v1.2.3", other); err == nil {
		t.Fatal("accepted wrong event object")
	}
}

func TestDeterministicV1BundleAndLegacyReader(t *testing.T) {
	b, git := gitFixture(t)
	commit := git("rev-parse", "HEAD")
	var previous []byte
	var artifact Bundle
	for i := 0; i < 2; i++ {
		var err error
		artifact, err = b.BuildBundle(context.Background(), BundleOptions{Output: filepath.Join(t.TempDir(), "output"), Version: "v1.2.3", Profile: "deployment-ready", ExpectedCommit: commit})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(artifact.Path)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && !bytes.Equal(raw, previous) {
			t.Fatal("bundle is not deterministic")
		}
		previous = raw
		m := contract.ManifestV1{Version: "v1.2.3", PayloadProfile: "deployment-ready", Source: contract.SourceV1{Commit: commit}, Bundle: contract.BundleV1{Name: filepath.Base(artifact.Path), SHA256: artifact.SHA256}}
		files, err := contract.ReadBundleV1(artifact.Path, m)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := files[".env"]; ok {
			t.Fatal("included private source outside allowlist")
		}
		if len(files) != len(contract.DeploymentRequiredFiles)+3 {
			t.Fatal("lost v1 compatibility/metadata/license members", len(files))
		}
		if _, err := b.BuildBundle(context.Background(), BundleOptions{Output: filepath.Dir(artifact.Path), Version: "v1.2.3", Profile: "deployment-ready"}); err == nil {
			t.Fatal("overwrote release artifact")
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("legacy Python compatibility reader unavailable")
	}
	parser, err := filepath.Abs("../../../release_artifacts.py")
	if err != nil {
		t.Fatal(err)
	}
	script := `import json,runpy,sys; from pathlib import Path; m=runpy.run_path(sys.argv[1]); m['validate_bundle']({'version':'v1.2.3','payload_profile':'deployment-ready','source':{'commit':sys.argv[3]},'bundle':{'name':Path(sys.argv[2]).name,'sha256':sys.argv[4]}},Path(sys.argv[2]))`
	if _, err := (process.Runner{}).Output(context.Background(), 10*time.Second, python, "-I", "-c", script, parser, artifact.Path, commit, artifact.SHA256); err != nil {
		t.Fatal("retained Python v1 reader rejected Go bundle", err)
	}
}

func TestRejectsDirtyStagedUntrackedLinkedAndWrongSourceBundle(t *testing.T) {
	for _, mode := range []string{"dirty", "staged-only", "untracked-license", "linked", "wrong-source", "missing-updater"} {
		t.Run(mode, func(t *testing.T) {
			b, git := gitFixture(t)
			name := filepath.Join(b.Root, "deploy/compose.release.yml")
			original, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			opts := BundleOptions{Output: filepath.Join(t.TempDir(), "output"), Version: "v1.2.3", Profile: "deployment-ready"}
			switch mode {
			case "dirty", "staged-only":
				if err := os.WriteFile(name, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				if mode == "staged-only" {
					git("add", "deploy/compose.release.yml")
					if err := os.WriteFile(name, original, 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "untracked-license":
				p := filepath.Join(b.Root, "backend/licenses/extra/NOTICE")
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("untracked"), 0600); err != nil {
					t.Fatal(err)
				}
			case "linked":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("release.env.example", name); err != nil {
					t.Fatal(err)
				}
			case "wrong-source":
				opts.ExpectedCommit = strings.Repeat("b", 40)
			case "missing-updater":
				git("rm", "-q", "deploy/update.py")
				git("commit", "-qm", "Remove compatibility updater")
			}
			if _, err := b.BuildBundle(context.Background(), opts); err == nil {
				t.Fatal("accepted wrong bundle source/state")
			}
		})
	}
}

func TestExpandedBundleBoundCountsHeadersAndPadding(t *testing.T) {
	b, git := gitFixture(t)
	p := filepath.Join(b.Root, "backend/licenses/large/LICENSE")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	// Payload fits the nominal limit; real USTAR headers/padding do not.
	if err := os.WriteFile(p, bytes.Repeat([]byte{'x'}, int(contract.MaxDeploymentBundleBytes)-2048), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "Large legal fixture")
	output := filepath.Join(t.TempDir(), "output")
	if _, err := b.BuildBundle(context.Background(), BundleOptions{Output: output, Version: "v1.2.3", Profile: "deployment-ready"}); err == nil {
		t.Fatal("bounded only visible member content, not tar expansion")
	}
	if _, err := os.Lstat(filepath.Join(output, "psst.zip-deployment-v1.2.3.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("created invalid output", err)
	}
}
