package source

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sourcePackageFixture(t *testing.T) ([]byte, map[string][]byte, map[string]string) {
	t.Helper()
	upstream := original(t, []*tar.Header{{Name: "fixture/LICENSE", Typeflag: tar.TypeReg, Mode: 0644, Size: 5}}, map[string][]byte{"fixture/LICENSE": []byte("terms")})
	sum := sha512.Sum512(upstream)
	recipe := map[string][]byte{"APKBUILD": []byte("pkgname=fixture\npkgver=1.0\npkgrel=0\nlicense='MIT'\n")}
	contents := map[string][]byte{"recipe/APKBUILD": recipe["APKBUILD"], "recipe/fixture-1.0.tar.gz": upstream}
	var headers []*tar.Header
	for _, name := range []string{"recipe/APKBUILD", "recipe/fixture-1.0.tar.gz"} {
		headers = append(headers, &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(contents[name]))})
	}
	return original(t, headers, contents), recipe, map[string]string{"fixture-1.0.tar.gz": hex.EncodeToString(sum[:])}
}
func TestRuntimeOriginalChecksumsAndLegalTextsComeFromRetainedSources(t *testing.T) {
	raw, recipe, sums := sourcePackageFixture(t)
	if err := verifySourcePackage(raw, sums, recipe); err != nil {
		t.Fatal(err)
	}
	p := APK{Name: "fixture", Version: "1.0-r0", Architecture: "x86_64", License: "MIT", Origin: "fixture", AportsCommit: strings.Repeat("b", 40), Checksum: "fixture"}
	source, files, err := runtimeOriginSource(context.Background(), p, raw, "main", recipe, []string{"fixture"}, sums)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.NoticeFiles) != 1 || len(source.Recipe.Files) != 1 || len(source.BuildInputPaths) != 1 {
		t.Fatal("actual runtime legal or recipe originals lost")
	}
	for _, notice := range source.NoticeFiles {
		if !bytes.Equal(files[notice.Path], []byte("terms")) {
			t.Fatal("legal text was not retained unchanged")
		}
	}
	wrong := map[string]string{}
	for name := range sums {
		wrong[name] = strings.Repeat("0", 128)
	}
	if err = verifySourcePackage(raw, wrong, recipe); err == nil {
		t.Fatal("source package accepted against substituted APKBUILD checksum")
	}
	altered := map[string][]byte{"APKBUILD": []byte("changed recipe")}
	if err = verifySourcePackage(raw, sums, altered); err == nil {
		t.Fatal("source recipe differed from original srcpkg")
	}
}
func TestRecipeAcquisitionBindsEveryHelperToExactGitBlob(t *testing.T) {
	commit := strings.Repeat("b", 40)
	recipe := []byte("pkgname=fixture\n")
	listing, _ := json.Marshal([]map[string]any{{"name": "APKBUILD", "path": "main/fixture/APKBUILD", "type": "file", "sha": gitBlob(recipe)}})
	fetch := func(ctx context.Context, address string, limit int64) ([]byte, error) {
		if strings.HasPrefix(address, "https://api.github.com/repos/alpinelinux/aports/contents/") {
			if !strings.HasSuffix(address, "?ref="+commit) {
				t.Fatal("recipe route did not pin commit")
			}
			return listing, nil
		}
		return recipe, nil
	}
	repository, files, err := recipeFiles(context.Background(), "fixture", commit, fetch)
	if err != nil || repository != "main" || !bytes.Equal(files["APKBUILD"], recipe) {
		t.Fatalf("exact recipe acquisition failed: %v", err)
	}
	if _, _, err = recipeFiles(context.Background(), "fixture", commit, func(ctx context.Context, address string, limit int64) ([]byte, error) {
		if strings.Contains(address, "/contents/") {
			return listing, nil
		}
		return []byte("substituted"), nil
	}); err == nil {
		t.Fatal("modified raw recipe passed exact Git tree object binding")
	}
}
func TestGoRuntimeRequiresCompletePinnedTreeAndMatchingVersion(t *testing.T) {
	commit := strings.Repeat("b", 40)
	prefix := "go-" + commit
	contents := map[string][]byte{}
	var headers []*tar.Header
	for _, name := range []string{"LICENSE", "PATENTS", "VERSION", "src/go.mod", "src/runtime/proc.go", "src/cmd/compile/main.go", "src/make.bash"} {
		body := []byte("retained source\n")
		if name == "VERSION" {
			body = []byte("go1.27.3\n")
		}
		contents[prefix+"/"+name] = body
		headers = append(headers, &tar.Header{Name: prefix + "/" + name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(body))})
	}
	raw := original(t, headers, contents)
	pin := goRuntimePin{Version: "go1.27.3", Commit: commit, File: prefix + ".tar.gz", URL: "https://codeload.github.com/golang/go/tar.gz/" + commit, SHA256: strings.TrimPrefix(hash(raw), "sha256:"), Size: int64(len(raw))}
	source, files, err := inspectGoRuntime(raw, pin)
	if err != nil || len(source.NoticeFiles) != 2 || len(source.ObservedInputs) != 7 || files[source.Original.Path] == nil {
		t.Fatalf("complete Go source/notice mapping failed: %v", err)
	}
	pin.Version = "go1.27.2"
	if _, _, err = inspectGoRuntime(raw, pin); err == nil {
		t.Fatal("full tree VERSION was not bound to actual compiler")
	}
}
func TestCommittedRuntimeLegalCatalogStillMatchesAllOriginalEvidence(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	raw, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	review, err := runtimeLegal(context.Background(), root, Context{"endorses/psst.zip", "v1.2.3", strings.TrimSpace(string(raw))})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.documents) < 20 || len(review.origins) < 4 {
		t.Fatal("committed runtime legal coverage unexpectedly narrowed")
	}
	ids, err := runtimeLicenseIDs("MPL-2.0 AND (MIT OR GPL-2.0-only)", review.standards)
	if err != nil || len(ids) != 3 {
		t.Fatalf("reviewed license expression failed: %v", err)
	}
	if _, err = runtimeLicenseIDs("MIT OR invented-license", review.standards); err == nil {
		t.Fatal("unreviewed runtime terms accepted")
	}
}

func TestFinalRuntimeOverlayKeepsInheritedBehaviorAndRejectsUnrelatedPaths(t *testing.T) {
	config := []byte(`{"User":"10001","Env":["LISTEN_ADDR=:8080"]}`)
	canonical, err := canonicalConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	database := apkFixture("1.0-r0")
	packages, _ := APKPackages(database)
	packages[0].Present = true
	graph := RuntimeGraph{Component: "backend", Platform: "linux/amd64", Config: "sha256:" + strings.Repeat("a", 64), RootFS: []string{"sha256:" + strings.Repeat("b", 64)}, RuntimeConfigSHA256: hash(canonical), Binary: fact("app/server", []byte("original executable")), Packages: packages}
	expected := map[string][]byte{"app/licenses/runtime/THIRD_PARTY_NOTICES.txt": []byte("original legal text")}
	image := SavedImage{Architecture: "amd64", RootFS: append(append([]string{}, graph.RootFS...), "sha256:"+strings.Repeat("c", 64)), RuntimeConfig: []byte(`{"User":"10001","Env":["LISTEN_ADDR=:8080"],"AttachStdin":false,"Cmd":null}`), Files: map[string][]byte{"app/server": []byte("original executable"), "app/licenses/runtime/THIRD_PARTY_NOTICES.txt": []byte("original legal text")}, Packages: packages, LayerEntries: [][]LayerEntry{{}, {{Path: "app/licenses/runtime", Type: tar.TypeDir}, {Path: "app/licenses/runtime/THIRD_PARTY_NOTICES.txt", Type: tar.TypeReg}}}}
	if err = CheckRuntimeImage(graph, image, expected); err != nil {
		t.Fatal(err)
	}
	image.LayerEntries[1] = append(image.LayerEntries[1], LayerEntry{Path: "etc/unrelated.conf", Type: tar.TypeReg})
	if err = CheckRuntimeImage(graph, image, expected); err == nil {
		t.Fatal("unrelated overlay filesystem mutation accepted")
	}
	image.LayerEntries[1] = image.LayerEntries[1][:2]
	image.RuntimeConfig = []byte(`{"User":"root","Env":["LISTEN_ADDR=:8080"]}`)
	if err = CheckRuntimeImage(graph, image, expected); err == nil {
		t.Fatal("runtime user changed without rejection")
	}
}
