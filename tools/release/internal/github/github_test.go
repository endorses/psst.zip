package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type executor func(context.Context, time.Duration, ...string) ([]byte, error)

func (f executor) Output(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	return f(ctx, timeout, args...)
}
func encoded(value any) ([]byte, error) { return json.Marshal(value) }
func endpoint(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "repos/") {
			return arg
		}
	}
	return ""
}
func exactRun() WorkflowRun {
	run := WorkflowRun{ID: 12, Attempt: 2, Revision: strings.Repeat("a", 40), Branch: "main", Event: "push", Path: ".github/workflows/ci.yml", Conclusion: "success"}
	run.Repository.Name, run.HeadRepository.Name = "endorses/psst.zip", "endorses/psst.zip"
	return run
}
func successfulJob(name string, steps []string) Job {
	job := Job{ID: 7, Name: name, Status: "completed", Conclusion: "success"}
	for _, name := range steps {
		job.Steps = append(job.Steps, Step{name, "success", "2026-10-24T00:00:00Z", "2026-10-24T00:00:01Z"})
	}
	return job
}

func TestCIRejectsForeignSkippedAndAmbiguousEvidence(t *testing.T) {
	run := exactRun()
	job := successfulJob("CLI", []string{"crypto"})
	if !run.Exact("endorses/psst.zip", run.Revision) || len(MatchJobs(run, []Job{job}, map[string][]string{"CLI": {"crypto"}})) != 1 {
		t.Fatal("valid exact executed evidence rejected")
	}
	foreign := run
	foreign.HeadRepository.Name = "fork/psst.zip"
	if foreign.Exact("endorses/psst.zip", run.Revision) {
		t.Fatal("fork provenance reused")
	}
	job.Steps[0].Conclusion = "skipped"
	if Executed(job, []string{"crypto"}) {
		t.Fatal("skipped check reused")
	}
	job.Steps[0].Conclusion = "success"
	if len(MatchJobs(run, []Job{job, job}, map[string][]string{"CLI": {"crypto"}})) != 0 {
		t.Fatal("ambiguous duplicate jobs reused")
	}
	job.Steps = append(job.Steps, job.Steps[0])
	if Executed(job, []string{"crypto"}) {
		t.Fatal("duplicate named steps reused")
	}
}

func TestCLISourceBindsDispatcherTagMainAndExecutedDefinition(t *testing.T) {
	revision, repository, tag := strings.Repeat("a", 40), "endorses/psst.zip", "client/v1.2.3"
	run := exactRun()
	required := map[string][]string{
		"Repository security": {"Scan complete Git history", "Repository check regression tests"},
		"Go client and CLI":   {"Vet Go client", "Client and CLI race tests", "Build and smoke-test CLI executable once", "Check resolved CLI dependency notices", "Disposable backend and browser crypto interoperability", "Prepare matching embedded LAN browser assets", "Embedded LAN Send and Receive browser interoperability"},
	}
	jobs := []Job{}
	for name, steps := range required {
		jobs = append(jobs, successfulJob(name, steps))
	}
	differentWorkflow, movedTag := false, false
	client := Client{executor(func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
		if args[0] == "git" {
			switch args[1] {
			case "rev-parse":
				return []byte(revision), nil
			case "merge-base", "cat-file":
				return nil, nil
			case "show":
				if differentWorkflow && strings.HasPrefix(args[2], revision+":") {
					return []byte("untrusted checks"), nil
				}
				return []byte("trusted checks"), nil
			}
		}
		switch path := endpoint(args); {
		case strings.Contains(path, "/git/ref/"):
			target := revision
			if movedTag {
				target = strings.Repeat("b", 40)
			}
			return encoded(map[string]any{"object": gitObject{"commit", target}})
		case strings.HasSuffix(path, "/branches/main"):
			return []byte(`{"name":"main","protected":true}`), nil
		case strings.Contains(path, "/workflows/ci.yml/runs?"):
			return encoded(map[string]any{"workflow_runs": []WorkflowRun{run}})
		case strings.Contains(path, "/attempts/2/jobs?"):
			return encoded(map[string]any{"jobs": jobs})
		}
		return nil, errors.New("unexpected fixture operation")
	})}
	result, err := client.CLISource(context.Background(), repository, tag, "refs/heads/main", revision)
	if err != nil || result.Revision != revision || result.Version != "1.2.3" || result.RunID != 12 {
		t.Fatalf("valid source: %+v %v", result, err)
	}
	differentWorkflow = true
	if _, err = client.CLISource(context.Background(), repository, tag, "refs/heads/main", revision); err == nil {
		t.Fatal("different executed CI definition accepted")
	}
	differentWorkflow, movedTag = false, true
	if _, err = client.CLISource(context.Background(), repository, tag, "refs/heads/main", revision); err == nil {
		t.Fatal("moved remote tag accepted")
	}
}

func cliFixture(t *testing.T) (string, string, []Asset) {
	t.Helper()
	directory := t.TempDir()
	version, revision := "1.2.3", strings.Repeat("a", 40)
	names := []string{"psst-1.2.3-source.tar.gz", "psst-1.2.3-linux-amd64.tar.gz", "psst-1.2.3-linux-arm64.tar.gz", "psst-1.2.3-darwin-amd64.tar.gz", "psst-1.2.3-darwin-arm64.tar.gz", "psst-1.2.3-windows-amd64.zip", "psst-1.2.3-windows-arm64.zip", "cli-release.json"}
	var sums strings.Builder
	for _, name := range names {
		data := []byte("disposable archive fixture " + name)
		if name == "cli-release.json" {
			data = []byte(fmt.Sprintf(`{"schema_version":1,"tag":"client/v%s","version":"%s","revision":"%s","development_only":false,"source_dirty":false}`, version, version, revision))
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(hash[:]), name)
	}
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(sums.String()), 0600); err != nil {
		t.Fatal(err)
	}
	private, assets, err := CLIAssets(directory, version, revision)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(private) })
	return directory, private, assets
}

func TestCLISnapshotRejectsExtraAndTamperedAssets(t *testing.T) {
	directory, private, assets := cliFixture(t)
	name := assets[0].Name
	if err := os.WriteFile(filepath.Join(directory, name), []byte("changed after snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(private, name)); string(data) == "changed after snapshot" {
		t.Fatal("snapshot followed mutable source")
	}
	if _, _, err := CLIAssets(directory, "1.2.3", strings.Repeat("a", 40)); err == nil {
		t.Fatal("tampered checksum accepted")
	}
	if err := os.WriteFile(filepath.Join(directory, "extra.txt"), []byte("extra"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CLIAssets(directory, "1.2.3", strings.Repeat("a", 40)); err == nil {
		t.Fatal("unexpected asset accepted")
	}
}

func TestDraftReadbackBeforePublicationAndPartialReceipt(t *testing.T) {
	for _, failure := range []string{"", "upload", "readback", "final"} {
		t.Run("failure-"+failure, func(t *testing.T) {
			_, _, assets := cliFixture(t)
			plan := Publication{Repository: "endorses/psst.zip", Tag: "client/v1.2.3", Revision: strings.Repeat("a", 40), Title: "fixture", Notes: "fixture notes", Receipt: filepath.Join(t.TempDir(), "receipt.json"), Assets: assets}
			created, published, downloads := false, false, 0
			release := Release{ID: 99, Tag: plan.Tag, Draft: true}
			for index, asset := range assets {
				release.Assets = append(release.Assets, ReleaseAsset{int64(index + 1), asset.Name, asset.Size, "sha256:" + asset.SHA256, "uploaded"})
			}
			client := Client{executor(func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
				if args[1] == "release" {
					switch args[2] {
					case "create":
						created = true
						if failure == "upload" {
							return nil, errors.New("private token must not escape")
						}
						return nil, nil
					case "download":
						if published {
							t.Fatal("published before private download readback")
						}
						var directory, name string
						for i := 0; i < len(args)-1; i++ {
							if args[i] == "--dir" {
								directory = args[i+1]
							}
							if args[i] == "--pattern" {
								name = args[i+1]
							}
						}
						for _, asset := range assets {
							if asset.Name == name {
								data, err := os.ReadFile(asset.Path)
								if err != nil {
									return nil, err
								}
								if failure == "readback" {
									data = []byte("wrong bytes")
								}
								downloads++
								return nil, os.WriteFile(filepath.Join(directory, name), data, 0600)
							}
						}
					case "edit":
						if downloads != len(assets) {
							t.Fatal("publication skipped complete asset byte readback")
						}
						published, release.Draft, release.Immutable = true, false, true
						return nil, nil
					}
				}
				switch path := endpoint(args); {
				case strings.Contains(path, "/releases?per_page="):
					return []byte(`[]`), nil
				case strings.HasSuffix(path, "/immutable-releases"):
					return []byte(`{"enabled":true}`), nil
				case strings.Contains(path, "/git/ref/"):
					return encoded(map[string]any{"object": gitObject{"commit", plan.Revision}})
				case strings.Contains(path, "/releases/tags/"):
					if !created {
						t.Fatal("read release before draft creation")
					}
					if published && failure == "final" {
						release.Immutable = false
					}
					return encoded(release)
				}
				return nil, errors.New("unexpected fixture operation")
			})}
			plan.Inspector = &client
			result, err := client.PublishNew(context.Background(), plan)
			if (err == nil) != (failure == "") || result.Complete != (failure == "") {
				t.Fatalf("completion=%t err=%v", result.Complete, err)
			}
			if err != nil && strings.Contains(err.Error(), "private token") {
				t.Fatal("private child diagnostics leaked")
			}
			if (failure == "upload" || failure == "readback") && published {
				t.Fatal("incomplete draft published")
			}
			data, readErr := os.ReadFile(plan.Receipt)
			if readErr != nil || strings.Contains(string(data), "private token") || strings.Contains(string(data), assets[0].Path) {
				t.Fatal("receipt missing or contains private data")
			}
			if _, err = client.PublishNew(context.Background(), plan); err == nil {
				t.Fatal("partial receipt reused automatically")
			}
		})
	}
}
