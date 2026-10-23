package notices

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

// BackendInventory retains the distributed v1 notice contract. Original source
// hashes and normalized container text hashes deliberately remain distinct.
type BackendInventory struct {
	Inputs  map[string]string     `json:"inputs"`
	Modules []BackendModuleNotice `json:"modules"`
}

type BackendModuleNotice struct {
	Module  string          `json:"module"`
	Version string          `json:"version"`
	Notices []BackendNotice `json:"notices"`
}

type BackendNotice struct {
	Path           string `json:"path"`
	SHA256         string `json:"sha256"`
	UpstreamSHA256 string `json:"upstream_sha256"`
}

func normalizedLegal(data []byte) ([]byte, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("legal text is not UTF-8")
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"), nil
}

// Backend resolves the selected module graph without network access, collecting
// exact legal inputs once for backend checks, container builds and source offers.
// It preserves the old inventory and text bytes while avoiding go.mod regexes.
func Backend(ctx context.Context, root, output string, check bool) (BackendInventory, error) {
	var result BackendInventory
	backend := filepath.Join(root, "backend")
	runner := process.Runner{Dir: backend, Env: map[string]string{
		"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly",
	}}
	modJSON, err := runner.Output(ctx, 15*time.Second, "go", "mod", "edit", "-json")
	if err != nil {
		return result, err
	}
	var definition struct {
		Module  struct{ Path string }
		Require []struct{ Path, Version string }
		Replace []json.RawMessage
	}
	if err := json.Unmarshal(modJSON, &definition); err != nil || definition.Module.Path != BackendModule || len(definition.Replace) != 0 {
		return result, fmt.Errorf("invalid pinned backend module definition")
	}
	declared := map[string]string{}
	arguments := []string{"go", "list", "-m", "-json"}
	for _, requirement := range definition.Require {
		if declared[requirement.Path] != "" {
			return result, fmt.Errorf("duplicate backend module requirement")
		}
		declared[requirement.Path] = requirement.Version
		arguments = append(arguments, requirement.Path)
	}
	raw, err := runner.Output(ctx, 60*time.Second, arguments...)
	if err != nil {
		return result, fmt.Errorf("resolve backend modules from local cache: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var modules []goModule
	for {
		var module goModule
		if err := decoder.Decode(&module); err == io.EOF {
			break
		} else if err != nil {
			return result, fmt.Errorf("invalid resolved module metadata: %w", err)
		}
		if module.Main {
			if module.Path != BackendModule || module.Replace != nil {
				return result, fmt.Errorf("unexpected backend main module")
			}
			continue
		}
		version, retained := declared[module.Path]
		if !retained {
			continue
		}
		if version != module.Version {
			return result, fmt.Errorf("backend declared and resolved module versions differ")
		}
		if module.Path == "" || module.Version == "" || module.Dir == "" || module.Replace != nil || strings.ContainsAny(module.Path+module.Version, "\\\x00") || strings.HasPrefix(module.Path, "/") || strings.Contains(module.Path, "..") {
			return result, fmt.Errorf("backend notices require pinned cached modules")
		}
		modules = append(modules, module)
	}
	if len(modules) == 0 || len(modules) != len(declared) {
		return result, fmt.Errorf("backend module graph is empty or incomplete")
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	result.Inputs = map[string]string{}
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := regular(filepath.Join(backend, name))
		if err != nil {
			return result, err
		}
		result.Inputs[name] = Hash(data)
	}
	files := map[string][]byte{}
	text := []string{
		"psst.zip backend dependency notices",
		"Generated from the exact versions declared in backend/go.mod.",
		"License formatting uses LF, no trailing spaces/tabs, and one final newline.",
		"The inventory retains both original upstream and distributed text hashes.",
		"The Go standard library license is copied from the build toolchain into",
		"/app/licenses/go/LICENSE in the container. Base-image packages retain their",
		"upstream licenses; this inventory covers the application Go modules.", "",
	}
	for _, module := range modules {
		entries, err := os.ReadDir(module.Dir)
		if err != nil {
			return result, err
		}
		entry := BackendModuleNotice{Module: module.Path, Version: module.Version, Notices: []BackendNotice{}}
		hasLicense := false
		for _, item := range entries {
			name := strings.ToLower(item.Name())
			isLegal := false
			for _, prefix := range []string{"license", "licence", "copying", "copyright", "notice", "sqlite-license"} {
				isLegal = isLegal || strings.HasPrefix(name, prefix)
			}
			if !isLegal || item.IsDir() {
				continue
			}
			upstream, err := regular(filepath.Join(module.Dir, item.Name()))
			if err != nil {
				return result, err
			}
			data, err := normalizedLegal(upstream)
			if err != nil {
				return result, err
			}
			rel := "dependencies/" + module.Path + "@" + module.Version + "/" + item.Name()
			files[rel] = data
			entry.Notices = append(entry.Notices, BackendNotice{rel, Hash(data), Hash(upstream)})
			hasLicense = hasLicense || strings.HasPrefix(name, "license") || strings.HasPrefix(name, "licence") || strings.HasPrefix(name, "copying")
			text = append(text, "=== "+module.Path+"@"+module.Version+": "+item.Name()+" ===", string(data), "")
		}
		if !hasLicense {
			return result, fmt.Errorf("no upstream license text for %s@%s", module.Path, module.Version)
		}
		result.Modules = append(result.Modules, entry)
	}
	files["THIRD_PARTY_NOTICES.txt"], err = normalizedLegal([]byte(strings.Join(text, "\n")))
	if err != nil {
		return result, err
	}
	files["dependency-inventory.json"], err = contract.JSON(result)
	if err != nil {
		return result, err
	}
	if output == "" {
		output = filepath.Join(backend, "licenses")
	}
	if err := writeBackendFiles(output, files, check); err != nil {
		return result, err
	}
	return result, nil
}

func writeBackendFiles(output string, files map[string][]byte, check bool) error {
	for relative, content := range files {
		name := filepath.Join(output, filepath.FromSlash(relative))
		if check {
			actual, err := regular(name)
			if err != nil || !bytes.Equal(content, actual) {
				return fmt.Errorf("stale backend dependency notice: %s", relative)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(name, content, 0o644); err != nil {
				return err
			}
		}
	}
	return filepath.WalkDir(filepath.Join(output, "dependencies"), func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(output, path)
		if err != nil {
			return err
		}
		if _, exists := files[filepath.ToSlash(rel)]; exists {
			return nil
		}
		if check {
			return fmt.Errorf("obsolete backend notice: %s", rel)
		}
		return os.Remove(path)
	})
}
