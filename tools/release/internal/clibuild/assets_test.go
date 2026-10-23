package clibuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNPMPlatformSubsetTracksRequiredInputs(t *testing.T) {
	web := t.TempDir()
	if err := os.MkdirAll(filepath.Join(web, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	packages := map[string]map[string]any{"": {"name": "fixture"}, "node_modules/vite": {"version": "1.0.0", "integrity": "fixture"}, "node_modules/darwin-only": {"version": "1", "optional": true, "os": []string{"darwin"}}}
	write := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(web, "node_modules/.package-lock.json"), map[string]any{"packages": map[string]any{"node_modules/vite": packages["node_modules/vite"]}})
	lock := filepath.Join(web, "package-lock.json")
	write(lock, map[string]any{"packages": packages})
	if !NPMCurrent(web, "linux", "x64", "glibc") {
		t.Fatal("valid platform subset rejected")
	}
	packages["node_modules/vite"]["version"] = "2"
	write(lock, map[string]any{"packages": packages})
	if NPMCurrent(web, "linux", "x64", "glibc") {
		t.Fatal("changed lock accepted")
	}
	packages["node_modules/vite"]["version"] = "1.0.0"
	packages["node_modules/new-required"] = map[string]any{"version": "1"}
	write(lock, map[string]any{"packages": packages})
	if NPMCurrent(web, "linux", "x64", "glibc") {
		t.Fatal("missing required dependency accepted")
	}
}

func TestSourceFilterRetainsEmbeddedVendorLegalText(t *testing.T) {
	if !sourceAllowed("client/internal/lanassets/site/licenses/backend/go/src/vendor/example/LICENSE") {
		t.Fatal("embedded legal source excluded")
	}
	for _, name := range []string{"client/vendor/example/source.go", "client/go.work", "backend/internal/source.go", "tools/release/.build/psst-release"} {
		if sourceAllowed(name) {
			t.Errorf("development input retained: %s", name)
		}
	}
}

func TestToolchainLegalInputsAffectDigest(t *testing.T) {
	root := t.TempDir()
	license := filepath.Join(root, "LICENSE")
	if err := os.WriteFile(license, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := inputDigest(root, []string{license})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(license, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := inputDigest(root, []string{license})
	if err != nil || first == second {
		t.Fatalf("license change ignored: %v", err)
	}
	first, err = toolchainDigest(root, "go1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err = toolchainDigest(root, "go1.27.4", false)
	if err != nil || first == second {
		t.Fatalf("toolchain version change ignored: %v", err)
	}
	third, err := toolchainDigest(root, "go1.27.4", true)
	if err != nil || second == third {
		t.Fatalf("workspace identity ignored: %v", err)
	}
}
