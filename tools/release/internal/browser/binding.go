package browser

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

var fixedInputs = []string{"scripts/browser-source-recipes.json", "src/app.html", "package.json", "package-lock.json", "Dockerfile", "vite.config.ts", "svelte.config.js", "tsconfig.json", "scripts/browser-module-inventory.mjs", "scripts/pack-browser-builder-inputs.mjs", preferredCatalogPath}

type BindingOptions struct {
	Root        string
	Source      source.Context
	Captured    map[string][]byte
	Common      source.Inventory // ReadCommon must authenticate/replay this payload first.
	CommonFiles map[string][]byte
	GitFiles    map[string][]byte // Optional shared exact Git snapshot from GitInputs.
}

type BoundFacts struct {
	Facts
	GitInputs       map[string]string `json:"git_inputs"`
	NPMSources      map[string]string `json:"npm_sources"`
	GeneratorInputs map[string]File   `json:"generator_inputs"`
	Generated       map[string]string `json:"generated_input_associations"`
	CopiedStatic    map[string]File   `json:"copied_static"`
	Catalog         string            `json:"reviewed_catalog_sha256"`
}

// GitInputs shares one bounded exact source snapshot across both architectures.
func GitInputs(ctx context.Context, root, commit string) (map[string][]byte, error) {
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("invalid browser Git source")
	}
	runner := process.Runner{Dir: root, CleanEnv: true, Env: map[string]string{"PATH": os.Getenv("PATH")}}
	raw, err := runner.Output(ctx, 30*time.Second, "git", "archive", "--format=tar", commit, "web", "backend/licenses")
	if err != nil {
		return nil, err
	}
	reader := tar.NewReader(bytes.NewReader(raw))
	files := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if !safe(header.Name) || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maximumFile || len(files) > 50000 || !(strings.HasPrefix(header.Name, "web/") || strings.HasPrefix(header.Name, "backend/licenses/")) {
			return nil, fmt.Errorf("unsafe Git browser snapshot")
		}
		if _, found := files[header.Name]; found {
			return nil, fmt.Errorf("duplicate Git browser file")
		}
		data, err := io.ReadAll(io.LimitReader(reader, maximumFile+1))
		if err != nil || int64(len(data)) != header.Size {
			return nil, fmt.Errorf("truncated Git browser source")
		}
		files[header.Name] = data
	}
	return files, nil
}

func blob(data []byte) string {
	sum := sha1.New()
	fmt.Fprintf(sum, "blob %d\x00", len(data))
	sum.Write(data)
	return hex.EncodeToString(sum.Sum(nil))
}

// BindCaptured ties observed builder bytes to the exact Git tree and the common
// retained npm originals. Reviewed generator data replaces snippet inference;
// no generated-byte reproduction or complete browser closure is claimed.
func BindCaptured(ctx context.Context, options BindingOptions) (BoundFacts, error) {
	facts, err := VerifyCaptured(options.Captured, options.Source.Version, options.Source.Commit)
	result := BoundFacts{Facts: facts, GitInputs: map[string]string{}, NPMSources: map[string]string{}, GeneratorInputs: map[string]File{}, Generated: map[string]string{}, CopiedStatic: map[string]File{}}
	if err != nil {
		return result, err
	}
	if err := options.Source.Validate(); err != nil {
		return result, err
	}
	if options.Common.Source != options.Source || options.Common.SchemaVersion != 2 {
		return result, fmt.Errorf("common browser source inventory differs")
	}
	git := options.GitFiles
	if git == nil {
		git, err = GitInputs(ctx, options.Root, options.Source.Commit)
		if err != nil {
			return result, err
		}
	}
	selected := map[string]bool{}
	for _, name := range fixedInputs {
		selected[name] = true
	}
	for name := range facts.Inputs {
		selected[name] = true
	}
	for name := range facts.Metadata {
		selected[".svelte-kit/output/client/"+name] = true
	}
	for name, data := range options.Captured {
		if strings.HasPrefix(name, "build/") {
			selected[name] = true
		} else if strings.HasPrefix(name, "static/") {
			selected[name] = true
			if name != "static/licenses/release.json" {
				if original, found := git["web/"+name]; !found || !bytes.Equal(data, original) {
					return result, fmt.Errorf("captured static bytes differ from Git")
				}
				if _, generated := facts.Outputs[strings.TrimPrefix(name, "static/")]; generated {
					return result, fmt.Errorf("static original overlaps generated output")
				}
				if !bytes.Equal(data, options.Captured["build/"+strings.TrimPrefix(name, "static/")]) {
					return result, fmt.Errorf("copied static output differs")
				}
				result.CopiedStatic[strings.TrimPrefix(name, "static/")] = fileFact(data)
			}
		}
	}
	for name := range git {
		if strings.HasPrefix(name, "web/static/") {
			relative := strings.TrimPrefix(name, "web/")
			if _, found := options.Captured[relative]; !found {
				return result, fmt.Errorf("committed static original missing from capture")
			}
		}
	}
	catalogRaw, found := options.Captured[fixedInputs[0]]
	if !found || !bytes.Equal(catalogRaw, git["web/"+fixedInputs[0]]) {
		return result, fmt.Errorf("browser source catalog differs from committed bytes")
	}
	var catalog map[string]json.RawMessage
	if err := contract.Decode(catalogRaw, maximumFile, &catalog); err != nil {
		return result, err
	}
	for _, key := range []string{"source_reproduction_verified", "browser_module_closure_verified", "distribution_authorized"} {
		if !bytes.Equal(bytes.TrimSpace(catalog[key]), []byte("false")) {
			return result, fmt.Errorf("browser catalog grants unsupported authority")
		}
	}
	if string(catalog["schema_version"]) != "1" || string(catalog["kind"]) != `"reviewed-browser-generator-associations"` {
		return result, fmt.Errorf("invalid reviewed browser source catalog")
	}
	for _, key := range []string{"vite", "kit", "adapter_static", "svelte_compiler", "svelte_plugin"} {
		var recipe struct {
			LockPath      string `json:"lock_path"`
			Name, Version string
			Members       []string
			Generated     map[string]json.RawMessage
			Nodes         struct{ Prefix string } `json:"generated_nodes"`
		}
		if json.Unmarshal(catalog[key], &recipe) != nil || recipe.Name == "" || recipe.Version == "" || recipe.LockPath != "node_modules/"+recipe.Name || !safe(recipe.LockPath) || len(recipe.Members) == 0 || len(recipe.Members) > 512 {
			return result, fmt.Errorf("invalid reviewed generator input catalog")
		}
		var installed struct{ Name, Version string }
		packageData, found := options.Captured[recipe.LockPath+"/package.json"]
		if !found || json.Unmarshal(packageData, &installed) != nil || installed.Name != recipe.Name || installed.Version != recipe.Version {
			return result, fmt.Errorf("generator input differs from reviewed catalog")
		}
		seen := map[string]bool{}
		for _, member := range append(append([]string(nil), recipe.Members...), "package.json") {
			if !safe(member) || seen[member] && member != "package.json" {
				return result, fmt.Errorf("invalid generator member")
			}
			seen[member] = true
			name := recipe.LockPath + "/" + member
			data, found := options.Captured[name]
			if !found {
				return result, fmt.Errorf("reviewed generator member missing")
			}
			selected[name] = true
			result.GeneratorInputs[name] = fileFact(data)
		}
		if key == "kit" {
			for name := range facts.Inputs {
				if strings.HasPrefix(name, ".svelte-kit/generated/") {
					_, fixed := recipe.Generated[name]
					index := strings.TrimSuffix(strings.TrimPrefix(name, recipe.Nodes.Prefix), ".js")
					indexed := recipe.Nodes.Prefix != "" && strings.HasPrefix(name, recipe.Nodes.Prefix) && strings.HasSuffix(name, ".js") && regexp.MustCompile(`^(0|[1-9][0-9]{0,4})$`).MatchString(index)
					number, parseErr := strconv.Atoi(index)
					indexed = indexed && parseErr == nil && number < maximumModules
					if !fixed && !indexed {
						return result, fmt.Errorf("generated browser input has no reviewed catalog association")
					}
					result.Generated[name] = "reviewed-retained-generator-input-association"
				}
			}
		}
	}
	result.Catalog = digest(catalogRaw)
	for name := range selected {
		if _, found := options.Captured[name]; !found {
			return result, fmt.Errorf("required browser input missing from capture")
		}
	}
	packages := map[string]source.Source{}
	for _, item := range options.Common.Sources {
		if item.Ecosystem == "npm" {
			if _, found := packages[item.LockPath]; found {
				return result, fmt.Errorf("duplicate retained npm path")
			}
			packages[item.LockPath] = item
		}
	}
	members := map[string]map[string]source.Member{}
	for name, data := range options.Captured {
		if !selected[name] {
			return result, fmt.Errorf("unexpected browser capture payload")
		}
		if strings.HasPrefix(name, "node_modules/") {
			packageName, location, valid := packageLocation(name)
			item, found := packages[location]
			if !valid || !found || item.Name != packageName {
				return result, fmt.Errorf("observed package absent from retained originals")
			}
			if members[location] == nil {
				original, err := source.SourceMembers(item, options.CommonFiles)
				if err != nil {
					return result, err
				}
				members[location] = map[string]source.Member{}
				prefix := item.Authentication["archive_root"]
				if prefix == "" {
					return result, fmt.Errorf("retained npm original root missing")
				}
				for _, member := range original {
					if member.Kind == "file" {
						members[location][strings.TrimPrefix(member.Path, prefix+"/")] = member
					}
				}
			}
			member, found := members[location][strings.TrimPrefix(name, location+"/")]
			if !found || member.SHA256 != digest(data) || member.Size != int64(len(data)) {
				return result, fmt.Errorf("observed npm bytes differ from retained original")
			}
			result.NPMSources[location] = item.ID
		} else if strings.HasPrefix(name, "build/") || strings.HasPrefix(name, ".svelte-kit/") || name == "static/licenses/release.json" {
			continue
		} else {
			original, found := git["web/"+name]
			if !found || !bytes.Equal(data, original) {
				return result, fmt.Errorf("browser application/config input differs from exact Git source")
			}
			result.GitInputs[name] = blob(data)
		}
	}
	var metadata map[string]any
	expected := map[string]any{"name": "psst.zip", "version": options.Source.Version, "revision": options.Source.Commit, "license": "AGPL-3.0-only", "source": "https://github.com/" + options.Source.Repository, "source_archive": "https://github.com/" + options.Source.Repository + "/archive/" + options.Source.Commit + ".tar.gz", "notice_files": []any{"/licenses/backend/THIRD_PARTY_NOTICES.txt"}}
	data, found := options.Captured["static/licenses/release.json"]
	if !found || contract.Decode(data, maximumFile, &metadata) != nil || !reflect.DeepEqual(metadata, expected) || !bytes.Equal(data, options.Captured["build/licenses/release.json"]) {
		return result, fmt.Errorf("generated browser release metadata differs")
	}
	return result, nil
}
