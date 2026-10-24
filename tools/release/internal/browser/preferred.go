package browser

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

const preferredCatalogPath = "scripts/browser-preferred-sources.json"

type preferredRule struct {
	Package      string              `json:"package"`
	Match        string              `json:"match,omitempty"`
	Matches      []string            `json:"matches,omitempty"`
	Prefix       string              `json:"match_prefix,omitempty"`
	Suffix       string              `json:"match_suffix,omitempty"`
	Method       string              `json:"method"`
	Upstream     string              `json:"upstream,omitempty"`
	SourcePrefix string              `json:"source_prefix,omitempty"`
	SourceSuffix string              `json:"source_suffix,omitempty"`
	Inputs       []string            `json:"inputs,omitempty"`
	Recipes      []string            `json:"recipes,omitempty"`
	Mapping      map[string]string   `json:"mapping,omitempty"`
	Additional   map[string][]string `json:"additional,omitempty"`
}
type preferredCatalog struct {
	SchemaVersion int             `json:"schema_version"`
	Kind          string          `json:"kind"`
	Reproduced    *bool           `json:"generated_byte_reproduction_verified"`
	Rules         []preferredRule `json:"rules"`
}
type PreferredInput struct {
	Upstream string      `json:"upstream"`
	Commit   string      `json:"commit"`
	Original source.File `json:"original"`
	Member   source.File `json:"member"`
}
type PreferredRelationship struct {
	Input      File             `json:"observed_input"`
	Method     string           `json:"relationship"`
	Inputs     []PreferredInput `json:"inputs"`
	Recipes    []PreferredInput `json:"recipes"`
	Reproduced bool             `json:"generated_byte_reproduction_verified"`
}
type GeneratorAssociation struct {
	Generator  string `json:"generator"`
	Physical   string `json:"physical_source,omitempty"`
	Rendered   bool   `json:"rendered_in_client_chunks"`
	Reproduced bool   `json:"generated_byte_reproduction_verified"`
}
type PreferredFacts struct {
	Catalog    File                             `json:"reviewed_catalog"`
	Modules    map[string]PreferredRelationship `json:"modules"`
	Generators map[string]PreferredRelationship `json:"generators"`
	Virtual    map[string]GeneratorAssociation  `json:"virtual_inputs"`
	Generated  map[string]GeneratorAssociation  `json:"generated_inputs"`
	Reproduced bool                             `json:"generated_byte_reproduction_verified"`
}

type preferredVerifier struct {
	reader *source.OriginalReader
	text   map[string]map[string][]byte
}

func (v preferredVerifier) facts(id string, names []string, pkg Package) ([]PreferredInput, error) {
	item, err := v.reader.Item(id)
	if err != nil {
		return nil, err
	}
	if pkg.Name != "" && item.Associations["npm:"+pkg.Name] != pkg.Version {
		return nil, fmt.Errorf("preferred upstream differs from locked browser package")
	}
	result := []PreferredInput{}
	for _, name := range names {
		member, err := v.reader.Fact(id, name)
		if err != nil {
			return nil, err
		}
		result = append(result, PreferredInput{id, item.Version, item.Original, member})
	}
	return result, nil
}
func ruleFor(rules []preferredRule, pkg, relative string) (preferredRule, error) {
	for _, rule := range rules {
		if rule.Package != pkg || rule.Match != "" && rule.Match != relative || len(rule.Matches) > 0 && !slices.Contains(rule.Matches, relative) || !strings.HasPrefix(relative, rule.Prefix) || !strings.HasSuffix(relative, rule.Suffix) {
			continue
		}
		if rule.Method == "generated-mapping" {
			if _, found := rule.Mapping[relative]; !found {
				continue
			}
		}
		return rule, nil
	}
	return preferredRule{}, fmt.Errorf("browser input lacks reviewed preferred-source relationship: %s/%s", pkg, relative)
}

// VerifyPreferred associates actual observed npm inputs with retained preferred
// originals and finite reviewed recipes. It never parses generator functions to
// infer their behavior and never claims generated-byte reproduction.
func VerifyPreferred(bound BoundFacts, captured map[string][]byte, common source.Inventory, files map[string][]byte) (PreferredFacts, error) {
	result := PreferredFacts{Modules: map[string]PreferredRelationship{}, Generators: map[string]PreferredRelationship{}, Virtual: map[string]GeneratorAssociation{}, Generated: map[string]GeneratorAssociation{}}
	if common.Source.Version != bound.Version || common.Source.Commit != bound.Revision {
		return result, fmt.Errorf("browser preferred-source identity differs")
	}
	raw := captured[preferredCatalogPath]
	if bound.GitInputs[preferredCatalogPath] != blob(raw) {
		return result, fmt.Errorf("preferred-source catalog is not bound to exact Git bytes")
	}
	var catalog preferredCatalog
	if err := contract.Decode(raw, maximumFile, &catalog); err != nil {
		return result, err
	}
	if catalog.SchemaVersion != 1 || catalog.Kind != "reviewed-browser-preferred-source-associations" || catalog.Reproduced == nil || *catalog.Reproduced || len(catalog.Rules) == 0 || len(catalog.Rules) > 128 {
		return result, fmt.Errorf("invalid reviewed browser preferred-source catalog")
	}
	for _, rule := range catalog.Rules {
		if rule.Package == "" || !slices.Contains([]string{"direct", "generated", "generated-direct", "generated-mapping", "npm-original", "typescript", "qr-embedded", "lucide-icon"}, rule.Method) || rule.Method != "npm-original" && rule.Upstream == "" {
			return result, fmt.Errorf("invalid browser preferred-source recipe")
		}
		if slices.Contains([]string{"generated", "generated-direct"}, rule.Method) && (len(rule.Inputs) == 0 || len(rule.Recipes) == 0) || rule.Method == "generated-mapping" && (len(rule.Mapping) == 0 || len(rule.Recipes) == 0) {
			return result, fmt.Errorf("reviewed generator lacks preferred originals or recipes")
		}
	}
	reader, err := source.NewOriginalReader(common, files)
	if err != nil {
		return result, err
	}
	v := preferredVerifier{reader: reader, text: map[string]map[string][]byte{}}
	var observation Observation
	if err := contract.Decode(captured["build/"+inventoryPath], maximumFile, &observation); err != nil {
		return result, err
	}
	type selectedInput struct {
		name, relative string
		pkg            Package
		rule           preferredRule
		rendered       bool
	}
	selected := map[string]selectedInput{}
	for _, row := range observation.Modules {
		var module Module
		if err := json.Unmarshal(row, &module); err != nil {
			return result, err
		}
		if module.Package == nil {
			continue
		}
		if module.Path == nil {
			return result, fmt.Errorf("observed package lacks physical source path")
		}
		name := *module.Path
		selected[name] = selectedInput{name, strings.TrimPrefix(name, module.Package.LockPath+"/"), *module.Package, preferredRule{}, true}
	}
	for name := range bound.GeneratorInputs {
		pkgName, location, valid := packageLocation(name)
		if !valid {
			return result, fmt.Errorf("invalid captured browser generator path")
		}
		relative := strings.TrimPrefix(name, location+"/")
		if relative == "package.json" {
			continue
		} // BoundCaptured verifies exact locked package metadata.
		var installed struct{ Name, Version string }
		if json.Unmarshal(captured[location+"/package.json"], &installed) != nil || installed.Name != pkgName {
			return result, fmt.Errorf("missing generator package identity")
		}
		item := selected[name]
		item.name, item.relative, item.pkg = name, relative, Package{Name: pkgName, Version: installed.Version, LockPath: location}
		selected[name] = item
	}
	textNeeded := map[string]map[string]bool{}
	need := func(id string, names ...string) {
		if textNeeded[id] == nil {
			textNeeded[id] = map[string]bool{}
		}
		for _, name := range names {
			textNeeded[id][name] = true
		}
	}
	for name, item := range selected {
		rule, err := ruleFor(catalog.Rules, item.pkg.Name, item.relative)
		if err != nil {
			return result, err
		}
		item.rule = rule
		selected[name] = item
		switch rule.Method {
		case "typescript":
			need(rule.Upstream, "package.json", "tsconfig.json", "package-lock.json")
			need("jsbt", "package.json", "tsconfig.json")
		case "qr-embedded":
			need(rule.Upstream, item.relative+".map")
		case "lucide-icon":
			icon := strings.TrimSuffix(strings.TrimPrefix(item.relative, "dist/icons/"), ".svelte")
			if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(icon) {
				return result, fmt.Errorf("invalid generated icon identity")
			}
			need(rule.Upstream, "icons/"+icon+".svg", "icons/"+icon+".json")
		}
		if item.pkg.Name == "vite" && rule.Method == "generated" {
			need("vite", "packages/vite/package.json", "pnpm-lock.yaml")
			need("rollup-commonjs", "packages/commonjs/package.json")
		}
	}
	for id, paths := range textNeeded {
		names := []string{}
		for name := range paths {
			names = append(names, name)
		}
		v.text[id], err = reader.Read(id, names)
		if err != nil {
			return result, err
		}
	}
	for name, item := range selected {
		relationship, err := v.relationship(captured[name], item.relative, item.pkg, item.rule)
		if err != nil {
			return result, err
		}
		if item.rendered {
			result.Modules[name] = relationship
		}
		if _, found := bound.GeneratorInputs[name]; found {
			result.Generators[name] = relationship
		}
	}
	if err := verifyGenerators(bound, captured, observation, &result); err != nil {
		return result, err
	}
	result.Catalog = fileFact(raw)
	return result, nil
}

func (v preferredVerifier) relationship(raw []byte, relative string, pkg Package, rule preferredRule) (PreferredRelationship, error) {
	result := PreferredRelationship{Input: fileFact(raw), Method: rule.Method, Inputs: []PreferredInput{}, Recipes: []PreferredInput{}}
	if rule.Method == "npm-original" {
		return result, nil
	}
	inputs := slices.Clone(rule.Inputs)
	switch rule.Method {
	case "direct", "qr-embedded":
		inputs = []string{rule.SourcePrefix + relative}
		fact, err := v.reader.Fact(rule.Upstream, inputs[0])
		if err != nil || fact.SHA256 != digest(raw) || fact.Size != int64(len(raw)) {
			return result, fmt.Errorf("installed browser input differs from preferred original")
		}
	case "typescript":
		inputs = []string{rule.SourcePrefix + strings.TrimSuffix(relative, rule.Suffix) + rule.SourceSuffix}
		if err := v.verifyTypeScript(rule.Upstream); err != nil {
			return result, err
		}
	case "generated-mapping":
		inputs = []string{rule.Mapping[relative]}
	case "generated-direct":
		fact, err := v.reader.Fact(rule.Upstream, relative)
		if err != nil || fact.SHA256 != digest(raw) || fact.Size != int64(len(raw)) {
			return result, fmt.Errorf("generated input differs from committed upstream emission")
		}
	case "lucide-icon":
		icon := strings.TrimSuffix(strings.TrimPrefix(relative, "dist/icons/"), ".svelte")
		inputs = []string{"icons/" + icon + ".svg", "icons/" + icon + ".json"}
		if err := verifyIcon(raw, icon, v.text[rule.Upstream][inputs[0]], v.text[rule.Upstream][inputs[1]]); err != nil {
			return result, err
		}
	}
	var err error
	result.Inputs, err = v.facts(rule.Upstream, inputs, pkg)
	if err != nil {
		return result, err
	}
	result.Recipes, err = v.facts(rule.Upstream, rule.Recipes, pkg)
	if err != nil {
		return result, err
	}
	for id, names := range rule.Additional {
		facts, err := v.facts(id, names, pkg)
		if err != nil {
			return result, err
		}
		result.Recipes = append(result.Recipes, facts...)
	}
	if rule.Method == "typescript" {
		facts, err := v.facts("jsbt", []string{"package.json", "tsconfig.json"}, Package{})
		if err != nil {
			return result, err
		}
		result.Recipes = append(result.Recipes, facts...)
	}
	if rule.Method == "qr-embedded" {
		facts, err := v.qrOriginals(rule.Upstream, relative, pkg)
		if err != nil {
			return result, err
		}
		result.Inputs = append(result.Inputs, facts...)
	}
	if pkg.Name == "vite" && rule.Method == "generated" {
		if err := v.verifyVite(pkg); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (v preferredVerifier) verifyTypeScript(id string) error {
	var manifest struct{ Scripts map[string]string }
	var config struct {
		Root string `json:"rootDir"`
		Out  string `json:"outDir"`
	}
	var parsed struct {
		Extends         string
		CompilerOptions json.RawMessage
	}
	var lock struct {
		Packages map[string]struct{ Version, Resolved, Integrity string }
	}
	var external struct{ Name, Version string }
	text := v.text[id]
	if json.Unmarshal(text["package.json"], &manifest) != nil || json.Unmarshal(text["tsconfig.json"], &parsed) != nil || json.Unmarshal(parsed.CompilerOptions, &config) != nil || json.Unmarshal(text["package-lock.json"], &lock) != nil || json.Unmarshal(v.text["jsbt"]["package.json"], &external) != nil {
		return fmt.Errorf("invalid retained TypeScript build configuration")
	}
	dependency, found := lock.Packages["node_modules/@paulmillr/jsbt"]
	if manifest.Scripts["build"] != "tsc" || config.Root != "src" || config.Out != "." || parsed.Extends != "@paulmillr/jsbt/tsconfig.json" || !found || dependency.Version != external.Version || external.Name != "@paulmillr/jsbt" || dependency.Resolved != "https://registry.npmjs.org/@paulmillr/jsbt/-/jsbt-"+dependency.Version+".tgz" {
		return fmt.Errorf("retained TypeScript emission recipe differs")
	}
	// ReadCommon already validates the independent build-configuration catalog's
	// exact integrity and config hash. Bind its actual lock identity here too.
	item, err := v.reader.Relationship("jsbt")
	if err != nil {
		return err
	}
	if item.Kind != "build-configuration" || item.Name != external.Name || item.Version != dependency.Version || item.Integrity != dependency.Integrity || item.ConfigurationSHA256 != digest(v.text["jsbt"]["tsconfig.json"]) {
		return fmt.Errorf("TypeScript external configuration lock differs")
	}
	var base map[string]json.RawMessage
	if contract.Decode(v.text["jsbt"]["tsconfig.json"], maximumFile, &base) != nil || base["compilerOptions"] == nil || base["extends"] != nil {
		return fmt.Errorf("unsupported external TypeScript configuration")
	}
	return nil
}

func (v preferredVerifier) verifyVite(pkg Package) error {
	var manifest struct {
		Name, Version   string
		DevDependencies map[string]string
	}
	var commonjs struct{ Name, Version string }
	if json.Unmarshal(v.text["vite"]["packages/vite/package.json"], &manifest) != nil || json.Unmarshal(v.text["rollup-commonjs"]["packages/commonjs/package.json"], &commonjs) != nil {
		return fmt.Errorf("invalid retained Vite generator manifests")
	}
	relation, err := v.reader.Relationship("rollup-commonjs")
	if err != nil {
		return err
	}
	if relation.Kind != "embedded-component" || relation.Name != commonjs.Name || relation.Version != commonjs.Version || commonjs.Name != "@rollup/plugin-commonjs" || manifest.Name != pkg.Name || manifest.Version != pkg.Version || manifest.DevDependencies[commonjs.Name] != "^"+commonjs.Version {
		return fmt.Errorf("Vite generator component identity differs from reviewed source")
	}
	importers := regexp.MustCompile(`(?ms)^  packages/vite:\n(.*?)(?:^  \S|^\S|\z)`).FindAllSubmatch(v.text["vite"]["pnpm-lock.yaml"], -1)
	if len(importers) != 1 {
		return fmt.Errorf("missing exact Vite lock importer")
	}
	versions := regexp.MustCompile(`(?m)^      '@rollup/plugin-commonjs':\n        specifier: [^\n]+\n        version: ([0-9]+\.[0-9]+\.[0-9]+)(?:\([^\n]*\))?\n`).FindAllSubmatch(importers[0][1], -1)
	if len(versions) != 1 || string(versions[0][1]) != commonjs.Version {
		return fmt.Errorf("Vite generator lock differs from retained preferred component")
	}
	return nil
}

func verifyGenerators(bound BoundFacts, captured map[string][]byte, observation Observation, result *PreferredFacts) error {
	var catalog struct {
		Vite struct {
			LockPath  string `json:"lock_path"`
			Generator string
			Suffixes  []string `json:"commonjs_suffixes"`
			Helpers   []string `json:"helper_ids"`
		}
		Kit struct {
			LockPath  string `json:"lock_path"`
			Generated map[string]struct{ Generator, Function, Invocation string }
			Nodes     struct{ Prefix, Generator, Function, Invocation string } `json:"generated_nodes"`
		}
		Adapter struct {
			LockPath string `json:"lock_path"`
			Fallback string
		} `json:"adapter_static"`
	}
	if json.Unmarshal(captured[fixedInputs[0]], &catalog) != nil {
		return fmt.Errorf("invalid reviewed generator associations")
	}
	if catalog.Adapter.Fallback != "index.html" || captured["build/"+catalog.Adapter.Fallback] == nil {
		return fmt.Errorf("static adapter fallback output missing")
	}
	generator := catalog.Vite.LockPath + "/" + catalog.Vite.Generator
	candidates := map[string]GeneratorAssociation{}
	add := func(id, physical string) {
		candidates["virtual:"+strings.TrimPrefix(digest([]byte(id)), "sha256:")] = GeneratorAssociation{Generator: generator, Physical: physical}
	}
	for name := range bound.Inputs {
		if strings.HasPrefix(name, "node_modules/") {
			for _, suffix := range catalog.Vite.Suffixes {
				if !slices.Contains([]string{"?commonjs-exports", "?commonjs-module", "?commonjs-es-import"}, suffix) {
					return fmt.Errorf("unsupported reviewed virtual module grammar")
				}
				add("\x00<root>/"+name+suffix, name)
			}
		}
	}
	for _, helper := range catalog.Vite.Helpers {
		if helper != "commonjsHelpers.js" && helper != "vite/preload-helper.js" {
			return fmt.Errorf("unsupported reviewed virtual helper")
		}
		add("\x00"+helper, "")
	}
	for excluded, rows := range [][]json.RawMessage{observation.Modules, observation.Excluded} {
		for _, row := range rows {
			var module Module
			if json.Unmarshal(row, &module) != nil {
				return fmt.Errorf("invalid observed module")
			}
			if module.Kind == "virtual" {
				association, found := candidates[module.ID]
				if !found {
					if excluded == 0 {
						return fmt.Errorf("rendered virtual module lacks a reviewed generator")
					}
					continue
				}
				if _, found := result.Generators[association.Generator]; !found {
					return fmt.Errorf("virtual module generator is not retained")
				}
				if association.Physical != "" && excluded == 0 {
					if _, found := result.Modules[association.Physical]; !found {
						return fmt.Errorf("virtual wrapper physical source missing")
					}
				}
				association.Rendered = excluded == 0
				result.Virtual[module.ID] = association
			} else if module.Kind == "generated-application" {
				if module.Path == nil {
					return fmt.Errorf("generated module lacks physical path")
				}
				name := *module.Path
				recipe, found := catalog.Kit.Generated[name]
				if !found && strings.HasPrefix(name, catalog.Kit.Nodes.Prefix) && catalog.Kit.Nodes.Prefix != "" {
					index := strings.TrimSuffix(strings.TrimPrefix(name, catalog.Kit.Nodes.Prefix), ".js")
					if regexp.MustCompile(`^(0|[1-9][0-9]{0,4})$`).MatchString(index) {
						recipe.Generator = catalog.Kit.Nodes.Generator
						found = true
					}
				}
				if !found {
					if excluded == 0 {
						return fmt.Errorf("rendered application input lacks reviewed generator")
					}
					continue
				}
				generator := catalog.Kit.LockPath + "/" + recipe.Generator
				if _, found := result.Generators[generator]; !found {
					return fmt.Errorf("application generator source missing")
				}
				result.Generated[name] = GeneratorAssociation{Generator: generator, Rendered: excluded == 0}
			}
		}
	}
	if _, found := result.Generators["node_modules/vite/LICENSE.md"]; found {
		original := captured["node_modules/vite/LICENSE.md"]
		if !bytes.Equal(original, captured["build/licenses/vite-generated-browser-helpers-LICENSE.md"]) {
			return fmt.Errorf("distributed Vite generated helper license differs from original")
		}
	}
	return nil
}

func (v preferredVerifier) qrOriginals(id, relative string, pkg Package) ([]PreferredInput, error) {
	var mapping struct {
		Sources  []string
		Contents []string `json:"sourcesContent"`
	}
	raw := v.text[id][relative+".map"]
	if contract.Decode(raw, maximumFile, new(any)) != nil || json.Unmarshal(raw, &mapping) != nil || len(mapping.Sources) == 0 || len(mapping.Sources) > 100 || len(mapping.Sources) != len(mapping.Contents) {
		return nil, fmt.Errorf("invalid retained QR embedded source map")
	}
	result, err := v.facts(id, []string{relative + ".map"}, pkg)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for index, name := range mapping.Sources {
		if seen[name] {
			return nil, fmt.Errorf("duplicate QR source map member")
		}
		seen[name] = true
		upstream, path := id, name
		if strings.HasPrefix(name, "node_modules/jsqr-es6/src/") {
			upstream, path = "qr-scanner-decoder", strings.TrimPrefix(name, "node_modules/jsqr-es6/")
		} else if name != "src/qr-scanner.ts" && name != "src/worker.ts" {
			return nil, fmt.Errorf("unknown QR embedded original")
		}
		facts, err := v.facts(upstream, []string{path}, pkg)
		if err != nil || facts[0].Member.SHA256 != digest([]byte(mapping.Contents[index])) || facts[0].Member.Size != int64(len(mapping.Contents[index])) {
			return nil, fmt.Errorf("embedded QR source differs from preferred original")
		}
		result = append(result, facts...)
	}
	return result, nil
}

func verifyIcon(raw []byte, name string, svg, metadata []byte) error {
	if bytes.Contains(bytes.ToUpper(svg), []byte("<!DOCTYPE")) || bytes.Contains(bytes.ToUpper(svg), []byte("<!ENTITY")) {
		return fmt.Errorf("unsupported SVG declaration")
	}
	decoder := xml.NewDecoder(bytes.NewReader(svg))
	depth := 0
	nodes := []any{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 && element.Name.Local != "svg" || depth > 2 {
				return fmt.Errorf("invalid original icon shape")
			}
			if depth == 2 {
				attrs := map[string]string{}
				for _, attr := range element.Attr {
					if _, exists := attrs[attr.Name.Local]; exists {
						return fmt.Errorf("ambiguous SVG attributes")
					}
					attrs[attr.Name.Local] = attr.Value
				}
				nodes = append(nodes, []any{element.Name.Local, attrs})
			}
		case xml.EndElement:
			depth--
		}
	}
	if depth != 0 || len(nodes) == 0 {
		return fmt.Errorf("missing original SVG nodes")
	}
	expected := map[string]any{"name": name, "size": float64(24), "node": nodes}
	var meta struct{ Aliases []json.RawMessage }
	if contract.Decode(metadata, maximumFile, new(any)) != nil || json.Unmarshal(metadata, &meta) != nil {
		return fmt.Errorf("invalid icon metadata")
	}
	if len(meta.Aliases) > 0 {
		aliases := []any{}
		for _, raw := range meta.Aliases {
			var alias string
			if json.Unmarshal(raw, &alias) != nil {
				var object struct{ Name string }
				if json.Unmarshal(raw, &object) != nil || object.Name == "" {
					return fmt.Errorf("invalid icon alias")
				}
				alias = object.Name
			}
			aliases = append(aliases, alias)
		}
		expected["aliases"] = aliases
	}
	matches := regexp.MustCompile(`const iconData = (.*?);`).FindAllSubmatch(raw, -1)
	if len(matches) != 1 {
		return fmt.Errorf("missing generated icon data")
	}
	var actual any
	if contract.Decode(matches[0][1], maximumFile, &actual) != nil {
		return fmt.Errorf("invalid generated icon data")
	}
	want, _ := contract.JSON(expected)
	got, _ := contract.JSON(actual)
	if !bytes.Equal(want, got) {
		return fmt.Errorf("generated icon differs from original SVG/metadata")
	}
	return nil
}
