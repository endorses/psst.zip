package scanning

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSourceProducerUsesReadOnlyNativeFixtureAndOneRetainedNPMAudit(t *testing.T) {
	for _, failure := range []string{"", "process", "missing-receipt"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			commonDirectory := t.TempDir()
			output := filepath.Join(t.TempDir(), "new-measurement")
			files := map[string][]byte{"backend/go.mod": []byte("module example\n\ngo 1.23.0\n"), "backend/go.sum": {}, "backend/cmd/server/main.go": []byte("package main\nfunc main(){}\n"), "web/package.json": []byte("{\"name\":\"web\"}\n"), "web/package-lock.json": sourceFixtureJSON(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{"name": "web"}}})}
			for name, raw := range files {
				destination := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			git := func(args ...string) string {
				command := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null"}, args...)...)
				command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_COMMITTER_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
				raw, err := command.CombinedOutput()
				if err != nil {
					t.Fatal(err)
				}
				return strings.TrimSpace(string(raw))
			}
			git("init", "-q")
			git("add", ".")
			git("commit", "-qm", "Disposable source collector fixture")
			commit := git("rev-parse", "HEAD")
			os.WriteFile(filepath.Join(root, "untracked-fixture"), []byte("private untracked fixture"), 0600)
			identity := Source{"endorses/psst.zip", "v1.2.3", commit}
			platform := "linux/" + runtime.GOARCH
			if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
				t.Skip("native Linux source producer")
			}
			digest := "sha256:" + strings.Repeat("a", 64)
			goImage := "docker.io/library/golang@" + digest
			nodeImage := "docker.io/library/node@" + digest
			npm := sourceNPMFixture(false)
			inventory := map[string]string{}
			for name, raw := range npm {
				inventory[name] = hash(raw)
			}
			common := NPMMeasurement{SchemaVersion: 1, Kind: "common-npm-lock-scanner-measurement", Source: identity, Execution: "common-lock", Platform: platform, SourceInputs: map[string]string{"web/package.json": hash(files["web/package.json"]), "web/package-lock.json": hash(files["web/package-lock.json"])}, Builder: Builder{Reference: nodeImage, Config: digest, Architecture: runtime.GOARCH, Layers: []string{digest}, Daemon: runtime.GOARCH}, Started: "2026-10-24T00:00:00Z", Completed: "2026-10-24T00:00:01Z", RawFiles: inventory}
			npm["common-npm-audit.json"] = sourceFixtureJSON(common)
			for name, raw := range npm {
				if err := os.WriteFile(filepath.Join(commonDirectory, name), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			runs, cleanup := 0, false
			cid := strings.Repeat("c", 64)
			execute := func(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
				if len(args) < 2 || args[0] != "docker" {
					t.Fatal("unexpected source fixture command")
				}
				switch args[1] {
				case "info":
					return json.Marshal(runtime.GOARCH)
				case "pull":
					return nil, nil
				case "image":
					return sourceFixtureJSON([]map[string]any{{"Id": digest, "Os": "linux", "Architecture": runtime.GOARCH, "RootFS": map[string]any{"Layers": []string{digest}}}}), nil
				case "container":
					if args[len(args)-1] != cid {
						t.Fatal("cleanup targeted an unowned container")
					}
					cleanup = true
					return nil, nil
				case "run":
					runs++
					line := strings.Join(args, " ")
					for _, required := range []string{"--read-only", "--cap-drop ALL", "--security-opt no-new-privileges", "--user ", "--pids-limit 256", "--memory 3g", "--cpus 2", GovulnVersion, GovulnSum, "GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org", "GOVULNDB=https://vuln.go.dev"} {
						if !strings.Contains(line, required) {
							t.Fatalf("source fixture lacks %s", required)
						}
					}
					for _, forbidden := range []string{"docker.sock", "--privileged", "GH_TOKEN", "GITHUB_TOKEN"} {
						if strings.Contains(line, forbidden) {
							t.Fatalf("source fixture contains %s", forbidden)
						}
					}
					reports, sourceRoot := "", ""
					for i, arg := range args {
						if arg == "--cidfile" {
							if err := os.WriteFile(args[i+1], []byte(cid), 0600); err != nil {
								t.Fatal(err)
							}
						}
						if strings.HasPrefix(arg, "type=bind,src=") {
							parts := strings.Split(strings.TrimPrefix(arg, "type=bind,src="), ",")
							if strings.Contains(arg, ",dst=/reports") {
								reports = parts[0]
							} else if strings.HasSuffix(arg, ",dst=/build,readonly") {
								sourceRoot = parts[0]
							}
						}
					}
					if reports == "" || sourceRoot == "" {
						t.Fatal("source fixture mount policy differs")
					}
					if _, err := os.Stat(filepath.Join(sourceRoot, "untracked-fixture")); !os.IsNotExist(err) {
						t.Fatal("untracked data entered committed scan fixture")
					}
					if failure == "process" {
						return nil, errors.New("actual fixture command failed")
					}
					raw, _ := sourceGoFixture(platform)
					if failure == "missing-receipt" {
						delete(raw, "go-execution.complete")
					}
					for name, body := range raw {
						if err := os.WriteFile(filepath.Join(reports, name), body, 0600); err != nil {
							t.Fatal(err)
						}
					}
					return nil, nil
				default:
					t.Fatal("unexpected Docker source fixture operation")
					return nil, nil
				}
			}
			result, err := MeasureSources(context.Background(), SourceOptions{Root: root, Source: identity, Platform: platform, GoImage: goImage, NodeImage: nodeImage, CommonNPM: commonDirectory, Output: output, Execute: execute})
			if runs != 1 || !cleanup {
				t.Fatal("source producer duplicated npm execution or failed owned cleanup")
			}
			if failure != "" {
				if err == nil {
					t.Fatal("failed scanner emitted completion")
				}
				if _, err := os.Stat(filepath.Join(output, "source-scan-measurement.json")); !os.IsNotExist(err) {
					t.Fatal("failed source fixture retained a completed wrapper")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Schema != 2 || result.Execution != "native-go-common-lock" || len(result.Scans) != 2 || result.Authorized || !result.Pending {
				t.Fatal("source producer changed its approval boundary")
			}
		})
	}
}
