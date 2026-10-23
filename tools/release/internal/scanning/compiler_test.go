package scanning

import (
	"archive/tar"
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func compilerFixture(t *testing.T) (map[string][]byte, []byte, *debug.BuildInfo) {
	t.Helper()
	binary := []byte("disposable actual native executable")
	sum := "h1:" + strings.Repeat("A", 43) + "="
	build := &debug.BuildInfo{GoVersion: CompilerVersion, Path: "example.invalid/tool/cmd/server", Main: debug.Module{Path: "example.invalid/tool", Version: "(devel)"}, Deps: []*debug.Module{{Path: "example.invalid/module", Version: "v1.0.0", Sum: sum}}, Settings: []debug.BuildSetting{{Key: "GOOS", Value: "linux"}, {Key: "GOARCH", Value: "amd64"}, {Key: "GOAMD64", Value: "v1"}, {Key: "CGO_ENABLED", Value: "0"}}}
	encoded, _ := contract.JSON(build)
	env, _ := contract.JSON(map[string]string{"GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1", "CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOFLAGS": "", "GOPATH": "/go", "GOMODCACHE": "/go/pkg/mod"})
	var graph bytes.Buffer
	json.NewEncoder(&graph).Encode(map[string]any{"ImportPath": build.Path, "Module": map[string]any{"Path": build.Main.Path, "Main": true}})
	json.NewEncoder(&graph).Encode(map[string]any{"ImportPath": "example.invalid/module/safe", "Module": map[string]any{"Path": build.Deps[0].Path, "Version": build.Deps[0].Version, "Sum": sum}})
	return map[string][]byte{"compiler-execution.complete": []byte("complete\n"), "binary-build-info.json": encoded, "go-version.txt": []byte("go version " + CompilerVersion + " linux/amd64\n"), "compiler-environment.json": env, "compiler-graph.json": graph.Bytes(), "rebuilt-backend-sha256.txt": []byte(strings.TrimPrefix(hash(binary), "sha256:") + "  /reports/rebuilt-backend\n")}, binary, build
}
func TestCompilerRequiresActualBinaryAndCompleteSelectedGraph(t *testing.T) {
	raw, binary, build := compilerFixture(t)
	graph, err := CompilerCorrespondence(raw, "backend", "linux/amd64", binary, build, nil, nil)
	if err != nil || graph.Count != 2 || graph.Rebuilt == nil || *graph.Rebuilt != hash(binary) {
		t.Fatalf("complete graph rejected: %v", err)
	}
	for _, mutation := range []struct {
		name   string
		change func(map[string][]byte, *debug.BuildInfo)
	}{
		{"changed executable", func(raw map[string][]byte, _ *debug.BuildInfo) {
			raw["rebuilt-backend-sha256.txt"] = []byte(strings.Repeat("f", 64) + "  /reports/rebuilt-backend\n")
		}},
		{"partial module set", func(raw map[string][]byte, _ *debug.BuildInfo) {
			raw["compiler-graph.json"] = bytes.SplitN(raw["compiler-graph.json"], []byte("\n"), 2)[0]
		}},
		{"changed environment", func(raw map[string][]byte, _ *debug.BuildInfo) {
			raw["compiler-environment.json"] = bytes.ReplaceAll(raw["compiler-environment.json"], []byte(`"CGO_ENABLED": "0"`), []byte(`"CGO_ENABLED": "1"`))
		}},
		{"old app compiler", func(raw map[string][]byte, b *debug.BuildInfo) {
			b.GoVersion = "go1.27.2"
			raw["binary-build-info.json"], _ = contract.JSON(b)
			raw["go-version.txt"] = []byte("go version go1.27.2 linux/amd64\n")
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			raw, binary, build := compilerFixture(t)
			mutation.change(raw, build)
			if _, err := CompilerCorrespondence(raw, "backend", "linux/amd64", binary, build, nil, nil); err == nil {
				t.Fatal("accepted altered compiler correspondence")
			}
		})
	}
}
func TestCaddyRetainsOriginalCompilerAndExactVendorTags(t *testing.T) {
	raw, binary, build := compilerFixture(t)
	build.GoVersion = "go1.27.2"
	build.Settings = append(build.Settings, debug.BuildSetting{Key: "-tags", Value: "nobadger,nomysql,nopgx"})
	raw["binary-build-info.json"], _ = contract.JSON(build)
	raw["go-version.txt"] = []byte("go version go1.27.2 linux/amd64\n")
	raw["compiler-environment.json"] = bytes.ReplaceAll(raw["compiler-environment.json"], []byte(`"GOFLAGS": ""`), []byte(`"GOFLAGS": "-mod=vendor"`))
	dep := build.Deps[0]
	sum := []byte(dep.Path + " " + dep.Version + " " + dep.Sum + "\n")
	modules := []byte("# " + dep.Path + " " + dep.Version + "\n")
	if _, err := CompilerCorrespondence(raw, "web", "linux/amd64", binary, build, sum, modules); err != nil {
		t.Fatal(err)
	}
	if _, err := CompilerCorrespondence(raw, "web", "linux/amd64", binary, build, []byte("different"), modules); err == nil {
		t.Fatal("accepted different vendor checksum")
	}
	settings, _ := buildSettings(build)
	settings["-race"] = "true"
	if _, err := CompilerScript("web", settings); err == nil {
		t.Fatal("accepted unsupported package-selection flags")
	}
}
func TestGraphPathsRejectsErrorsTestsReplacementsAndDuplicateJSON(t *testing.T) {
	for _, raw := range []string{`{"ImportPath":"safe","Incomplete":true}`, `{"ImportPath":"safe","ForTest":"safe"}`, `{"ImportPath":"safe","DepsErrors":[{"Err":"missing"}]}`, `{"ImportPath":"safe","Module":{"Path":"safe","Replace":{"Path":"other"}}}`, `{"ImportPath":"safe","ImportPath":"other"}`, "{\"ImportPath\":\"safe\"}\n{\"ImportPath\":\"safe\"}", `{"ImportPath":"../outside"}`} {
		if _, _, err := GraphPaths([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid graph %s", raw)
		}
	}
}
func TestCompilerSourceExtractionRejectsLinksTraversalDuplicatesAndExpansion(t *testing.T) {
	for _, kind := range []string{"symlink", "traversal", "duplicate", "expanded"} {
		t.Run(kind, func(t *testing.T) {
			var raw bytes.Buffer
			writer := tar.NewWriter(&raw)
			name := "safe/file"
			flag := byte(tar.TypeReg)
			link := ""
			size := int64(1)
			if kind == "symlink" {
				flag = tar.TypeSymlink
				link = "/outside"
				size = 0
			}
			if kind == "traversal" {
				name = "../outside"
			}
			h := &tar.Header{Name: name, Typeflag: flag, Linkname: link, Size: size, Mode: 0777}
			writer.WriteHeader(h)
			if size > 0 {
				writer.Write([]byte("x"))
			}
			if kind == "duplicate" {
				writer.WriteHeader(h)
				writer.Write([]byte("x"))
			}
			writer.Close()
			limit := int64(1 << 20)
			if kind == "expanded" {
				limit = 512
			}
			if err := extractCompilerTar(bytes.NewReader(raw.Bytes()), t.TempDir(), limit, ""); err == nil {
				t.Fatal("accepted unsafe/oversized source")
			}
		})
	}
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	writer.WriteHeader(&tar.Header{Name: "source/", Typeflag: tar.TypeDir, Mode: 0777})
	writer.WriteHeader(&tar.Header{Name: "source/main.go", Typeflag: tar.TypeReg, Mode: 0777, Size: 1})
	writer.Write([]byte("x"))
	writer.Close()
	root := t.TempDir()
	if err := extractCompilerTar(bytes.NewReader(raw.Bytes()), root, 1<<20, ""); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "source/main.go"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("retained unsafe source permissions")
	}
}

func TestCompilerExtractsExactGitSnapshotWithItsGlobalCommitMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "backend"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backend", "go.mod"), []byte("module example.invalid/disposable\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) []byte {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		raw, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("disposable git snapshot failed: %v", err)
		}
		return raw
	}
	git("init", "-q")
	git("add", "backend")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath="+os.DevNull, "commit", "-qm", "Disposable compiler source")
	commit := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	archive := git("archive", "--format=tar", commit, "backend")
	output := t.TempDir()
	if err := extractCompilerTar(bytes.NewReader(archive), output, 1<<20, commit); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "backend/go.mod")); err != nil {
		t.Fatal(err)
	}
	if err := extractCompilerTar(bytes.NewReader(archive), t.TempDir(), 1<<20, strings.Repeat("f", 40)); err == nil {
		t.Fatal("accepted different Git global commit metadata")
	}
}

func TestCompilerMetadataMatchesIndependentGoELFInspector(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	build, err := buildinfo.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "version", "-m", "-json", executable)
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOENV=off", "GOWORK=off")
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("independent compiler metadata inspection failed: %v", err)
	}
	var actual debug.BuildInfo
	if err = json.Unmarshal(raw, &actual); err != nil || !reflect.DeepEqual(actual, *build) {
		t.Fatalf("actual Go JSON and direct ELF metadata differ: %v", err)
	}
}
