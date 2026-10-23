package browser

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func stringRef(value string) *string { return &value }

func browserFixture(t *testing.T) map[string][]byte {
	t.Helper()
	packageJSON := []byte(`{"name":"fixture","version":"1.2.3"}`)
	files := map[string][]byte{
		"src/app.js":                        []byte("import 'fixture';"),
		"node_modules/fixture/index.js":     []byte("export const value = 1;"),
		"node_modules/fixture/package.json": packageJSON,
		"build/app.js":                      []byte("minified browser fixture"),
		"build/appearance.js":               []byte("copied browser source"),
	}
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))
	lock, _ := contract.JSON(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{"name": "application"}, "node_modules/fixture": map[string]any{"version": "1.2.3", "integrity": integrity, "resolved": "https://registry.npmjs.org/fixture/-/fixture-1.2.3.tgz"}}})
	files["package-lock.json"] = lock
	physical := func(name, kind string) Module {
		return Module{ID: "file:" + name, Kind: kind, Path: stringRef(name), Source: stringRef(digest(files[name])), Rendered: []Contribution{{"app.js", 1}}}
	}
	app := physical("src/app.js", "application-source")
	dependency := physical("node_modules/fixture/index.js", "package-source")
	dependency.Package = &Package{Name: "fixture", Version: "1.2.3", Integrity: integrity, LockPath: "node_modules/fixture", PackageJSON: digest(packageJSON)}
	virtual := Module{ID: "virtual:" + strings.Repeat("a", 64), Kind: "virtual", Rendered: []Contribution{{"app.js", 1}}}
	excluded := app
	excluded.ID += ":variant:" + strings.Repeat("b", 64)
	excluded.Rendered = []Contribution{}
	excluded.Reason = "not-in-client-chunks"
	inventory := Observation{SchemaVersion: 1, Kind: "browser-module-inventory", Execution: "vite-rollup-client-build", Outputs: []Output{{"app.js", "chunk", digest(files["build/app.js"]), int64(len(files["build/app.js"]))}}, Metadata: []Output{}, Modules: []json.RawMessage{}, Excluded: []json.RawMessage{}}
	inventory.Source.Version = "dev"
	inventory.Source.Revision = "main"
	inventory.Source.Lock.File = "package-lock.json"
	inventory.Source.Lock.SHA256 = digest(lock)
	for _, module := range []Module{app, dependency, virtual} {
		raw, _ := contract.JSON(module)
		inventory.Modules = append(inventory.Modules, raw)
	}
	raw, _ := contract.JSON(excluded)
	inventory.Excluded = append(inventory.Excluded, raw)
	raw, err := contract.JSON(inventory)
	if err != nil {
		t.Fatal(err)
	}
	files["build/"+inventoryPath] = raw
	return files
}

func TestRealObservationContractAndSubstitutedBuilderInputs(t *testing.T) {
	files := browserFixture(t)
	facts, err := VerifyCaptured(files, "dev", "main")
	if err != nil {
		t.Fatal(err)
	}
	if facts.Physical != 3 || facts.Virtual != 1 || facts.Excluded != 1 || len(facts.Packages) != 1 || len(facts.Unattributed) != 1 || facts.Reproduced || facts.Closure || facts.Authorized {
		t.Fatal("observed identity or claims changed")
	}
	for _, name := range []string{"src/app.js", "node_modules/fixture/package.json", "build/app.js", "package-lock.json"} {
		original := files[name]
		files[name] = append(append([]byte(nil), original...), byte('!'))
		if _, err := VerifyCaptured(files, "dev", "main"); err == nil {
			t.Errorf("substituted %s accepted", name)
		}
		files[name] = original
	}
}

func TestBrowserObservationRejectsAuthorityAndUnsafeArchiveIdentity(t *testing.T) {
	files := browserFixture(t)
	var record map[string]any
	json.Unmarshal(files["build/"+inventoryPath], &record)
	record["publication_authorized"] = true
	raw, _ := contract.JSON(record)
	files["build/"+inventoryPath] = raw
	if _, err := VerifyCaptured(files, "dev", "main"); err == nil {
		t.Fatal("browser observation self-authorized publication")
	}
	files = browserFixture(t)
	files["../outside"] = []byte("rejected")
	if _, err := VerifyCaptured(files, "dev", "main"); err == nil {
		t.Fatal("unsafe capture path accepted")
	}
	files = browserFixture(t)
	json.Unmarshal(files["build/"+inventoryPath], &record)
	record["excluded_modules"] = nil
	raw, _ = contract.JSON(record)
	files["build/"+inventoryPath] = raw
	if _, err := VerifyCaptured(files, "dev", "main"); err == nil {
		t.Fatal("null module coverage accepted")
	}
}
