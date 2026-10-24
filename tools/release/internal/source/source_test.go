package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func original(t *testing.T, headers []*tar.Header, contents map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	writer := tar.NewWriter(gz)
	for _, header := range headers {
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := writer.Write(contents[header.Name]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func gitFixture(t *testing.T, files map[string][]byte) (string, string) {
	t.Helper()
	root := t.TempDir()
	for name, raw := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_COMMITTER_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture failed: %v", err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("remote", "add", "origin", "https://github.com/endorses/psst.zip.git")
	git("add", ".")
	git("commit", "-qm", "Disposable source fixture")
	return root, git("rev-parse", "HEAD")
}
func upstreamFixture(t *testing.T) (CommonOptions, []byte) {
	t.Helper()
	commit := strings.Repeat("b", 40)
	prefix := "fixture-" + commit
	contents := map[string][]byte{prefix + "/LICENSE": []byte("Disposable source terms\n"), prefix + "/package.json": []byte(`{"name":"fixture","version":"1.2.3"}`), prefix + "/src/index.js": []byte("export const fixture = true;\n")}
	headers := []*tar.Header{{Name: prefix + "/", Typeflag: tar.TypeDir, Mode: 0755}, {Name: prefix + "/src/", Typeflag: tar.TypeDir, Mode: 0755}}
	for _, name := range []string{"LICENSE", "package.json", "src/index.js"} {
		raw := contents[prefix+"/"+name]
		headers = append(headers, &tar.Header{Name: prefix + "/" + name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(raw))})
	}
	archive := original(t, headers, contents)
	catalog := upstreamCatalog{1, "pinned-upstream-application-source-inputs", []upstreamRecord{{ID: "fixture", Repository: "Example/fixture", Commit: commit, Archive: originalPin{"fixture-" + commit + ".tar.gz", hash(archive), int64(len(archive))}, InspectPaths: []string{"LICENSE", "package.json", "src/index.js"}, Packages: map[string]string{"fixture": "1.2.3"}}}}
	rawCatalog, _ := contract.JSON(catalog)
	sum := sha512.Sum512(archive)
	lock, _ := json.Marshal(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{"name": "fixture-app"}, "node_modules/fixture": map[string]any{"version": "1.2.3", "resolved": "https://registry.npmjs.org/fixture/-/fixture-1.2.3.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum[:])}}})
	root, sourceCommit := gitFixture(t, map[string][]byte{backendRecipeCatalog: []byte(`{"schema_version":1,"kind":"reviewed-backend-generator-associations","generated_output_reproduction_verified":false}`), catalogPath: rawCatalog, lockPath: lock, "web/package.json": []byte(`{"name":"fixture-app"}`), "backend/go.mod": []byte("module example.invalid/fixture\n\ngo 1.23.0\n"), "backend/go.sum": {}})
	return CommonOptions{Root: root, Output: filepath.Join(t.TempDir(), "collected"), Source: Context{"endorses/psst.zip", "v1.2.3", sourceCommit}}, archive
}
func TestCollectAndVerifyRetainOriginalsAndRejectSubstitution(t *testing.T) {
	o, raw := upstreamFixture(t)
	var calls atomic.Int32
	o.Fetch = func(ctx context.Context, address string, limit int64) ([]byte, error) {
		calls.Add(1)
		if !strings.HasPrefix(address, "https://codeload.github.com/Example/fixture/tar.gz/") && address != "https://registry.npmjs.org/fixture/-/fixture-1.2.3.tgz" {
			t.Fatal("source URL changed")
		}
		return raw, nil
	}
	inventory, err := CollectCommon(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(inventory.Sources) != 2 || inventory.Sources[0].Original.SHA256 != hash(raw) || len(inventory.Sources[0].LicensePaths) != 1 {
		t.Fatal("original source/legal inventory missing")
	}
	o.Collection = o.Output
	verified, err := VerifyCommon(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Source != inventory.Source || len(verified.Artifacts) != 0 {
		t.Fatal("verification changed source identity or introduced self-referential asset facts")
	}
	sourceAsset := filepath.Join(o.Collection, "psst.zip-upstream-inputs-"+o.Source.Version+".tar.gz")
	payload, _ := os.ReadFile(sourceAsset)
	payload[len(payload)/2] ^= 1
	os.WriteFile(sourceAsset, payload, 0600)
	if _, err = VerifyCommon(context.Background(), o); err == nil {
		t.Fatal("changed offering accepted")
	}
}

func TestNpmContentIsFetchedOnceAndEveryLockedPathIsAuthenticated(t *testing.T) {
	_, raw := upstreamFixture(t)
	sum := sha512.Sum512(raw)
	input := map[string]any{"version": "1.2.3", "resolved": "https://registry.npmjs.org/fixture/-/fixture-1.2.3.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum[:]), "dev": true, "optional": true}
	lock, _ := json.Marshal(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{}, "node_modules/fixture": input, "node_modules/parent/node_modules/fixture": input}})
	var calls atomic.Int32
	sources, files, err := collectNpm(context.Background(), lock, func(context.Context, string, int64) ([]byte, error) { calls.Add(1); return raw, nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(files) != 2 || len(sources) != 2 || sources[0].ID == sources[1].ID || !sources[0].Development || !sources[0].Optional {
		t.Fatal("common npm acquisition duplicated or lost the exact locked path mapping")
	}
	corrupt := append([]byte(nil), raw...)
	corrupt[len(corrupt)/2] ^= 1
	if _, _, err = collectNpm(context.Background(), lock, func(context.Context, string, int64) ([]byte, error) { return corrupt, nil }); err == nil {
		t.Fatal("changed npm original passed SRI authentication")
	}
}
func TestOriginalLinkPolicyNeverFollowsLinksAndRejectsHiddenPayload(t *testing.T) {
	prefix := "fixture-" + strings.Repeat("b", 40)
	policy := archivePolicy{Expanded: 1 << 20, Member: 1 << 20, Members: 20, Prefix: prefix, Original: true, Selected: map[string]bool{"LICENSE": true}}
	headers := []*tar.Header{{Name: prefix, Typeflag: tar.TypeDir, Mode: 0755}, {Name: prefix + "/LICENSE", Typeflag: tar.TypeReg, Mode: 0644, Size: 4}, {Name: prefix + "/COPYING", Typeflag: tar.TypeSymlink, Mode: 0777, Linkname: "LICENSE"}}
	raw := original(t, headers, map[string][]byte{prefix + "/LICENSE": []byte("text")})
	members, files, err := inspectArchive(raw, policy)
	if err != nil || len(members) != 3 || string(files["LICENSE"]) != "text" {
		t.Fatalf("valid original link rejected: %v", err)
	}
	headers[2].Linkname = "../../outside"
	if _, _, err = inspectArchive(original(t, headers, map[string][]byte{prefix + "/LICENSE": []byte("text")}), policy); err == nil {
		t.Fatal("escaping source link accepted")
	}
	if _, _, err = inspectArchive(append(raw, raw...), policy); err == nil {
		t.Fatal("concatenated gzip accepted")
	}
	short := policy
	short.Expanded = 32
	if _, _, err = inspectArchive(raw, short); err == nil {
		t.Fatal("source expansion bound ignored")
	}
}
func TestCatalogLockAndPinnedOriginalFailuresPrecedeCollectionOutput(t *testing.T) {
	o, raw := upstreamFixture(t)
	o.Fetch = func(context.Context, string, int64) ([]byte, error) { return append(raw, 0), nil }
	if _, err := CollectCommon(context.Background(), o); err == nil {
		t.Fatal("unmatched original pin accepted")
	}
	if _, err := os.Stat(o.Output); !os.IsNotExist(err) {
		t.Fatal("failed source collection emitted output")
	}
	catalog, err := committed(context.Background(), o.Root, o.Source.Commit, catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	json.Unmarshal(catalog, &value)
	bad, _ := json.Marshal(value)
	if _, err = validateCatalog(context.Background(), bad, []byte(`{"lockfileVersion":3,"packages":{"node_modules/fixture":{"version":"9.9.9"}}}`), []byte("module example.invalid/fixture\n")); err == nil {
		t.Fatal("source catalog accepted another lock")
	}
}
