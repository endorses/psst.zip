// Package notices inventories exact legal bytes from resolved Go package graphs.
package notices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

const BackendModule = "github.com/endorses/psst.zip/backend"

var Targets = []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"}

type Notice struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type Module struct {
	Module   string   `json:"module"`
	Version  string   `json:"version,omitempty"`
	Packages []string `json:"packages,omitempty"`
	Targets  []string `json:"targets,omitempty"`
	Notices  []Notice `json:"notices"`
}
type Command struct {
	Target  string   `json:"target"`
	Command []string `json:"command"`
}
type Inventory struct {
	SchemaVersion int               `json:"schema_version"`
	Inputs        map[string]string `json:"inputs"`
	Commands      []Command         `json:"commands"`
	Browser       Module            `json:"embedded_browser"`
	Modules       []Module          `json:"modules"`
}
type Options struct {
	Root, Output          string
	Targets               []string
	Workspace, IncludeWeb bool
}

type goModule struct {
	Path, Version, Dir, Sum, GoModSum string
	Main                              bool
	Replace                           *goModule
}
type goPackage struct {
	ImportPath, Dir string
	Standard        bool
	Module          *goModule
	Error           any
	DepsErrors      []any
}

func Hash(data []byte) string { value := sha256.Sum256(data); return hex.EncodeToString(value[:]) }

func regular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("notice input must be a regular file: %s", path)
	}
	return os.ReadFile(path)
}

func legalFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		for _, prefix := range []string{"license", "licence", "copying", "copyright", "notice", "patents"} {
			if strings.HasPrefix(name, prefix) {
				if entry.Type()&os.ModeSymlink != 0 {
					return nil, fmt.Errorf("symlink legal text is unsupported")
				}
				if !entry.IsDir() {
					files = append(files, filepath.Join(dir, entry.Name()))
				}
				break
			}
		}
	}
	return files, nil
}

func inside(root, name string) bool {
	rel, err := filepath.Rel(root, name)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Collect queries each requested target once and retains actual linked module terms.
func Collect(ctx context.Context, opts Options) (Inventory, error) {
	var result Inventory
	client := filepath.Join(opts.Root, "client")
	runner := process.Runner{Dir: client}
	if len(opts.Targets) == 0 {
		opts.Targets = append([]string(nil), Targets...)
	}
	if err := os.MkdirAll(opts.Output, 0o755); err != nil {
		return result, err
	}
	existing, err := os.ReadDir(opts.Output)
	if err != nil {
		return result, err
	}
	if len(existing) != 0 {
		return result, fmt.Errorf("notice output must be empty")
	}
	raw, err := runner.Output(ctx, 90*time.Second, "go", "env", "-json", "GOROOT", "GOVERSION")
	if err != nil {
		return result, err
	}
	var environment struct{ GOROOT, GOVERSION string }
	if err = json.Unmarshal(raw, &environment); err != nil {
		return result, err
	}
	files := map[string][]byte{}
	entries := map[string]*Module{}
	retain := func(path, name string, entry *Module) error {
		data, err := regular(path)
		if err != nil {
			return err
		}
		if previous, ok := files[name]; ok && !bytes.Equal(previous, data) {
			return fmt.Errorf("conflicting legal text")
		}
		files[name] = data
		notice := Notice{name, Hash(data)}
		if !slices.Contains(entry.Notices, notice) {
			entry.Notices = append(entry.Notices, notice)
		}
		return nil
	}
	goEntry := &Module{Module: "Go toolchain and standard library", Version: environment.GOVERSION, Packages: []string{}, Targets: append([]string(nil), opts.Targets...), Notices: []Notice{}}
	entries["go"] = goEntry
	legal, err := legalFiles(environment.GOROOT)
	if err != nil {
		return result, err
	}
	for _, path := range legal {
		if err := retain(path, "go/"+filepath.Base(path), goEntry); err != nil {
			return result, err
		}
	}
	if _, err := regular(filepath.Join(environment.GOROOT, "LICENSE")); err != nil {
		return result, fmt.Errorf("Go toolchain license is missing: %w", err)
	}
	for _, target := range opts.Targets {
		if !slices.Contains(Targets, target) {
			return result, fmt.Errorf("unsupported CLI target: %s", target)
		}
		system, arch, _ := strings.Cut(target, "/")
		workspace := "off"
		if opts.Workspace {
			workspace = filepath.Join(client, "go.work")
		}
		args := []string{"go", "list", "-deps", "-json", "./cmd/psst"}
		result.Commands = append(result.Commands, Command{target, args})
		raw, err = runner.With(map[string]string{"CGO_ENABLED": "0", "GOOS": system, "GOARCH": arch, "GOFLAGS": "-mod=readonly", "GOWORK": workspace}).Output(ctx, 90*time.Second, args...)
		if err != nil {
			return result, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for {
			var pkg goPackage
			err := decoder.Decode(&pkg)
			if err == io.EOF {
				break
			}
			if err != nil {
				return result, err
			}
			if pkg.Error != nil || len(pkg.DepsErrors) != 0 {
				return result, fmt.Errorf("Go package graph is incomplete")
			}
			module := pkg.Module
			if module != nil && (!module.Main || module.Path == BackendModule) {
				local := module.Main && opts.Workspace
				if module.Replace != nil || (!local && module.Version == "") {
					return result, fmt.Errorf("release dependencies require pinned module sources")
				}
				version := module.Version
				if local {
					version = "workspace-source"
				}
				id := module.Path + "@" + version
				entry := entries[id]
				if entry == nil {
					entry = &Module{Module: module.Path, Version: version, Packages: []string{}, Targets: []string{}, Notices: []Notice{}}
					entries[id] = entry
				}
				if !slices.Contains(entry.Targets, target) {
					entry.Targets = append(entry.Targets, target)
				}
				if !slices.Contains(entry.Packages, pkg.ImportPath) {
					entry.Packages = append(entry.Packages, pkg.ImportPath)
				}
				if module.Dir == "" || !inside(module.Dir, pkg.Dir) {
					return result, fmt.Errorf("package escaped pinned module")
				}
				for dir := pkg.Dir; ; dir = filepath.Dir(dir) {
					legal, err := legalFiles(dir)
					if err != nil {
						return result, err
					}
					for _, path := range legal {
						rel, _ := filepath.Rel(module.Dir, path)
						if err := retain(path, "modules/"+id+"/"+filepath.ToSlash(rel), entry); err != nil {
							return result, err
						}
					}
					if dir == module.Dir {
						break
					}
				}
				hasLicense := false
				for _, item := range entry.Notices {
					n := strings.ToLower(filepath.Base(item.Path))
					if strings.HasPrefix(n, "license") || strings.HasPrefix(n, "licence") || strings.HasPrefix(n, "copying") {
						hasLicense = true
					}
				}
				if !hasLicense {
					return result, fmt.Errorf("no upstream license for %s", id)
				}
			} else if pkg.Standard && strings.HasPrefix(pkg.ImportPath, "vendor/") {
				if !inside(environment.GOROOT, pkg.Dir) {
					return result, fmt.Errorf("vendored standard package escaped toolchain")
				}
				for dir := pkg.Dir; dir != environment.GOROOT; dir = filepath.Dir(dir) {
					legal, err := legalFiles(dir)
					if err != nil {
						return result, err
					}
					for _, path := range legal {
						rel, _ := filepath.Rel(environment.GOROOT, path)
						if err := retain(path, "go/"+filepath.ToSlash(rel), goEntry); err != nil {
							return result, err
						}
					}
				}
			}
		}
	}
	result.SchemaVersion = 1
	result.Inputs = map[string]string{}
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := regular(filepath.Join(client, name))
		if err != nil {
			return result, err
		}
		result.Inputs[name] = Hash(data)
	}
	result.Browser = Module{Module: "Embedded LAN browser", Notices: []Notice{}}
	if opts.IncludeWeb {
		site := filepath.Join(client, "internal/lanassets/site")
		for _, name := range []string{"index.html", "licenses/SOURCE.json"} {
			if _, err := regular(filepath.Join(site, name)); err != nil {
				return result, fmt.Errorf("embedded browser assets are missing: %w", err)
			}
		}
		err = filepath.WalkDir(filepath.Join(site, "licenses"), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path == filepath.Join(site, "licenses/backend") {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(filepath.Join(site, "licenses"), path)
			return retain(path, "web/"+filepath.ToSlash(rel), &result.Browser)
		})
		if err != nil {
			return result, err
		}
		for _, name := range []string{"AGPL-3.0-only.txt", "THIRD_PARTY_NOTICES.txt", "dependency-inventory.json"} {
			if _, ok := files["web/"+name]; !ok {
				return result, fmt.Errorf("embedded browser legal resources are incomplete")
			}
		}
	}
	var keys []string
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	text := []string{"psst.zip CLI third-party notices", "Exact legal text from pinned modules used by the listed target package graphs.", "Go standard-library and imported vendored-package terms are retained too.", "The project itself is licensed under AGPL-3.0-only; see LICENSE.", ""}
	for _, key := range keys {
		entry := entries[key]
		sort.Strings(entry.Packages)
		sort.Strings(entry.Targets)
		sort.Slice(entry.Notices, func(i, j int) bool { return entry.Notices[i].Path < entry.Notices[j].Path })
		result.Modules = append(result.Modules, *entry)
		for _, item := range entry.Notices {
			text = append(text, "=== "+item.Path+" ===", string(files[item.Path]), "")
		}
	}
	for _, item := range result.Browser.Notices {
		ext := strings.ToLower(filepath.Ext(item.Path))
		if ext == ".txt" || ext == ".md" {
			text = append(text, "=== "+item.Path+" ===", string(files[item.Path]), "")
		}
	}
	data, err := contract.JSON(result)
	if err != nil {
		return result, err
	}
	files["dependency-inventory.json"] = data
	files["THIRD_PARTY_NOTICES.txt"] = []byte(strings.TrimRight(strings.Join(text, "\n"), "\n \t") + "\n")
	for name, data := range files {
		path := filepath.Join(opts.Output, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return result, err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return result, err
		}
	}
	return result, nil
}

// ExportPrepared reuses the six-target inventory already authenticated by asset
// preparation. Embedded Go terms are not duplicated under the browser section.
func ExportPrepared(root, output string) (Inventory, error) {
	var result Inventory
	site := filepath.Join(root, "client/internal/lanassets/site/licenses")
	base := filepath.Join(site, "backend")
	raw, err := os.ReadFile(filepath.Join(base, "dependency-inventory.json"))
	if err != nil {
		return result, err
	}
	if err := contract.Decode(raw, 16<<20, &result); err != nil {
		return result, err
	}
	if result.SchemaVersion != 1 || len(result.Modules) == 0 || len(result.Commands) != len(Targets) {
		return result, fmt.Errorf("prepared notice inventory is incomplete")
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return result, err
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return result, err
	}
	if len(entries) != 0 {
		return result, fmt.Errorf("notice output must be empty")
	}
	for _, entry := range result.Modules {
		for _, notice := range entry.Notices {
			if !safeName(notice.Path) {
				return result, fmt.Errorf("unsafe prepared notice path")
			}
			data, err := regular(filepath.Join(base, filepath.FromSlash(notice.Path)))
			if err != nil {
				return result, err
			}
			if Hash(data) != notice.SHA256 {
				return result, fmt.Errorf("prepared notice checksum mismatch")
			}
			if err := write(output, notice.Path, data); err != nil {
				return result, err
			}
		}
	}
	result.Browser = Module{Module: "Embedded LAN browser", Notices: []Notice{}}
	err = filepath.WalkDir(site, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == base {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(site, path)
		name := "web/" + filepath.ToSlash(rel)
		data, err := regular(path)
		if err != nil {
			return err
		}
		result.Browser.Notices = append(result.Browser.Notices, Notice{name, Hash(data)})
		return write(output, name, data)
	})
	if err != nil {
		return result, err
	}
	text := []string{"psst.zip CLI third-party notices", "Exact legal text from pinned modules used by the listed target package graphs.", "Go standard-library and imported vendored-package terms are retained too.", "The project itself is licensed under AGPL-3.0-only; see LICENSE.", ""}
	for _, entry := range result.Modules {
		for _, notice := range entry.Notices {
			data, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(notice.Path)))
			if err != nil {
				return result, err
			}
			text = append(text, "=== "+notice.Path+" ===", string(data), "")
		}
	}
	for _, notice := range result.Browser.Notices {
		ext := strings.ToLower(filepath.Ext(notice.Path))
		if ext == ".txt" || ext == ".md" {
			data, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(notice.Path)))
			if err != nil {
				return result, err
			}
			text = append(text, "=== "+notice.Path+" ===", string(data), "")
		}
	}
	if err := write(output, "THIRD_PARTY_NOTICES.txt", []byte(strings.TrimRight(strings.Join(text, "\n"), "\n \t")+"\n")); err != nil {
		return result, err
	}
	data, err := contract.JSON(result)
	if err != nil {
		return result, err
	}
	return result, write(output, "dependency-inventory.json", data)
}

func safeName(name string) bool {
	return name != "" && !filepath.IsAbs(name) && !strings.ContainsAny(name, "\\:\x00") && !strings.HasPrefix(name, "../") && !strings.Contains(name, "/../") && filepath.ToSlash(filepath.Clean(name)) == name
}
func write(root, name string, data []byte) error {
	if !safeName(name) {
		return fmt.Errorf("invalid notice path")
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
