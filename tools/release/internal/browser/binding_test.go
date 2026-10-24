package browser

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

func bindingFixture(t *testing.T) BindingOptions {
	t.Helper()
	files := browserFixture(t)
	c := source.Context{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}
	common := source.Inventory{SchemaVersion: 2, Source: c}
	originals := map[string][]byte{}
	catalog := map[string]any{"schema_version": 1, "kind": "reviewed-browser-generator-associations", "source_reproduction_verified": false, "browser_module_closure_verified": false, "distribution_authorized": false}
	for index, key := range []string{"vite", "kit", "adapter_static", "svelte_compiler", "svelte_plugin"} {
		name := []string{"fixture", "kit", "adapter", "compiler", "plugin"}[index]
		location := "node_modules/" + name
		packageData, _ := contract.JSON(map[string]string{"name": name, "version": "1.2.3"})
		if index == 0 {
			packageData = files[location+"/package.json"]
		}
		files[location+"/package.json"] = packageData
		files[location+"/generator.js"] = []byte("retained generator original: " + key)
		catalog[key] = map[string]any{"lock_path": location, "name": name, "version": "1.2.3", "members": []string{"generator.js"}}
		var members []source.Member
		for file, body := range files {
			if strings.HasPrefix(file, location+"/") {
				members = append(members, source.Member{Path: "package/" + strings.TrimPrefix(file, location+"/"), Kind: "file", Size: int64(len(body)), SHA256: digest(body)})
			}
		}
		raw, _ := contract.JSON(map[string]any{"schema_version": 1, "kind": "retained-original-source-members", "members": members})
		inventoryPath := "inventory/" + name + ".json"
		originals[inventoryPath] = raw
		common.Sources = append(common.Sources, source.Source{ID: name, Ecosystem: "npm", Name: name, Version: "1.2.3", LockPath: location, Authentication: map[string]string{"archive_root": "package"}, MemberInventory: &source.File{Path: inventoryPath, SHA256: digest(raw), Size: int64(len(raw))}})
	}
	raw, _ := contract.JSON(catalog)
	files[fixedInputs[0]] = raw
	for _, name := range fixedInputs[1:] {
		if _, found := files[name]; !found {
			files[name] = []byte("committed fixture " + name)
		}
	}
	files["static/appearance.js"] = files["build/appearance.js"]
	metadata, _ := contract.JSON(map[string]any{"name": "psst.zip", "version": c.Version, "revision": c.Commit, "license": "AGPL-3.0-only", "source": "https://github.com/" + c.Repository, "source_archive": "https://github.com/" + c.Repository + "/archive/" + c.Commit + ".tar.gz", "notice_files": []string{"/licenses/backend/THIRD_PARTY_NOTICES.txt"}})
	files["static/licenses/release.json"], files["build/licenses/release.json"] = metadata, metadata
	var observation Observation
	json.Unmarshal(files["build/"+inventoryPath], &observation)
	observation.Source.Version, observation.Source.Revision = c.Version, c.Commit
	files["build/"+inventoryPath], _ = contract.JSON(observation)
	git := map[string][]byte{"backend/licenses/THIRD_PARTY_NOTICES.txt": []byte("committed backend notices")}
	for name, body := range files {
		if !strings.HasPrefix(name, "node_modules/") && !strings.HasPrefix(name, "build/") && name != "static/licenses/release.json" {
			git["web/"+name] = body
		}
	}
	return BindingOptions{Source: c, Captured: files, Common: common, CommonFiles: originals, GitFiles: git}
}

func TestCapturedSourcesBindGitRetainedOriginalsAndReviewedCatalog(t *testing.T) {
	o := bindingFixture(t)
	bound, err := BindCaptured(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.NPMSources) != 5 || len(bound.GeneratorInputs) != 10 || bound.CopiedStatic["appearance.js"].SHA256 == "" || bound.Catalog == "" {
		t.Fatal("missing source associations")
	}
	for _, name := range []string{"src/app.js", "node_modules/compiler/generator.js", "scripts/browser-source-recipes.json", "static/appearance.js"} {
		original := o.Captured[name]
		o.Captured[name] = append(bytes.Clone(original), '!')
		if _, err := BindCaptured(context.Background(), o); err == nil {
			t.Errorf("substituted source %s accepted", name)
		}
		o.Captured[name] = original
	}
	o.Captured["unobserved-secret"] = []byte("not selected")
	if _, err := BindCaptured(context.Background(), o); err == nil {
		t.Fatal("extra builder capture accepted")
	}
}

func TestFinalBrowserUsesVerifiedRuntimeAndRejectsUnrelatedChanges(t *testing.T) {
	o := bindingFixture(t)
	bound, err := BindCaptured(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	final := map[string][]byte{}
	for name, body := range o.Captured {
		if strings.HasPrefix(name, "build/") {
			final["srv/web/"+strings.TrimPrefix(name, "build/")] = body
		}
	}
	final["srv/web/licenses/backend/THIRD_PARTY_NOTICES.txt"] = o.GitFiles["backend/licenses/THIRD_PARTY_NOTICES.txt"]
	final["srv/web/licenses/release.json"] = []byte("verified runtime release metadata")
	final["srv/web/licenses/runtime/NOTICE.txt"] = []byte("verified runtime notice")
	verified := source.RuntimeVerification{Images: map[string]oci.Image{"web": {Platform: "linux/amd64", Labels: map[string]string{"org.opencontainers.image.version": o.Source.Version, "org.opencontainers.image.revision": o.Source.Commit}}}, Files: map[string]map[string][]byte{"web": final}, NoticeFiles: map[string][]source.File{}}
	for _, name := range []string{"srv/web/licenses/release.json", "srv/web/licenses/runtime/NOTICE.txt"} {
		body := final[name]
		verified.NoticeFiles["web"] = append(verified.NoticeFiles["web"], source.File{Path: name, SHA256: digest(body), Size: int64(len(body))})
	}
	result, err := VerifyFinal(bound, o.Captured, o.GitFiles, verified, "linux/amd64")
	if err != nil || len(result.UnresolvedJavaScript) != 0 {
		t.Fatal("final matching failed", err)
	}
	for _, name := range []string{"srv/web/app.js", "srv/web/licenses/backend/THIRD_PARTY_NOTICES.txt", "srv/web/licenses/release.json"} {
		original := final[name]
		final[name] = []byte("substituted")
		if _, err := VerifyFinal(bound, o.Captured, o.GitFiles, verified, "linux/amd64"); err == nil {
			t.Errorf("changed final %s accepted", name)
		}
		final[name] = original
	}
	final["srv/web/extra.js"] = []byte("unrelated output")
	if _, err := VerifyFinal(bound, o.Captured, o.GitFiles, verified, "linux/amd64"); err == nil {
		t.Fatal("unrelated final file accepted")
	}
}

func TestCaptureRoundTripAndPhysicalHeaderBounds(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "capture.tar")
	files := browserFixture(t)
	written, err := WriteCapture(name, files)
	if err != nil {
		t.Fatal(err)
	}
	read, fact, err := ReadCapture(name)
	if err != nil || written != fact || !bytes.Equal(read["src/app.js"], files["src/app.js"]) {
		t.Fatal("capture round trip failed", err)
	}
	if _, err := WriteCapture(name, files); err == nil {
		t.Fatal("overwrote retained capture")
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeXHeader, tar.TypeGNULongName} {
		var raw bytes.Buffer
		writer := tar.NewWriter(&raw)
		if err := writer.WriteHeader(&tar.Header{Name: "input", Linkname: "outside", Typeflag: kind}); err != nil {
			continue
		}
		writer.Close()
		os.WriteFile(filepath.Join(dir, "unsafe.tar"), raw.Bytes(), 0600)
		if _, _, err := ReadCapture(filepath.Join(dir, "unsafe.tar")); err == nil {
			t.Fatal("unsupported header accepted")
		}
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '!')
	os.WriteFile(filepath.Join(dir, "tail.tar"), raw, 0600)
	if _, _, err := ReadCapture(filepath.Join(dir, "tail.tar")); err == nil {
		t.Fatal("trailing payload accepted")
	}
}
