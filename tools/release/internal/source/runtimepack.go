package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/process"
)

type RuntimeOptions struct {
	Root, Output, Collection, Platform, BackendConfig, WebConfig, ImagesTar, HelperImage, CaddyBase, Cosign, DeliveryBase string
	Source                                                                                                                Context
	Fetch                                                                                                                 Fetch
	Execute                                                                                                               func(context.Context, time.Duration, ...string) ([]byte, error)
}

func (o RuntimeOptions) defaults() RuntimeOptions {
	if o.DeliveryBase == "" {
		o.DeliveryBase = "https://github.com/" + o.Source.Repository + "/releases/download/" + o.Source.Version
	}
	return o
}

func (o RuntimeOptions) checked() error {
	if err := o.Source.Validate(); err != nil {
		return err
	}
	if o.Platform != "linux/amd64" && o.Platform != "linux/arm64" || !digestRE.MatchString(o.BackendConfig) || !digestRE.MatchString(o.WebConfig) || o.BackendConfig == o.WebConfig || !caddyImageRE.MatchString(o.CaddyBase) || o.HelperImage == "" {
		return fmt.Errorf("invalid native runtime source inputs")
	}
	u, err := url.Parse(o.DeliveryBase)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/"+o.Source.Repository+"/releases/download/"+o.Source.Version {
		return fmt.Errorf("runtime source delivery base differs from exact release")
	}
	return nil
}
func (o RuntimeOptions) execute() func(context.Context, time.Duration, ...string) ([]byte, error) {
	if o.Execute != nil {
		return o.Execute
	}
	return (process.Runner{Dir: o.Root, Env: commandEnvironment(), CleanEnv: true}).Output
}
func helperFile(ctx context.Context, execute func(context.Context, time.Duration, ...string) ([]byte, error), config, name string) ([]byte, error) {
	return execute(ctx, 55*time.Second, "docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--entrypoint", "cat", config, name)
}
func CollectRuntime(ctx context.Context, o RuntimeOptions) (Inventory, error) {
	o = o.defaults()
	if err := o.checked(); err != nil {
		return Inventory{}, err
	}
	if _, err := os.Lstat(o.Output); !os.IsNotExist(err) {
		return Inventory{}, fmt.Errorf("runtime source output already exists")
	}
	archive, err := openImageArchive(o.ImagesTar)
	if err != nil {
		return Inventory{}, err
	}
	defer archive.file.Close()
	backend, err := archive.saved(o.BackendConfig)
	if err != nil {
		return Inventory{}, err
	}
	web, err := archive.saved(o.WebConfig)
	if err != nil {
		return Inventory{}, err
	}
	if "linux/"+backend.Architecture != o.Platform || web.Architecture != backend.Architecture {
		return Inventory{}, fmt.Errorf("runtime pair native platforms differ")
	}
	inventory := newInventory(o.Source)
	for _, entry := range []struct {
		component string
		image     SavedImage
	}{{"backend", backend}, {"web", web}} {
		graph, e := runtimeGraph(entry.component, entry.image, o.Source)
		if e != nil {
			return inventory, e
		}
		inventory.Runtime = append(inventory.Runtime, graph)
	}
	execute := o.execute()
	fetch := o.Fetch
	if fetch == nil {
		fetch = publicFetch
	}
	inspected, err := execute(ctx, 55*time.Second, "docker", "image", "inspect", o.HelperImage)
	if err != nil {
		return inventory, err
	}
	var helper []struct{ ID, Architecture, OS string }
	if json.Unmarshal(inspected, &helper) != nil || len(helper) != 1 || !digestRE.MatchString(helper[0].ID) || helper[0].OS != "linux" || helper[0].Architecture != backend.Architecture {
		return inventory, fmt.Errorf("runtime helper actual native image identity differs")
	}
	helperID := helper[0].ID
	release, err := helperFile(ctx, execute, helperID, "/etc/alpine-release")
	if err != nil {
		return inventory, err
	}
	match := regexp.MustCompile(`^([1-9][0-9]*\.[0-9]+)\.[0-9]+\n?$`).FindSubmatch(release)
	if match == nil {
		return inventory, fmt.Errorf("source helper Alpine release is not stable numeric version")
	}
	mirror := "https://distfiles.alpinelinux.org/distfiles/v" + string(match[1])
	helperDatabase, err := helperFile(ctx, execute, helperID, "/lib/apk/db/installed")
	if err != nil {
		return inventory, err
	}
	if _, err = APKPackages(helperDatabase); err != nil {
		return inventory, err
	}
	files := map[string][]byte{"collection/helper-alpine-release.txt": release, "collection/helper-installed-apk-db": helperDatabase}
	for name, raw := range backend.DatabaseFiles {
		files[name] = raw
	}
	for name, raw := range web.DatabaseFiles {
		files[name] = raw
	}
	originalRelease := web.Files["srv/web/licenses/release.json"]
	if err := checkWebRelease(originalRelease, o.Source, false); err != nil {
		return inventory, err
	}
	files["collection/original-web-release.json"] = originalRelease
	webRelease := fact("collection/original-web-release.json", originalRelease)
	for i := range inventory.Runtime {
		if inventory.Runtime[i].Component == "web" {
			inventory.Runtime[i].WebRelease = &webRelease
		}
	}
	inventory.Collectors = []Collector{{Name: "alpine-abuild-originals", Platform: o.Platform, Image: o.HelperImage, Config: helperID, Version: strings.TrimSpace(string(release)), Environment: map[string]string{"DISTFILES_MIRROR": mirror}, Reports: []File{fact("collection/helper-alpine-release.txt", release), fact("collection/helper-installed-apk-db", helperDatabase)}}}
	for _, name := range []string{goRuntimeCatalog, "tools/runtime-sources.Dockerfile"} {
		raw, e := committed(ctx, o.Root, o.Source.Commit, name)
		if e != nil {
			return inventory, e
		}
		files["inputs/"+name] = raw
		inventory.Inputs[name] = fact("inputs/"+name, raw)
	}
	review, err := runtimeLegal(ctx, o.Root, o.Source)
	if err != nil {
		return inventory, err
	}
	for name, raw := range review.files {
		files[name] = raw
	}
	inventory.Inputs[runtimeLegalRoot+"review.json"] = fact("legal-review/review.json", review.files["legal-review/review.json"])
	origins := map[string]APK{}
	for _, graph := range inventory.Runtime {
		for _, p := range graph.Packages {
			origins[apkOrigin(p)] = p
		}
	}
	var originKeys []string
	for key := range origins {
		originKeys = append(originKeys, key)
	}
	sort.Strings(originKeys)
	byID := map[string]Source{}
	for _, key := range originKeys {
		p := origins[key]
		repository, recipe, e := recipeFiles(ctx, p.Origin, p.AportsCommit, fetch)
		if e != nil {
			return inventory, e
		}
		raw, declared, sums, e := collectAPKOriginal(ctx, p, helperID, mirror, recipe, execute)
		if e != nil {
			return inventory, e
		}
		source, retained, e := runtimeOriginSource(ctx, p, raw, repository, recipe, declared, sums)
		if e != nil {
			return inventory, e
		}
		for _, graph := range inventory.Runtime {
			for _, pkg := range graph.Packages {
				if apkOrigin(pkg) == key {
					source.Associations["apk:"+pkg.Name] = pkg.Version
				}
			}
		}
		for name, body := range retained {
			files[name] = body
		}
		inventory.Sources = append(inventory.Sources, source)
		byID[source.ID] = source
	}
	caddy, caddyFiles, signatures, err := collectCaddy(ctx, web, o.CaddyBase, o.Cosign, fetch, execute)
	if err != nil {
		return inventory, err
	}
	inventory.Signatures = signatures
	inventory.Sources = append(inventory.Sources, caddy)
	byID[caddy.ID] = caddy
	for name, raw := range caddyFiles {
		files[name] = raw
	}
	compilers := map[string]bool{}
	for _, graph := range inventory.Runtime {
		compilers[graph.GoVersion] = true
	}
	if !compilers[caddy.Authentication["go_version"]] {
		return inventory, fmt.Errorf("Caddy source/compiler mapping differs from image executable")
	}
	var versions []string
	for version := range compilers {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	for _, version := range versions {
		source, retained, e := collectGoRuntime(ctx, files["inputs/"+goRuntimeCatalog], version, fetch)
		if e != nil {
			return inventory, e
		}
		inventory.Sources = append(inventory.Sources, source)
		byID[source.ID] = source
		for name, raw := range retained {
			files[name] = raw
		}
	}
	for i := range inventory.Runtime {
		graph := &inventory.Runtime[i]
		seen := map[string]bool{}
		for _, p := range graph.Packages {
			id := "apk-" + strings.TrimPrefix(hash([]byte(apkOrigin(p))), "sha256:")
			seen[id] = true
		}
		seen["go-runtime-"+graph.GoVersion] = true
		if graph.Component == "web" {
			seen[caddy.ID] = true
		}
		for id := range seen {
			graph.SourceIDs = append(graph.SourceIDs, id)
		}
		sort.Strings(graph.SourceIDs)
		notices, e := runtimeNotices(*graph, byID, files, review)
		if e != nil {
			return inventory, e
		}
		var full bytes.Buffer
		full.Write(notices)
		full.WriteString("\nGo runtime\n")
		appendSourceNotices(&full, byID["go-runtime-"+graph.GoVersion], files)
		if graph.Component == "web" {
			full.WriteString("\nCaddy, vendored modules and runtime source inputs\n")
			appendSourceNotices(&full, caddy, files)
		}
		noticeName := "overlays/" + graph.Component + "/runtime/THIRD_PARTY_NOTICES.txt"
		files[noticeName] = full.Bytes()
		graph.Notices = fact(noticeName, full.Bytes())
	}
	assetName := runtimeAssetName(o.Source.Version, o.Platform)
	files["SOURCE.md"] = runtimeSourceNote(o, assetName)
	rawInventory, err := encode(inventory)
	if err != nil {
		return inventory, err
	}
	files["source-inventory.json"] = rawInventory
	payload, err := offering(files)
	if err != nil {
		return inventory, err
	}
	if err = os.Mkdir(o.Output, 0700); err != nil {
		return inventory, err
	}
	if err = writeNew(filepath.Join(o.Output, assetName), payload); err != nil {
		return inventory, err
	}
	if err = writeNew(filepath.Join(o.Output, "source-inventory.json"), rawInventory); err != nil {
		return inventory, err
	}
	return inventory, nil
}
func runtimeAssetName(version, platform string) string {
	return "psst.zip-" + version + "-runtime-sources-" + strings.TrimPrefix(platform, "linux/") + ".tar.gz"
}
func runtimeSourceNote(o RuntimeOptions, asset string) []byte {
	return []byte("# psst.zip runtime corresponding sources\n\nRelease: " + o.Source.Version + "\nApplication revision: " + o.Source.Commit + "\nPlatform: " + o.Platform + "\nRuntime source asset: " + o.DeliveryBase + "/" + asset + "\n\nsource-inventory.json records every exact original source, recipe, notice, compiler version and retained image-layer package origin. Common origins shared by the backend and web images are retained once and explicitly associated with both.\n\nFor Alpine, each original srcpkg retains the exact APKBUILD, patches/helpers and checksum-authenticated upstream distfiles. Rebuild in an isolated environment using the retained matching Alpine recipe and helper toolchain. The helper is build tooling; its binaries are not distributed. Original APK binary envelopes are not retained, so source provenance is not APK binary-signature verification.\n\nFor Caddy, the original buildable artifact retains main.go, go.mod, go.sum and its full vendor tree. Exact source recipe/distribution trees, official binary input, legacy Sigstore proofs and signed-checksum bindings are retained. The compiler version of each executable maps to its full pinned Go runtime/compiler original. Extract the matching tree and follow src/make.bash with a compatible bootstrap toolchain; if VERSION is absent, write the recorded exact Go version plus a newline there before rebuilding.\n\nOriginal copyright notices and license terms remain complete. Metadata/key origins preserve their upstream MIT declaration and full terms without assigning an invented copyright owner. The two hash-bound malformed BusyBox unzip fixtures are retained as original test data. Source relationships identify retained inputs; they do not claim byte-for-byte executable reproduction.\n")
}

// RuntimeFiles reads a self-contained offering and checks its external canonical
// inventory is the exact inventory retained inside it. The final release manifest
// supplies the archive digest, avoiding an impossible inventory self-reference.
func RuntimeFiles(collection string, source Context, platform string) (Inventory, map[string][]byte, error) {
	raw, err := readRegular(filepath.Join(collection, "source-inventory.json"), MetadataLimit)
	if err != nil {
		return Inventory{}, nil, err
	}
	inventory, err := Decode(raw)
	if err != nil {
		return inventory, nil, err
	}
	if inventory.Source != source || len(inventory.Runtime) != 2 || len(inventory.Artifacts) != 0 {
		return inventory, nil, fmt.Errorf("runtime inventory scope differs")
	}
	payload, err := readRegular(filepath.Join(collection, runtimeAssetName(source.Version, platform)), OfferingLimit)
	if err != nil {
		return inventory, nil, err
	}
	_, files, err := inspectArchive(payload, archivePolicy{Expanded: OfferingLimit, Member: OriginalLimit, Members: 100000, Capture: func(string) bool { return true }})
	if err != nil {
		return inventory, nil, err
	}
	if !bytes.Equal(files["source-inventory.json"], raw) {
		return inventory, nil, fmt.Errorf("runtime source inventory differs from retained offering")
	}
	return inventory, files, nil
}
