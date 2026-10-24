package scanning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"reflect"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const CompilerVersion = "go1.27.3"
const scannerRawLimit = 32 << 20

type GraphModule struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Sum     string `json:"sum"`
}

type CompilerGraph struct {
	Root           string            `json:"root_target"`
	Import         string            `json:"root_import_path"`
	RawFile        string            `json:"raw_file"`
	SHA256         string            `json:"sha256"`
	PackagesSHA256 string            `json:"package_paths_sha256"`
	Count          int               `json:"package_count"`
	Modules        []GraphModule     `json:"modules"`
	Environment    map[string]string `json:"build_environment"`
	Rebuilt        *string           `json:"rebuilt_sha256"`
	Correspondence string            `json:"executable_correspondence"`
}

type packageRecord struct {
	ImportPath string
	Module     *debug.Module
	Incomplete bool
	Error      json.RawMessage
	DepsErrors json.RawMessage
	ForTest    string
}

// GraphPaths rejects partial/error/test graphs rather than inferring absence
// from a subset of the actual compiler-selected packages.
func GraphPaths(raw []byte) ([]string, map[string]GraphModule, error) {
	if len(raw) == 0 || len(raw) > scannerRawLimit {
		return nil, nil, fmt.Errorf("compiler graph is missing or exceeds bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	paths := map[string]bool{}
	modules := map[string]GraphModule{}
	for count := 0; ; count++ {
		var record json.RawMessage
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil || count >= 100000 {
			return nil, nil, fmt.Errorf("compiler graph stream is invalid or exceeds bounds")
		}
		var fields map[string]any
		if err = contract.Decode(record, scannerRawLimit, &fields); err != nil {
			return nil, nil, err
		}
		var entry packageRecord
		if json.Unmarshal(record, &entry) != nil || !validImport(entry.ImportPath) || paths[entry.ImportPath] || entry.Incomplete || presentJSON(entry.Error) || presentJSON(entry.DepsErrors) || entry.ForTest != "" {
			return nil, nil, fmt.Errorf("compiler graph is incomplete, duplicated or includes test packages")
		}
		paths[entry.ImportPath] = true
		if entry.Module != nil {
			m := entry.Module
			original := object(fields["Module"])
			if !validImport(m.Path) || m.Replace != nil || original["Error"] != nil {
				return nil, nil, fmt.Errorf("compiler module replacement/error requires review")
			}
			item := GraphModule{m.Path, m.Version, m.Sum}
			if previous, ok := modules[m.Path]; ok && previous != item {
				return nil, nil, fmt.Errorf("compiler graph has inconsistent module versions/checksums")
			}
			modules[m.Path] = item
		}
	}
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("empty compiler package graph")
	}
	result := make([]string, 0, len(paths))
	for name := range paths {
		result = append(result, name)
	}
	slices.Sort(result)
	return result, modules, nil
}
func presentJSON(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte("[]"))
}
func validImport(name string) bool {
	return name != "" && len(name) < 4096 && !strings.HasPrefix(name, "/") && !strings.Contains(name, "\\") && !strings.Contains(name, "//") && path.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\x00\r\n\t ")
}

func buildSettings(build *debug.BuildInfo) (map[string]string, error) {
	settings := map[string]string{}
	for _, s := range build.Settings {
		if _, ok := settings[s.Key]; ok || s.Key == "" {
			return nil, fmt.Errorf("duplicate/incomplete executable build settings")
		}
		settings[s.Key] = s.Value
	}
	return settings, nil
}

// CompilerScript uses the actual ELF's supported build settings. Backend
// reproduction must byte-match; the original signed Caddy vendor tree retains
// its original compiler independently of the current application compiler.
func CompilerScript(component string, settings map[string]string) (string, error) {
	if component != "backend" && component != "web" {
		return "", fmt.Errorf("unknown compiler component")
	}
	if settings["GOEXPERIMENT"] != "" || settings["-buildmode"] != "" && settings["-buildmode"] != "exe" || settings["-compiler"] != "" && settings["-compiler"] != "gc" {
		return "", fmt.Errorf("unsupported executable package-selection settings")
	}
	for name := range settings {
		if strings.HasPrefix(name, "-") && !slices.Contains([]string{"-buildmode", "-compiler", "-tags", "-trimpath", "-ldflags"}, name) {
			return "", fmt.Errorf("unreviewed executable build setting")
		}
	}
	arch := settings["GOARCH"]
	if settings["CGO_ENABLED"] != "0" || settings["GOOS"] != "linux" || arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported executable platform/CGO")
	}
	variant, key := "v1", "GOAMD64"
	if arch == "arm64" {
		variant, key = "v8.0", "GOARM64"
	}
	if value := settings[key]; value != "" && value != variant {
		return "", fmt.Errorf("unsupported executable architecture variant")
	}
	tags := ""
	if component == "web" {
		tags = "nobadger,nomysql,nopgx"
	}
	if settings["-tags"] != tags {
		return "", fmt.Errorf("unreviewed executable build tags")
	}
	script := `set -eu
export PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export GOTOOLCHAIN=local GOPATH=/go GOMODCACHE=/go/pkg/mod GOCACHE=/tmp/build GOWORK=off
export GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org GOPRIVATE= GONOPROXY= GONOSUMDB=
export CGO_ENABLED=0 GOOS=linux GOFLAGS= GOEXPERIMENT=
cd /build
go version > /reports/go-version.txt
go version -m -json /reports/runtime-binary > /reports/binary-build-info.json
`
	script += "export GOARCH=" + arch + " " + key + "=" + variant + "\n"
	if component == "web" {
		script += "export GOFLAGS=-mod=vendor\ngo list -deps -json -buildvcs=false -tags=nobadger,nomysql,nopgx . > /reports/compiler-graph.json\n"
	} else {
		script += "go mod download\ngo list -deps -json ./cmd/server > /reports/compiler-graph.json\ngo build -o /reports/rebuilt-backend ./cmd/server\nsha256sum /reports/rebuilt-backend > /reports/rebuilt-backend-sha256.txt\n"
	}
	script += "go env -json GOOS GOARCH CGO_ENABLED GOAMD64 GOARM64 GOFLAGS GOEXPERIMENT GOTOOLCHAIN GOPATH GOMODCACHE GOSUMDB GOPROXY > /reports/compiler-environment.json\nprintf 'complete\\n' > /reports/compiler-execution.complete\n"
	return script, nil
}

// CompilerCorrespondence proves the package graph was produced with the exact
// final executable's toolchain/configuration and complete dependency module set.
func CompilerCorrespondence(raw map[string][]byte, component, platform string, binary []byte, expected *debug.BuildInfo, vendorSum, vendorModules []byte) (CompilerGraph, error) {
	var result CompilerGraph
	if expected == nil || !bytes.Equal(raw["compiler-execution.complete"], []byte("complete\n")) {
		return result, fmt.Errorf("incomplete compiler execution")
	}
	var actual debug.BuildInfo
	// go version -m JSON has an additional Path field only on newer Go versions;
	// decode actual exported BuildInfo fields and reject duplicate JSON keys.
	var generic map[string]any
	if contract.Decode(raw["binary-build-info.json"], scannerRawLimit, &generic) != nil || json.Unmarshal(raw["binary-build-info.json"], &actual) != nil || !reflect.DeepEqual(actual, *expected) {
		return result, fmt.Errorf("compiler executable metadata differs from final image")
	}
	if component == "backend" && actual.GoVersion != CompilerVersion {
		return result, fmt.Errorf("backend compiler differs from pinned application producer")
	}
	settings, err := buildSettings(&actual)
	if err != nil {
		return result, err
	}
	if _, err = CompilerScript(component, settings); err != nil {
		return result, err
	}
	if platform != "linux/"+settings["GOARCH"] {
		return result, fmt.Errorf("compiler platform differs")
	}
	var env map[string]string
	if contract.Decode(raw["compiler-environment.json"], scannerRawLimit, &env) != nil || env["GOEXPERIMENT"] != "" || env["GOTOOLCHAIN"] != "local" || strings.TrimSpace(string(raw["go-version.txt"])) != "go version "+actual.GoVersion+" "+platform {
		return result, fmt.Errorf("compiler environment/toolchain differs")
	}
	for _, key := range []string{"GOOS", "GOARCH", "CGO_ENABLED", "GOAMD64", "GOARM64"} {
		if value, ok := settings[key]; ok && env[key] != value {
			return result, fmt.Errorf("compiler environment differs from executable build settings")
		}
	}
	paths, modules, err := GraphPaths(raw["compiler-graph.json"])
	if err != nil {
		return result, err
	}
	if !slices.Contains(paths, actual.Path) || actual.Main.Path == "" {
		return result, fmt.Errorf("compiler root missing from graph")
	}
	delete(modules, actual.Main.Path)
	if len(modules) != len(actual.Deps) {
		return result, fmt.Errorf("compiler dependency module set differs from executable")
	}
	declared := make([]GraphModule, 0, len(actual.Deps))
	seen := map[string]bool{}
	for _, dep := range actual.Deps {
		if dep == nil || dep.Replace != nil || seen[dep.Path] || !regexp.MustCompile(`^h1:[A-Za-z0-9+/]{43}=$`).MatchString(dep.Sum) {
			return result, fmt.Errorf("executable has missing/replaced/duplicate module checksum")
		}
		seen[dep.Path] = true
		m, ok := modules[dep.Path]
		if !ok || m.Version != dep.Version || component == "backend" && m.Sum != dep.Sum {
			return result, fmt.Errorf("compiler selected module version/checksum differs")
		}
		declared = append(declared, GraphModule{dep.Path, dep.Version, dep.Sum})
	}
	var rebuilt *string
	correspondence := "authenticated-original-vendor-source-and-signed-executable"
	root := "."
	if component == "web" {
		if env["GOFLAGS"] != "-mod=vendor" || len(vendorSum) == 0 || len(vendorModules) == 0 {
			return result, fmt.Errorf("Caddy graph lacks exact vendor source")
		}
		if err = verifyCompilerVendor(&actual, vendorSum, vendorModules); err != nil {
			return result, err
		}
	} else {
		fields := strings.Fields(string(raw["rebuilt-backend-sha256.txt"]))
		if len(fields) != 2 || "sha256:"+fields[0] != hash(binary) || fields[1] != "/reports/rebuilt-backend" {
			return result, fmt.Errorf("rebuilt backend does not byte-match final image executable")
		}
		if env["GOFLAGS"] != "" || env["GOPATH"] != "/go" || env["GOMODCACHE"] != "/go/pkg/mod" {
			return result, fmt.Errorf("backend rebuild differs from release builder layout")
		}
		value := hash(binary)
		rebuilt = &value
		root = "./cmd/server"
		correspondence = "byte-identical-same-builder-rebuild"
	}
	return CompilerGraph{root, actual.Path, "compiler-graph.json", hash(raw["compiler-graph.json"]), hash(legacyJSON(paths)), len(paths), declared, env, rebuilt, correspondence}, nil
}
func verifyCompilerVendor(build *debug.BuildInfo, sums, modules []byte) error {
	declared := map[string]bool{}
	checks := map[string]bool{}
	for _, line := range strings.Split(string(sums), "\n") {
		checks[line] = true
	}
	for _, line := range strings.Split(string(modules), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "#" {
			declared[fields[1]+" "+fields[2]] = true
		}
	}
	for _, dep := range build.Deps {
		key := dep.Path + " " + dep.Version
		if !declared[key] || !checks[key+" "+dep.Sum] {
			return fmt.Errorf("Caddy vendor module checksum/declaration differs from executable")
		}
	}
	return nil
}
