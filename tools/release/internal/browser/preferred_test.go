package browser

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

func preferredFixture(t *testing.T) (BoundFacts, map[string][]byte, source.Inventory, map[string][]byte) {
	t.Helper()
	commit := strings.Repeat("a", 40)
	original := []byte("export const fixture = 1;")
	upstreamFiles := map[string][]byte{"packages/svelte/src/index.js": original}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	members := []source.Member{}
	for name, data := range upstreamFiles {
		full := "svelte-" + commit + "/" + name
		if err := tw.WriteHeader(&tar.Header{Name: full, Size: int64(len(data)), Mode: 0644, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		tw.Write(data)
		members = append(members, source.Member{Path: full, Kind: "file", Size: int64(len(data)), SHA256: digest(data)})
	}
	tw.Close()
	gz.Close()
	memberRaw, _ := contract.JSON(map[string]any{"schema_version": 1, "kind": "retained-original-source-members", "origin": "sveltejs/svelte", "version": commit, "members": members})
	common := source.Inventory{SchemaVersion: 2, Source: source.Context{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: commit}, Sources: []source.Source{{ID: "svelte", Ecosystem: "upstream", Name: "sveltejs/svelte", Version: commit, Original: source.File{Path: "archives/svelte.tar.gz", SHA256: digest(compressed.Bytes()), Size: int64(compressed.Len())}, Associations: map[string]string{"npm:svelte": "1.2.3"}, MemberInventory: &source.File{Path: "inventory/svelte.json", SHA256: digest(memberRaw), Size: int64(len(memberRaw))}}}}
	commonCatalog, _ := contract.JSON(map[string]any{"schema_version": 1, "kind": "pinned-upstream-application-source-inputs", "upstreams": []any{map[string]any{"id": "svelte"}}})
	files := map[string][]byte{"archives/svelte.tar.gz": compressed.Bytes(), "inventory/svelte.json": memberRaw, "inputs/tools/upstream-application-sources.json": commonCatalog}
	catalog, _ := contract.JSON(map[string]any{"schema_version": 1, "kind": "reviewed-browser-preferred-source-associations", "generated_byte_reproduction_verified": false, "rules": []preferredRule{{Package: "svelte", Method: "direct", Upstream: "svelte", SourcePrefix: "packages/svelte/"}}})
	name := "node_modules/svelte/src/index.js"
	pkg := Package{Name: "svelte", Version: "1.2.3", LockPath: "node_modules/svelte"}
	module := Module{ID: "file:" + name, Kind: "package-source", Path: &name, Package: &pkg, Source: stringRef(digest(original)), Rendered: []Contribution{{"app.js", 1}}}
	moduleRaw, _ := contract.JSON(module)
	observation, _ := contract.JSON(Observation{Modules: []json.RawMessage{moduleRaw}, Excluded: []json.RawMessage{}})
	generatorCatalog := []byte(`{"vite":{"lock_path":"node_modules/vite","generator":"bundle.js","commonjs_suffixes":[],"helper_ids":[]},"kit":{"lock_path":"node_modules/@sveltejs/kit","generated":{},"generated_nodes":{}},"adapter_static":{"fallback":"index.html"}}`)
	captured := map[string][]byte{preferredCatalogPath: catalog, fixedInputs[0]: generatorCatalog, name: original, "build/" + inventoryPath: observation, "build/index.html": []byte("fallback")}
	bound := BoundFacts{Facts: Facts{Version: common.Source.Version, Revision: commit, Inputs: map[string]File{name: fileFact(original)}}, GitInputs: map[string]string{preferredCatalogPath: blob(catalog)}, GeneratorInputs: map[string]File{}}
	return bound, captured, common, files
}

func TestPreferredBrowserRejectsChangedOriginalsUnknownRulesAndVirtualClaims(t *testing.T) {
	bound, captured, common, files := preferredFixture(t)
	facts, err := VerifyPreferred(bound, captured, common, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Modules) != 1 || facts.Reproduced || facts.Modules["node_modules/svelte/src/index.js"].Inputs[0].Commit != common.Source.Commit {
		t.Fatal("preferred source identity changed")
	}
	name := "node_modules/svelte/src/index.js"
	original := captured[name]
	captured[name] = []byte("substituted original")
	if _, err := VerifyPreferred(bound, captured, common, files); err == nil {
		t.Fatal("unmatched preferred source accepted")
	}
	captured[name] = original
	var observation Observation
	json.Unmarshal(captured["build/"+inventoryPath], &observation)
	virtual, _ := contract.JSON(Module{ID: "virtual:" + strings.Repeat("b", 64), Kind: "virtual", Rendered: []Contribution{{"app.js", 1}}})
	observation.Modules = append(observation.Modules, virtual)
	captured["build/"+inventoryPath], _ = contract.JSON(observation)
	if _, err := VerifyPreferred(bound, captured, common, files); err == nil {
		t.Fatal("unknown rendered virtual source accepted")
	}
	bound, captured, common, files = preferredFixture(t)
	common.Sources[0].Associations["npm:svelte"] = "9.9.9"
	if _, err := VerifyPreferred(bound, captured, common, files); err == nil {
		t.Fatal("wrong preferred package version accepted")
	}
}

func TestGeneratedIconKeepsRealOriginalContentChecks(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M1 2"/></svg>`)
	metadata := []byte(`{"aliases":["fixture-alias"]}`)
	data := []byte(`const iconData = {"name":"fixture","size":24,"node":[["path",{"d":"M1 2"}]],"aliases":["fixture-alias"]};`)
	if err := verifyIcon(data, "fixture", svg, metadata); err != nil {
		t.Fatal(err)
	}
	if err := verifyIcon(bytes.ReplaceAll(data, []byte("M1 2"), []byte("M3 4")), "fixture", svg, metadata); err == nil {
		t.Fatal("changed generated icon accepted")
	}
	if err := verifyIcon(data, "fixture", []byte(`<!DOCTYPE svg><svg><path d="M1 2"/></svg>`), metadata); err == nil {
		t.Fatal("SVG declarations accepted")
	}
}
