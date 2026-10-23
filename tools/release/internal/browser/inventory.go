// Package browser verifies observations emitted by the real Node/Vite toolchain.
// Input/output correspondence is not a claim of transformed-byte reproduction.
package browser

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const inventoryPath = "licenses/browser-module-inventory.json"
const maximumFile = 32 << 20
const maximumTotal = 512 << 20
const maximumModules = 25000

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

type File struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Output struct {
	File   string `json:"file"`
	Type   string `json:"type"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Package struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Integrity   string `json:"integrity"`
	LockPath    string `json:"lock_path"`
	PackageJSON string `json:"package_json_sha256"`
}
type Contribution struct {
	File   string `json:"file"`
	Length int64  `json:"rendered_length"`
}
type Module struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Path      *string        `json:"module_path"`
	Source    *string        `json:"source_sha256"`
	Package   *Package       `json:"package"`
	Transform *string        `json:"transform_input_sha256"`
	Rollup    *string        `json:"rollup_input_sha256"`
	Rendered  []Contribution `json:"rendered_in"`
	Reason    string         `json:"reason,omitempty"`
}
type Observation struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Source        struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
		Lock     struct {
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
		} `json:"package_lock"`
	} `json:"source"`
	Execution  string            `json:"execution"`
	Outputs    []Output          `json:"outputs"`
	Metadata   []Output          `json:"build_metadata_outputs"`
	Modules    []json.RawMessage `json:"modules"`
	Excluded   []json.RawMessage `json:"excluded_modules"`
	Reproduced bool              `json:"source_reproduction_verified"`
	Closure    bool              `json:"browser_module_closure_verified"`
	Authorized bool              `json:"publication_authorized"`
}
type Facts struct {
	SchemaVersion int                `json:"schema_version"`
	Kind          string             `json:"kind"`
	Version       string             `json:"version"`
	Revision      string             `json:"revision"`
	Inventory     string             `json:"inventory_sha256"`
	Lock          string             `json:"package_lock_sha256"`
	Inputs        map[string]File    `json:"inputs"`
	Outputs       map[string]File    `json:"outputs"`
	Metadata      map[string]File    `json:"build_metadata_outputs"`
	Packages      map[string]Package `json:"packages"`
	Unattributed  map[string]File    `json:"unattributed_javascript_outputs"`
	Physical      int                `json:"physical_modules"`
	Virtual       int                `json:"virtual_modules"`
	Rendered      int                `json:"rendered_modules"`
	Excluded      int                `json:"excluded_modules"`
	Reproduced    bool               `json:"source_reproduction_verified"`
	Closure       bool               `json:"browser_module_closure_verified"`
	Authorized    bool               `json:"publication_authorized"`
}

func digest(data []byte) string {
	value := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(value[:])
}
func fileFact(data []byte) File { return File{digest(data), int64(len(data))} }

func safe(name string) bool {
	return name != "" && len(name) <= 4096 && path.Clean(name) == name && !path.IsAbs(name) && name != "." && name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\?#") && strings.IndexFunc(name, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func decode(data []byte, target any, fields ...string) error {
	if err := contract.Decode(data, maximumFile, target); err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || len(object) != len(fields) {
		return fmt.Errorf("browser record has missing fields")
	}
	for _, key := range fields {
		if _, present := object[key]; !present {
			return fmt.Errorf("browser record has missing fields")
		}
	}
	return nil
}

type tree struct {
	root, output string
	captured     map[string][]byte
	stamps       map[string]os.FileInfo
}

func (t tree) read(name string) ([]byte, error) {
	if !safe(name) {
		return nil, fmt.Errorf("unsafe browser input path")
	}
	if t.captured != nil {
		data, found := t.captured[name]
		if !found || len(data) > maximumFile {
			return nil, fmt.Errorf("browser input absent or oversized")
		}
		return data, nil
	}
	base, relative := t.root, name
	if strings.HasPrefix(name, "build/") {
		base, relative = t.output, strings.TrimPrefix(name, "build/")
	}
	name = filepath.Join(base, filepath.FromSlash(relative))
	for current := name; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("browser input path must be real: %s", relative)
		}
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximumFile {
		return nil, fmt.Errorf("browser input must be a bounded regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumFile+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || len(data) > maximumFile || int64(len(data)) != info.Size() || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("browser input changed or exceeded bounds")
	}
	if previous, seen := t.stamps[name]; seen && (!os.SameFile(previous, after) || previous.Size() != after.Size() || !previous.ModTime().Equal(after.ModTime())) {
		return nil, fmt.Errorf("browser input changed during verification")
	}
	if t.stamps != nil {
		t.stamps[name] = after
	}
	return data, nil
}

func (t tree) names(prefix string) ([]string, error) {
	var names []string
	if t.captured != nil {
		for name := range t.captured {
			if strings.HasPrefix(name, prefix+"/") {
				names = append(names, name)
			}
		}
	} else {
		base := t.root
		if prefix == "build" {
			base = t.output
		} else {
			base = filepath.Join(base, filepath.FromSlash(prefix))
		}
		if err := filepath.WalkDir(base, func(name string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("linked browser output")
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() || len(names) >= maximumModules {
				return fmt.Errorf("browser output tree exceeds bounds")
			}
			relative, err := filepath.Rel(base, name)
			if err != nil {
				return err
			}
			names = append(names, prefix+"/"+filepath.ToSlash(relative))
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if len(names) > maximumModules {
		return nil, fmt.Errorf("browser tree exceeds bounds")
	}
	sort.Strings(names)
	return names, nil
}

// VerifyLocal serves ordinary web CI without loading all of node_modules.
func VerifyLocal(root, output, version, revision string) (Facts, error) {
	return verify(tree{root: root, output: output}, version, revision)
}

// VerifyCaptured checks the same observations against an already bounded,
// regular-only builder capture. No archive code or package scripts are executed.
func VerifyCaptured(files map[string][]byte, version, revision string) (Facts, error) {
	var total int64
	for name, data := range files {
		total += int64(len(data))
		if !safe(name) || len(data) > maximumFile || len(files) > 50000 || total > maximumTotal {
			return Facts{}, fmt.Errorf("browser capture exceeds bounds")
		}
	}
	return verify(tree{captured: files}, version, revision)
}

func verify(t tree, version, revision string) (Facts, error) {
	if t.captured == nil {
		t.stamps = map[string]os.FileInfo{}
	}
	facts := Facts{SchemaVersion: 2, Kind: "browser-source-facts", Version: version, Revision: revision, Inputs: map[string]File{}, Outputs: map[string]File{}, Metadata: map[string]File{}, Packages: map[string]Package{}, Unattributed: map[string]File{}}
	if version != "dev" && !versionPattern.MatchString(version) || revision != "main" && !commitPattern.MatchString(revision) {
		return facts, fmt.Errorf("invalid expected browser source")
	}
	raw, err := t.read("build/" + inventoryPath)
	if err != nil {
		return facts, err
	}
	var inventory Observation
	if err := decode(raw, &inventory, "schema_version", "kind", "source", "execution", "outputs", "build_metadata_outputs", "modules", "excluded_modules", "source_reproduction_verified", "browser_module_closure_verified", "publication_authorized"); err != nil {
		return facts, err
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	for _, key := range []string{"source_reproduction_verified", "browser_module_closure_verified", "publication_authorized"} {
		if !bytes.Equal(fields[key], []byte("false")) {
			return facts, fmt.Errorf("browser observation grants unsupported authority")
		}
	}
	for _, key := range []string{"outputs", "build_metadata_outputs"} {
		var rows []json.RawMessage
		if json.Unmarshal(fields[key], &rows) != nil {
			return facts, fmt.Errorf("invalid browser output rows")
		}
		for _, row := range rows {
			var output Output
			if err := decode(row, &output, "file", "type", "sha256", "size"); err != nil {
				return facts, err
			}
		}
	}
	for _, key := range []string{"outputs", "build_metadata_outputs", "modules", "excluded_modules"} {
		value := bytes.TrimSpace(fields[key])
		if len(value) == 0 || value[0] != '[' {
			return facts, fmt.Errorf("browser observation requires explicit array fields")
		}
	}
	if inventory.SchemaVersion != 1 || inventory.Kind != "browser-module-inventory" || inventory.Execution != "vite-rollup-client-build" || inventory.Source.Version != version || inventory.Source.Revision != revision || inventory.Source.Lock.File != "package-lock.json" {
		return facts, fmt.Errorf("browser observation identity differs")
	}
	lockRaw, err := t.read("package-lock.json")
	if err != nil {
		return facts, err
	}
	facts.Inventory, facts.Lock = digest(raw), digest(lockRaw)
	if inventory.Source.Lock.SHA256 != facts.Lock {
		return facts, fmt.Errorf("browser lock bytes differ")
	}
	var lock struct {
		Version  int `json:"lockfileVersion"`
		Packages map[string]struct {
			Version, Integrity, Resolved string
			Link                         bool
		} `json:"packages"`
	}
	// Locks contain npm fields outside this consumer; duplicate keys still refuse.
	var validated any
	if err := contract.Decode(lockRaw, maximumFile, &validated); err != nil {
		return facts, err
	}
	if json.Unmarshal(lockRaw, &lock) != nil || lock.Version != 3 || len(lock.Packages) == 0 || len(lock.Packages) > maximumModules {
		return facts, fmt.Errorf("invalid exact browser lock")
	}
	if len(inventory.Outputs) == 0 || len(inventory.Outputs) > maximumModules || len(inventory.Metadata) > 2 || len(inventory.Modules) == 0 || len(inventory.Modules)+len(inventory.Excluded) > maximumModules {
		return facts, fmt.Errorf("browser observation exceeds bounds")
	}
	chunks := map[string]bool{}
	for _, output := range inventory.Outputs {
		if !safe(output.File) || !slices.Contains([]string{"chunk", "asset"}, output.Type) || !digestPattern.MatchString(output.SHA256) || output.Size < 0 || output.Size > maximumFile || output.File == inventoryPath || strings.HasPrefix(output.File, ".vite/") {
			return facts, fmt.Errorf("invalid browser output fact")
		}
		if _, found := facts.Outputs[output.File]; found {
			return facts, fmt.Errorf("duplicate browser output")
		}
		data, err := t.read("build/" + output.File)
		if err != nil || fileFact(data) != (File{output.SHA256, output.Size}) {
			return facts, fmt.Errorf("browser output bytes differ")
		}
		facts.Outputs[output.File] = fileFact(data)
		if output.Type == "chunk" {
			chunks[output.File] = true
		}
	}
	if len(chunks) == 0 {
		return facts, fmt.Errorf("missing browser client chunks")
	}
	for _, output := range inventory.Metadata {
		if !slices.Contains([]string{".vite/manifest.json", ".vite/ssr-manifest.json"}, output.File) || facts.Metadata[output.File].SHA256 != "" || output.Type != "asset" {
			return facts, fmt.Errorf("invalid browser build metadata")
		}
		data, err := t.read(".svelte-kit/output/client/" + output.File)
		if err != nil || fileFact(data) != (File{output.SHA256, output.Size}) {
			return facts, fmt.Errorf("browser build metadata bytes differ")
		}
		facts.Metadata[output.File] = fileFact(data)
	}
	seen := map[string]bool{}
	var total int64
	for excluded, rows := range [][]json.RawMessage{inventory.Modules, inventory.Excluded} {
		for _, raw := range rows {
			var module Module
			fields := []string{"id", "kind", "module_path", "source_sha256", "package", "transform_input_sha256", "rollup_input_sha256", "rendered_in"}
			if excluded == 1 {
				fields = append(fields, "reason")
			}
			if err := decode(raw, &module, fields...); err != nil {
				return facts, err
			}
			var moduleFields map[string]json.RawMessage
			json.Unmarshal(raw, &moduleFields)
			contributions := bytes.TrimSpace(moduleFields["rendered_in"])
			if len(contributions) == 0 || contributions[0] != '[' {
				return facts, fmt.Errorf("browser module requires explicit contribution list")
			}
			if module.ID == "" || len(module.ID) > 16384 || seen[module.ID] || len(module.Rendered) > len(chunks) || (excluded == 0) != (len(module.Rendered) > 0) || excluded == 1 && !slices.Contains([]string{"zero-rendered-length", "not-in-client-chunks"}, module.Reason) {
				return facts, fmt.Errorf("invalid browser module correspondence")
			}
			seen[module.ID] = true
			for _, opaque := range []*string{module.Transform, module.Rollup} {
				if opaque != nil && !digestPattern.MatchString(*opaque) {
					return facts, fmt.Errorf("invalid transformed input identity")
				}
			}
			contributed := map[string]bool{}
			for _, contribution := range module.Rendered {
				if !chunks[contribution.File] || contributed[contribution.File] || contribution.Length <= 0 || contribution.Length > maximumFile {
					return facts, fmt.Errorf("invalid rendered contribution")
				}
				contributed[contribution.File] = true
			}
			if module.Kind == "virtual" {
				if !regexp.MustCompile(`^virtual:[a-f0-9]{64}$`).MatchString(module.ID) || module.Path != nil || module.Source != nil || module.Package != nil {
					return facts, fmt.Errorf("virtual module exposes physical identity")
				}
				facts.Virtual++
				continue
			}
			if module.Path == nil || module.Source == nil || !safe(*module.Path) {
				return facts, fmt.Errorf("invalid browser physical input")
			}
			name := *module.Path
			if module.ID != "file:"+name && !regexp.MustCompile(`^`+regexp.QuoteMeta("file:"+name)+`:variant:[a-f0-9]{64}$`).MatchString(module.ID) {
				return facts, fmt.Errorf("browser physical identifier differs")
			}
			if _, found := facts.Inputs[name]; !found {
				data, err := t.read(name)
				if err != nil {
					return facts, err
				}
				facts.Inputs[name] = fileFact(data)
				total += int64(len(data))
				if total > maximumTotal {
					return facts, fmt.Errorf("browser source inputs exceed bounds")
				}
			}
			if facts.Inputs[name].SHA256 != *module.Source {
				return facts, fmt.Errorf("observed browser source changed")
			}
			facts.Physical++
			packageName, location, hasPackage := packageLocation(name)
			if !hasPackage && slices.Contains(strings.Split(name, "/"), "node_modules") {
				return facts, fmt.Errorf("invalid observed package path")
			}
			kind := "application-source"
			if hasPackage {
				kind = "package-source"
			} else if strings.HasPrefix(name, ".svelte-kit/") {
				kind = "generated-application"
			}
			if module.Kind != kind || !hasPackage && module.Package != nil {
				return facts, fmt.Errorf("browser module kind differs from path")
			}
			if !hasPackage {
				continue
			}
			pkg := module.Package
			locked, found := lock.Packages[location]
			if pkg == nil || !found || !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`).MatchString(pkg.Version) || pkg.Name != packageName || pkg.LockPath != location || pkg.Version != locked.Version || pkg.Integrity != locked.Integrity || locked.Link || locked.Resolved != "https://registry.npmjs.org/"+packageName+"/-/"+path.Base(packageName)+"-"+pkg.Version+".tgz" {
				return facts, fmt.Errorf("observed package differs from exact lock")
			}
			integrity, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(pkg.Integrity, "sha512-"))
			if err != nil || len(integrity) != 64 || "sha512-"+base64.StdEncoding.EncodeToString(integrity) != pkg.Integrity {
				return facts, fmt.Errorf("invalid observed package integrity")
			}
			if previous, found := facts.Packages[location]; found {
				if previous != *pkg {
					return facts, fmt.Errorf("inconsistent package observation")
				}
			} else {
				data, err := t.read(location + "/package.json")
				var installed struct{ Name, Version string }
				var validated any
				if err != nil || contract.Decode(data, maximumFile, &validated) != nil || json.Unmarshal(data, &installed) != nil || installed.Name != packageName || installed.Version != pkg.Version || digest(data) != pkg.PackageJSON {
					return facts, fmt.Errorf("installed package metadata differs")
				}
				facts.Packages[location] = *pkg
				if _, present := facts.Inputs[location+"/package.json"]; !present {
					total += int64(len(data))
					if total > maximumTotal {
						return facts, fmt.Errorf("browser source inputs exceed bounds")
					}
				}
				facts.Inputs[location+"/package.json"] = fileFact(data)
			}
		}
	}
	facts.Rendered, facts.Excluded = len(inventory.Modules), len(inventory.Excluded)
	names, err := t.names("build")
	if err != nil {
		return facts, err
	}
	total = 0
	for _, name := range names {
		data, err := t.read(name)
		if err != nil {
			return facts, err
		}
		total += int64(len(data))
		if total > maximumTotal {
			return facts, fmt.Errorf("browser output bytes exceed bounds")
		}
		name = strings.TrimPrefix(name, "build/")
		if strings.HasSuffix(name, ".js") && !chunks[name] {
			facts.Unattributed[name] = fileFact(data)
		}
	}
	for name, previous := range t.stamps {
		current, err := os.Lstat(name)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(previous, current) || previous.Size() != current.Size() || !previous.ModTime().Equal(current.ModTime()) {
			return facts, fmt.Errorf("browser input changed during verification")
		}
	}
	return facts, nil
}

func packageLocation(name string) (string, string, bool) {
	parts := strings.Split(name, "/")
	index := -1
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == "node_modules" {
			index = i
			break
		}
	}
	if index < 0 {
		return "", "", false
	}
	end := index + 2
	if end > len(parts) || strings.HasPrefix(parts[index+1], ".") {
		return "", "", false
	}
	if strings.HasPrefix(parts[index+1], "@") {
		end++
	}
	if end >= len(parts) {
		return "", "", false
	}
	packageName := strings.Join(parts[index+1:end], "/")
	if !regexp.MustCompile(`^(?:@[a-zA-Z0-9_.-]+/)?[a-zA-Z0-9_.-]+$`).MatchString(packageName) {
		return "", "", false
	}
	return packageName, strings.Join(parts[:end], "/"), true
}
