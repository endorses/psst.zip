package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const backendRecipeCatalog = "tools/backend-source-recipes.json"

type sqliteRecipe struct {
	Project, Module, Version, Original string
	CVersion                           string `json:"c_version"`
	CVersionNumber                     string `json:"c_version_number"`
	SourceID                           string `json:"source_id"`
	FossilCommit                       string `json:"fossil_commit"`
	Recipe                             string
	RecipeSHA256                       string   `json:"recipe_sha256"`
	RecipeInputs                       []string `json:"recipe_inputs"`
	Translator                         string
	TranslatorVersion                  string            `json:"translator_version"`
	TranslatorInputs                   []string          `json:"translator_inputs"`
	TranslatorTools                    map[string]string `json:"translator_tools"`
	Platforms                          []struct{ Platform, Original, Output string }
}
type libcRecipe struct {
	Project, Module, Version, Original string
	RecipeInputs                       []string `json:"recipe_inputs"`
	RecipePrefixes                     []string `json:"recipe_prefixes"`
	RequiredInputs                     []string `json:"required_inputs"`
	Outputs                            []string
	GeneratorTools                     []struct {
		Module, Version string
		Entrypoints     []string
	} `json:"generator_tools"`
}
type backendRecipe struct {
	SchemaVersion                       int `json:"schema_version"`
	Kind                                string
	GeneratedOutputReproductionVerified bool `json:"generated_output_reproduction_verified"`
	SQLite                              sqliteRecipe
	Libc                                libcRecipe
}

// BackendPreferredFacts records source relationships without claiming generator
// execution or reproduction. All originals and member hashes come from replayed
// common/module inventories; no script is reverse-engineered with snippets.
type BackendPreferredFacts struct {
	Catalog                             File                  `json:"reviewed_catalog"`
	Projects                            []ProjectRelationship `json:"projects"`
	SQLite                              SQLiteRelationship    `json:"sqlite"`
	Libc                                LibcRelationship      `json:"libc"`
	GeneratedOutputReproductionVerified bool                  `json:"generated_output_reproduction_verified"`
}
type ProjectRelationship struct {
	Module, Version string
	Original        File              `json:"original"`
	ModuleOriginal  File              `json:"module_original"`
	Origin          map[string]string `json:"origin"`
	ProxyFiles      []File            `json:"proxy_files"`
}
type GeneratorTool struct {
	Module, Version, Sum string
	Original             *File  `json:"original,omitempty"`
	Inputs               []File `json:"inputs,omitempty"`
}
type SQLiteRelationship struct {
	Original, Project, Translator                           File
	OriginalInputs, RecipeInputs, TranslatorInputs, Outputs []File
	TranslatorTools                                         []GeneratorTool
	Comparisons                                             map[string]SQLiteVendoring
}
type LibcRelationship struct {
	Original, Project                     File
	OriginalInputs, RecipeInputs, Outputs []File
	GeneratorTools                        []GeneratorTool
}

type preferredOriginal struct {
	source  Source
	members map[string]Member
	root    string
}

func preferredMembers(item Source, files map[string][]byte) (preferredOriginal, error) {
	result := preferredOriginal{source: item, members: map[string]Member{}}
	members, err := SourceMembers(item, files)
	if err != nil {
		return result, err
	}
	for _, m := range members {
		root := strings.SplitN(m.Path, "/", 2)[0]
		if item.Ecosystem == "go" {
			root = item.Name + "@" + item.Version
		}
		relative := strings.TrimPrefix(m.Path, root+"/")
		if result.root == "" {
			result.root = root
		}
		if root != result.root || (m.Path != root && !strings.HasPrefix(m.Path, root+"/")) {
			return result, fmt.Errorf("original source has multiple member roots")
		}
		if m.Kind == "file" {
			if relative == m.Path || relative == "" {
				return result, fmt.Errorf("unrooted original member")
			}
			m.Path = relative
			result.members[relative] = m
		}
	}
	if len(result.members) == 0 {
		return result, fmt.Errorf("preferred source has no regular originals")
	}
	return result, nil
}
func preferredFacts(m map[string]Member, names []string) ([]File, error) {
	seen := map[string]bool{}
	result := []File{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		member, ok := m[name]
		if !safePath(name) || !ok || member.Kind != "file" || !digestRE.MatchString(member.SHA256) {
			return nil, fmt.Errorf("reviewed preferred member is absent: %s", name)
		}
		result = append(result, File{name, member.SHA256, member.Size})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}
func preferredBytes(item preferredOriginal, files map[string][]byte, selected map[string]bool) (map[string][]byte, error) {
	raw := files[item.source.Original.Path]
	if fact(item.source.Original.Path, raw) != item.source.Original {
		return nil, fmt.Errorf("preferred original changed")
	}
	_, out, err := inspectArchive(raw, archivePolicy{Expanded: 2 << 30, Member: OriginalLimit, Members: 100000, Prefix: item.root, Commit: item.source.Version, Original: true, Selected: selected})
	if err != nil {
		return nil, err
	}
	for name := range selected {
		if _, ok := out[name]; !ok {
			return nil, fmt.Errorf("preferred source input absent")
		}
	}
	return out, nil
}
func namedSources(inventory Inventory, files map[string][]byte, ecosystem string) (map[string]preferredOriginal, error) {
	result := map[string]preferredOriginal{}
	for _, item := range inventory.Sources {
		if item.Ecosystem != ecosystem {
			continue
		}
		key := item.ID
		if ecosystem == "go" {
			key = item.Name
		}
		if _, ok := result[key]; ok {
			return nil, fmt.Errorf("duplicate preferred source identity")
		}
		value, err := preferredMembers(item, files)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}
func preferredProject(upstream, module preferredOriginal, files map[string][]byte) (ProjectRelationship, error) {
	item := module.source
	result := ProjectRelationship{Module: item.Name, Version: item.Version, Original: upstream.source.Original, ModuleOriginal: item.Original}
	if item.Ecosystem != "go" || upstream.source.Associations["go:"+item.Name] != item.Version {
		return result, fmt.Errorf("preferred project/module differs from reviewed identity")
	}
	info := strings.TrimSuffix(item.Original.Path, ".zip") + ".info"
	raw, ok := files[info]
	if !ok {
		return result, fmt.Errorf("original module origin missing")
	}
	var value struct{ Origin map[string]string }
	if err := json.Unmarshal(raw, &value); err != nil {
		return result, err
	}
	expected := map[string]string{"VCS": "git", "URL": "https://gitlab.com/" + upstream.source.Name, "Hash": upstream.source.Version, "Ref": "refs/tags/" + item.Version}
	for name, want := range expected {
		if value.Origin[name] != want {
			return result, fmt.Errorf("proxy module origin differs from pinned project")
		}
	}
	result.Origin = expected
	names := []string{}
	for name, m := range module.members {
		original, ok := upstream.members[name]
		if !ok || original.SHA256 != m.SHA256 || original.Size != m.Size {
			return result, fmt.Errorf("proxy member differs from full preferred project: %s", name)
		}
		names = append(names, name)
	}
	var err error
	result.ProxyFiles, err = preferredFacts(module.members, names)
	return result, err
}
func originalInputs(item preferredOriginal) ([]File, error) {
	names := []string{}
	for name := range item.source.ObservedInputs {
		names = append(names, name)
	}
	return preferredFacts(item.members, names)
}
func requireSource(upstreams map[string]preferredOriginal, id, module, version string) (preferredOriginal, error) {
	item, ok := upstreams[id]
	if !ok || item.source.Associations["go:"+module] != version {
		return item, fmt.Errorf("preferred backend original identity differs")
	}
	return item, nil
}

// BackendPreferred consumes independently replayed originals. Callers should reuse
// identical common/module identities across native architectures, not rerun this
// architecture-independent source relationship check.
func BackendPreferred(ctx context.Context, common Inventory, commonFiles map[string][]byte, modules Inventory, moduleFiles map[string][]byte) (BackendPreferredFacts, error) {
	var result BackendPreferredFacts
	if common.Source != modules.Source {
		return result, fmt.Errorf("preferred source collections belong to different releases")
	}
	catalog := commonFiles["inputs/"+backendRecipeCatalog]
	if common.Inputs[backendRecipeCatalog] != fact("inputs/"+backendRecipeCatalog, catalog) {
		return result, fmt.Errorf("reviewed backend catalog differs from common source inventory")
	}
	var recipe backendRecipe
	if err := contract.Decode(catalog, MetadataLimit, &recipe); err != nil {
		return result, err
	}
	if recipe.SchemaVersion != 1 || recipe.Kind != "reviewed-backend-generator-associations" || recipe.GeneratedOutputReproductionVerified {
		return result, fmt.Errorf("invalid backend source association catalog")
	}
	result.Catalog = fact(backendRecipeCatalog, catalog)
	upstreams, err := namedSources(common, commonFiles, "upstream")
	if err != nil {
		return result, err
	}
	proxy, err := namedSources(modules, moduleFiles, "go")
	if err != nil {
		return result, err
	}
	sqlite, err := requireSource(upstreams, recipe.SQLite.Project, recipe.SQLite.Module, recipe.SQLite.Version)
	if err != nil {
		return result, err
	}
	libc, err := requireSource(upstreams, recipe.Libc.Project, recipe.Libc.Module, recipe.Libc.Version)
	if err != nil {
		return result, err
	}
	for _, item := range []preferredOriginal{sqlite, libc} {
		module := ""
		if item.source.ID == recipe.SQLite.Project {
			module = recipe.SQLite.Module
		} else {
			module = recipe.Libc.Module
		}
		relationship, err := preferredProject(item, proxy[module], moduleFiles)
		if err != nil {
			return result, err
		}
		result.Projects = append(result.Projects, relationship)
	}
	result.SQLite, err = backendSQLite(ctx, recipe.SQLite, sqlite, upstreams, commonFiles)
	if err != nil {
		return result, err
	}
	result.Libc, err = backendLibc(ctx, recipe.Libc, libc, upstreams, proxy, commonFiles, moduleFiles)
	return result, err
}
func backendSQLite(ctx context.Context, r sqliteRecipe, project preferredOriginal, upstreams map[string]preferredOriginal, files map[string][]byte) (SQLiteRelationship, error) {
	var result SQLiteRelationship
	original, err := requireSource(upstreams, r.Original, r.Module, r.Version)
	if err != nil {
		return result, err
	}
	translator, err := requireSource(upstreams, r.Translator, r.Module, r.Version)
	if err != nil {
		return result, err
	}
	if translator.source.Name != "cznic/libsqlite3" || r.TranslatorVersion != "v1.9.0" || !digestRE.MatchString(r.RecipeSHA256) {
		return result, fmt.Errorf("SQLite translator catalog identity differs")
	}
	result.Original = original.source.Original
	result.Project = project.source.Original
	result.Translator = translator.source.Original
	result.OriginalInputs, err = originalInputs(original)
	if err != nil {
		return result, err
	}
	result.RecipeInputs, err = preferredFacts(project.members, r.RecipeInputs)
	if err != nil {
		return result, err
	}
	selectedProject := map[string]bool{r.Recipe: true}
	selectedTranslator := map[string]bool{"go.mod": true, "go.sum": true}
	translatorPaths := append([]string{}, r.TranslatorInputs...)
	for name := range translator.members {
		if !strings.Contains(name, "/") && strings.HasPrefix(name, "generator") && strings.HasSuffix(name, ".go") {
			translatorPaths = append(translatorPaths, name)
		}
	}
	result.TranslatorInputs, err = preferredFacts(translator.members, translatorPaths)
	if err != nil {
		return result, err
	}
	if len(r.Platforms) != 2 {
		return result, fmt.Errorf("SQLite source associations lack native platforms")
	}
	seen := map[string]bool{}
	outputNames := []string{}
	for _, mapping := range r.Platforms {
		if seen[mapping.Platform] || (mapping.Platform != "linux/amd64" && mapping.Platform != "linux/arm64") {
			return result, fmt.Errorf("invalid SQLite native source association")
		}
		seen[mapping.Platform] = true
		selectedProject[mapping.Output] = true
		selectedTranslator[mapping.Original] = true
		outputNames = append(outputNames, mapping.Output)
	}
	result.Outputs, err = preferredFacts(project.members, outputNames)
	if err != nil {
		return result, err
	}
	projectBytes, err := preferredBytes(project, files, selectedProject)
	if err != nil {
		return result, err
	}
	translatorBytes, err := preferredBytes(translator, files, selectedTranslator)
	if err != nil {
		return result, err
	}
	originalBytes, err := preferredBytes(original, files, map[string]bool{"VERSION": true, "manifest.uuid": true})
	if err != nil {
		return result, err
	}
	if string(bytes.TrimSpace(originalBytes["VERSION"])) != r.CVersion || string(bytes.TrimSpace(originalBytes["manifest.uuid"])) != r.FossilCommit || hash(projectBytes[r.Recipe]) != r.RecipeSHA256 {
		return result, fmt.Errorf("SQLite original/version/vendoring recipe differs from reviewed association")
	}
	result.TranslatorTools, err = declaredTools(ctx, translatorBytes, r.TranslatorTools, nil, nil)
	if err != nil {
		return result, err
	}
	result.Comparisons = map[string]SQLiteVendoring{}
	for _, mapping := range r.Platforms {
		constants, err := sqliteConstants(projectBytes[mapping.Output])
		if err != nil {
			return result, err
		}
		if constants["SQLITE_VERSION"] != "\""+r.CVersion+"\"" || constants["SQLITE_VERSION_NUMBER"] != r.CVersionNumber || constants["SQLITE_SOURCE_ID"] != "\""+r.SourceID+"\"" {
			return result, fmt.Errorf("SQLite generated identities differ from reviewed original")
		}
		comparison, err := CompareSQLiteVendoring(translatorBytes[mapping.Original], projectBytes[mapping.Output])
		if err != nil {
			return result, err
		}
		result.Comparisons[mapping.Platform] = comparison
	}
	return result, nil
}
func sqliteConstants(raw []byte) (map[string]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "sqlite.go", raw, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	for _, d := range parsed.Decls {
		declaration, ok := d.(*ast.GenDecl)
		if !ok || declaration.Tok != token.CONST {
			continue
		}
		for _, spec := range declaration.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			name := value.Names[0].Name
			if name != "SQLITE_VERSION" && name != "SQLITE_VERSION_NUMBER" && name != "SQLITE_SOURCE_ID" {
				continue
			}
			literal, ok := value.Values[0].(*ast.BasicLit)
			if !ok || result[name] != "" {
				return nil, fmt.Errorf("unsupported SQLite generated version constant")
			}
			result[name] = literal.Value
		}
	}
	return result, nil
}
func declaredTools(ctx context.Context, files map[string][]byte, versions map[string]string, proxy map[string]preferredOriginal, moduleFiles map[string][]byte) ([]GeneratorTool, error) {
	requirements, err := goRequirements(ctx, files["go.mod"])
	if err != nil {
		return nil, err
	}
	sums, err := moduleSums(files["go.sum"])
	if err != nil {
		return nil, err
	}
	result := []GeneratorTool{}
	names := []string{}
	for name := range versions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		version := versions[name]
		sum := sums[name+"@"+version]
		if requirements[name] != version || !strings.HasPrefix(sum, "h1:") {
			return nil, fmt.Errorf("backend translator tool lock differs")
		}
		tool := GeneratorTool{Module: name, Version: version, Sum: sum}
		if proxy != nil {
			item, ok := proxy[name]
			if !ok || item.source.Version != version || item.source.Authentication["zip_h1"] != sum {
				return nil, fmt.Errorf("backend generator tool lacks authenticated original")
			}
			original := item.source.Original
			tool.Original = &original
			paths := []string{}
			for name := range item.members {
				if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") || name == "go.mod" || name == "go.sum" || name == "LICENSE" || name == "Makefile" {
					paths = append(paths, name)
				}
			}
			tool.Inputs, err = preferredFacts(item.members, paths)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, tool)
	}
	return result, nil
}
func backendLibc(ctx context.Context, r libcRecipe, project preferredOriginal, upstreams, proxy map[string]preferredOriginal, files, moduleFiles map[string][]byte) (LibcRelationship, error) {
	var result LibcRelationship
	original, err := requireSource(upstreams, r.Original, r.Module, r.Version)
	if err != nil {
		return result, err
	}
	result.Original = original.source.Original
	result.Project = project.source.Original
	result.OriginalInputs, err = originalInputs(original)
	if err != nil {
		return result, err
	}
	names := append([]string{}, r.RecipeInputs...)
	names = append(names, r.RequiredInputs...)
	commonOverlay := false
	for name := range project.members {
		included := !strings.Contains(name, "/") && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") && name != "generate.go"
		for _, prefix := range r.RecipePrefixes {
			if strings.HasPrefix(name, prefix) {
				included = true
			}
		}
		if included {
			names = append(names, name)
			if strings.HasPrefix(name, "internal/overlay/musl/") {
				commonOverlay = true
			}
		}
	}
	if !commonOverlay {
		return result, fmt.Errorf("libc common musl overlays missing")
	}
	result.RecipeInputs, err = preferredFacts(project.members, names)
	if err != nil {
		return result, err
	}
	result.Outputs, err = preferredFacts(project.members, r.Outputs)
	if err != nil {
		return result, err
	}
	selected := map[string]bool{"go.mod": true, "go.sum": true, "builder.json": true}
	projectBytes, err := preferredBytes(project, files, selected)
	if err != nil {
		return result, err
	}
	var builder struct{ Download []struct{ Files []string } }
	if err = json.Unmarshal(projectBytes["builder.json"], &builder); err != nil {
		return result, err
	}
	urls := []string{}
	for _, row := range builder.Download {
		urls = append(urls, row.Files...)
	}
	if len(urls) != 1 || urls[0] != original.source.URL {
		return result, fmt.Errorf("libc preferred source archive differs from reviewed generator input")
	}
	versions := map[string]string{}
	for _, tool := range r.GeneratorTools {
		item, ok := proxy[tool.Module]
		if !ok {
			return result, fmt.Errorf("libc generator original missing")
		}
		if _, err = preferredFacts(item.members, tool.Entrypoints); err != nil {
			return result, err
		}
		versions[tool.Module] = tool.Version
	}
	result.GeneratorTools, err = declaredTools(ctx, projectBytes, versions, proxy, moduleFiles)
	return result, err
}
