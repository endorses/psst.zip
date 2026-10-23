package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// ReplayRuntimeRetained verifies the self-contained source offering without
// repeating acquisition or cryptographic network calls. Its caller must
// authenticate the native producer's inventory/result before crossing jobs.
// Retained verifier logs are diagnostics; they are never treated as independent
// signature proof by this method.
func ReplayRuntimeRetained(ctx context.Context, o RuntimeOptions) (Inventory, map[string][]byte, error) {
	o = o.defaults()
	inventory, files, err := RuntimeFiles(o.Collection, o.Source, o.Platform)
	if err != nil {
		return inventory, nil, err
	}
	expected := map[string][]byte{"source-inventory.json": files["source-inventory.json"], "SOURCE.md": runtimeSourceNote(o, runtimeAssetName(o.Source.Version, o.Platform))}
	if len(inventory.Images) != 0 || len(inventory.Collectors) != 1 || len(inventory.Inputs) != 3 {
		return inventory, nil, fmt.Errorf("unexpected runtime source inventory scope")
	}
	for _, name := range []string{goRuntimeCatalog, "tools/runtime-sources.Dockerfile"} {
		raw, e := committed(ctx, o.Root, o.Source.Commit, name)
		if e != nil {
			return inventory, nil, e
		}
		target := "inputs/" + name
		if inventory.Inputs[name] != fact(target, raw) {
			return inventory, nil, fmt.Errorf("runtime source policy/recipe differs from exact commit")
		}
		expected[target] = raw
	}
	review, err := runtimeLegal(ctx, o.Root, o.Source)
	if err != nil {
		return inventory, nil, err
	}
	for name, raw := range review.files {
		expected[name] = raw
	}
	if inventory.Inputs[runtimeLegalRoot+"review.json"] != fact("legal-review/review.json", review.files["legal-review/review.json"]) {
		return inventory, nil, fmt.Errorf("runtime legal review input differs")
	}
	collector := inventory.Collectors[0]
	release := files["collection/helper-alpine-release.txt"]
	match := regexp.MustCompile(`^([1-9][0-9]*\.[0-9]+)\.[0-9]+\n?$`).FindSubmatch(release)
	if match == nil || collector.Name != "alpine-abuild-originals" || collector.Platform != o.Platform || collector.Version != strings.TrimSpace(string(release)) || len(collector.Environment) != 1 || collector.Environment["DISTFILES_MIRROR"] != "https://distfiles.alpinelinux.org/distfiles/v"+string(match[1]) || len(collector.Reports) != 2 {
		return inventory, nil, fmt.Errorf("runtime helper identity differs from retained inputs")
	}
	for _, name := range []string{"helper-alpine-release.txt", "helper-installed-apk-db"} {
		target := "collection/" + name
		expected[target] = files[target]
	}
	if _, err = APKPackages(files["collection/helper-installed-apk-db"]); err != nil {
		return inventory, nil, err
	}
	for _, report := range collector.Reports {
		if report != fact(report.Path, expected[report.Path]) {
			return inventory, nil, fmt.Errorf("runtime helper raw observation differs")
		}
	}
	byID := map[string]Source{}
	var caddy Source
	origins := map[string][]APK{}
	versions := map[string]bool{}
	components := map[string]bool{}
	for _, graph := range inventory.Runtime {
		if components[graph.Component] || (graph.Component != "backend" && graph.Component != "web") || graph.Platform != o.Platform || !digestRE.MatchString(graph.Config) || !goVersionRE.MatchString(graph.GoVersion) || len(graph.RootFS) == 0 || len(graph.RootFS) > 128 || !digestRE.MatchString(graph.RuntimeConfigSHA256) || !digestRE.MatchString(graph.Binary.SHA256) || len(graph.Layers) == 0 {
			return inventory, nil, fmt.Errorf("runtime native graph identity differs")
		}
		components[graph.Component] = true
		if graph.Component == "web" {
			name := "collection/original-web-release.json"
			if graph.WebRelease == nil || *graph.WebRelease != fact(name, files[name]) {
				return inventory, nil, fmt.Errorf("original hosted release metadata is missing or changed")
			}
			if err := checkWebRelease(files[name], o.Source, false); err != nil {
				return inventory, nil, err
			}
			expected[name] = files[name]
		} else if graph.WebRelease != nil {
			return inventory, nil, fmt.Errorf("unexpected backend browser metadata")
		}
		versions[graph.GoVersion] = true
		binary := "app/server"
		if graph.Component == "web" {
			binary = "usr/bin/caddy"
		}
		if graph.Binary.Path != binary {
			return inventory, nil, fmt.Errorf("runtime executable path differs")
		}
		for _, diff := range graph.RootFS {
			if !digestRE.MatchString(diff) {
				return inventory, nil, fmt.Errorf("runtime native diff ID missing")
			}
		}
		retained := map[string]APK{}
		var final []APK
		last := -1
		for _, database := range graph.Layers {
			body := files[database.File.Path]
			if database.Layer <= last || database.Layer >= len(graph.RootFS) || !digestRE.MatchString("sha256:"+database.LayerSHA256) || fact(database.File.Path, body) != database.File || hash(body) != "sha256:"+database.DatabaseSHA256 {
				return inventory, nil, fmt.Errorf("runtime retained APK layer/database bytes differ")
			}
			last = database.Layer
			expected[database.File.Path] = body
			packages, e := APKPackages(body)
			if e != nil {
				return inventory, nil, e
			}
			final = packages
			for _, p := range packages {
				retained[apkKey(p)] = p
			}
		}
		for _, p := range final {
			p.Present = true
			retained[apkKey(p)] = p
		}
		var packages []APK
		for _, p := range retained {
			packages = append(packages, p)
		}
		sort.Slice(packages, func(i, j int) bool { return apkKey(packages[i]) < apkKey(packages[j]) })
		actual, _ := contract.JSON(graph.Packages)
		wanted, _ := contract.JSON(packages)
		if !bytes.Equal(actual, wanted) {
			return inventory, nil, fmt.Errorf("runtime retained/final package graph differs from actual original databases")
		}
		for _, p := range packages {
			origins[apkOrigin(p)] = append(origins[apkOrigin(p)], p)
		}
	}
	for _, item := range inventory.Sources {
		byID[item.ID] = item
		if item.Ecosystem == "caddy" {
			if caddy.ID != "" {
				return inventory, nil, fmt.Errorf("duplicate Caddy source")
			}
			caddy = item
		}
	}
	if len(inventory.Sources) != len(origins)+len(versions)+1 || caddy.ID == "" {
		return inventory, nil, fmt.Errorf("runtime source original coverage differs")
	}
	for key, packages := range origins {
		p := packages[0]
		id := "apk-" + strings.TrimPrefix(hash([]byte(key)), "sha256:")
		declared := byID[id]
		if declared.Recipe == nil || declared.Recipe.Commit != p.AportsCommit {
			return inventory, nil, fmt.Errorf("runtime origin exact recipe missing")
		}
		recipe := map[string][]byte{}
		prefix := "recipes/" + id + "/"
		for _, f := range declared.Recipe.Files {
			if !strings.HasPrefix(f.Path, prefix) || fact(f.Path, files[f.Path]) != f {
				return inventory, nil, fmt.Errorf("runtime recipe file differs")
			}
			name := strings.TrimPrefix(f.Path, prefix)
			if !safePath(name) {
				return inventory, nil, fmt.Errorf("runtime recipe path differs")
			}
			recipe[name] = files[f.Path]
		}
		raw := files[declared.Original.Path]
		if err = verifySourcePackage(raw, declared.Recipe.SourceSHA512, recipe); err != nil {
			return inventory, nil, err
		}
		replayed, retained, e := runtimeOriginSource(ctx, p, raw, declared.Recipe.Repository, recipe, declared.Recipe.DeclaredPackages, declared.Recipe.SourceSHA512)
		if e != nil {
			return inventory, nil, e
		}
		for _, p := range packages {
			replayed.Associations["apk:"+p.Name] = p.Version
		}
		got, _ := contract.JSON(declared)
		want, _ := contract.JSON(replayed)
		if !bytes.Equal(got, want) {
			return inventory, nil, fmt.Errorf("runtime source inventory differs from retained original/recipe/legal bytes")
		}
		for name, body := range retained {
			expected[name] = body
		}
	}
	for version := range versions {
		declared := byID["go-runtime-"+version]
		pin, e := goRuntimeSourcePin(expected["inputs/"+goRuntimeCatalog], version)
		if e != nil {
			return inventory, nil, e
		}
		replayed, retained, e := inspectGoRuntime(files[declared.Original.Path], pin)
		if e != nil {
			return inventory, nil, e
		}
		for _, name := range []string{"tag-ref.json", "tag-object.json"} {
			target := "go-runtime/" + version + "/" + name
			if body, ok := files[target]; ok {
				replayed.Auxiliary = append(replayed.Auxiliary, fact(target, body))
				retained[target] = body
			}
		}
		commit, e := retainedTagCommit(retained["go-runtime/"+version+"/tag-ref.json"], retained["go-runtime/"+version+"/tag-object.json"])
		if e != nil || commit != pin.Commit {
			return inventory, nil, fmt.Errorf("retained compiler tag/pinned source commit differs")
		}
		sort.Slice(replayed.Auxiliary, func(i, j int) bool { return replayed.Auxiliary[i].Path < replayed.Auxiliary[j].Path })
		got, _ := contract.JSON(declared)
		want, _ := contract.JSON(replayed)
		if !bytes.Equal(got, want) {
			return inventory, nil, fmt.Errorf("full Go source inventory differs from exact retained originals")
		}
		for name, body := range retained {
			expected[name] = body
		}
	}
	caddyExpected, retained, e := replayCaddy(ctx, o, caddy, inventory.Signatures, files, inventory.Runtime)
	if e != nil {
		return inventory, nil, e
	}
	got, _ := contract.JSON(caddy)
	want, _ := contract.JSON(caddyExpected)
	if !bytes.Equal(got, want) {
		return inventory, nil, fmt.Errorf("Caddy source inventory differs from retained signed originals")
	}
	for name, body := range retained {
		expected[name] = body
	}
	for _, graph := range inventory.Runtime {
		ids := map[string]bool{"go-runtime-" + graph.GoVersion: true}
		for _, p := range graph.Packages {
			ids["apk-"+strings.TrimPrefix(hash([]byte(apkOrigin(p))), "sha256:")] = true
		}
		if graph.Component == "web" {
			ids[caddy.ID] = true
		}
		if len(ids) != len(graph.SourceIDs) {
			return inventory, nil, fmt.Errorf("runtime component/source association coverage differs")
		}
		for _, id := range graph.SourceIDs {
			if !ids[id] {
				return inventory, nil, fmt.Errorf("runtime component refers to unrelated source")
			}
			delete(ids, id)
		}
		notices, e := runtimeNotices(graph, byID, files, review)
		if e != nil {
			return inventory, nil, e
		}
		var full bytes.Buffer
		full.Write(notices)
		full.WriteString("\nGo runtime\n")
		appendSourceNotices(&full, byID["go-runtime-"+graph.GoVersion], files)
		if graph.Component == "web" {
			full.WriteString("\nCaddy, vendored modules and runtime source inputs\n")
			appendSourceNotices(&full, caddy, files)
		}
		name := "overlays/" + graph.Component + "/runtime/THIRD_PARTY_NOTICES.txt"
		expected[name] = full.Bytes()
		if graph.Notices != fact(name, full.Bytes()) {
			return inventory, nil, fmt.Errorf("runtime notice aggregate differs from exact original sources")
		}
	}
	if len(expected) != len(files) {
		return inventory, nil, fmt.Errorf("runtime offering has missing or unexpected payload")
	}
	for name, body := range expected {
		if !bytes.Equal(files[name], body) {
			return inventory, nil, fmt.Errorf("runtime source offering differs from substantive retained-byte replay")
		}
	}
	return inventory, files, nil
}
func retainedTagCommit(ref, tag []byte) (string, error) {
	var value struct{ Object struct{ SHA, Type string } }
	if _, err := jsonObject(ref); err != nil {
		return "", err
	}
	if json.Unmarshal(ref, &value) != nil || !commitRE.MatchString(value.Object.SHA) {
		return "", fmt.Errorf("invalid retained tag ref")
	}
	if value.Object.Type == "tag" {
		if _, err := jsonObject(tag); err != nil {
			return "", err
		}
		if json.Unmarshal(tag, &value) != nil {
			return "", fmt.Errorf("invalid retained tag object")
		}
	} else if len(tag) != 0 {
		return "", fmt.Errorf("unexpected retained tag object")
	}
	if value.Object.Type != "commit" || !commitRE.MatchString(value.Object.SHA) {
		return "", fmt.Errorf("retained tag does not identify a source commit")
	}
	return value.Object.SHA, nil
}
func replayCaddy(ctx context.Context, o RuntimeOptions, source Source, signatures []SignatureVerification, files map[string][]byte, graphs []RuntimeGraph) (Source, map[string][]byte, error) {
	metadata := source.Authentication
	version := source.Version
	arch := strings.TrimPrefix(o.Platform, "linux/")
	binaryName := "caddy_" + strings.TrimPrefix(version, "v") + "_linux_" + arch + ".tar.gz"
	selected, err := originalTarFiles(files["caddy/"+binaryName], func(name string) bool { return name == "caddy" || strings.HasSuffix(name, "/caddy") })
	if err != nil {
		return Source{}, nil, err
	}
	binary, err := soleSuffix(selected, "caddy", false)
	if err != nil {
		return Source{}, nil, err
	}
	for _, graph := range graphs {
		if graph.Component == "web" && (graph.Binary.SHA256 != hash(binary) || graph.Binary.Size != int64(len(binary)) || graph.GoVersion != metadata["go_version"]) {
			return Source{}, nil, fmt.Errorf("retained signed Caddy executable differs from native runtime graph")
		}
	}
	routes := map[string][]byte{"https://raw.githubusercontent.com/caddyserver/caddy-docker/" + metadata["recipe_commit"] + "/" + metadata["recipe_directory"] + "/Dockerfile": files["caddy/evidence/Dockerfile"], "https://api.github.com/repos/caddyserver/caddy/releases/tags/" + version: files["caddy/evidence/release.json"], "https://raw.githubusercontent.com/caddyserver/caddy/" + metadata["source_commit"] + "/cmd/caddy/main.go": files["caddy/evidence/main.go"]}
	for name, body := range files {
		if strings.HasPrefix(name, "caddy/") && path.Base(name) == strings.TrimPrefix(name, "caddy/") {
			base := path.Base(name)
			routes["https://github.com/caddyserver/caddy/releases/download/"+version+"/"+base] = body
		}
	}
	for _, repo := range []struct{ name, commit string }{{"caddy-docker", metadata["recipe_commit"]}, {"dist", metadata["distribution_commit"]}} {
		routes["https://codeload.github.com/caddyserver/"+repo.name+"/tar.gz/"+repo.commit] = files["caddy/"+repo.name+"-"+repo.commit+".tar.gz"]
	}
	fetch := func(ctx context.Context, address string, limit int64) ([]byte, error) {
		body, ok := routes[address]
		if !ok || len(body) == 0 || int64(len(body)) > limit {
			return nil, fmt.Errorf("missing exact retained Caddy acquisition input")
		}
		return body, nil
	}
	execute := func(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
		if len(args) != 7 || strings.Join(args[:4], " ") != "docker buildx imagetools inspect" || args[4] != metadata["base_index"] {
			return nil, fmt.Errorf("unexpected operation during offline Caddy replay")
		}
		return files["caddy/evidence/index.json"], nil
	}
	verifier := func(ctx context.Context, cosign, v, revision, architecture string, retained map[string][]byte) ([]SignatureVerification, map[string][]byte, error) {
		if v != version || architecture != arch || len(signatures) != 2 {
			return nil, nil, fmt.Errorf("retained Caddy native signature coverage differs")
		}
		bindings, err := caddyChecksumBindings(version, arch, retained)
		if err != nil {
			return nil, nil, err
		}
		archiveName := "caddy_" + strings.TrimPrefix(version, "v") + "_buildable-artifact.tar.gz"
		checksumsName := "caddy_" + strings.TrimPrefix(version, "v") + "_checksums.txt"
		required := map[string]bool{archiveName: true, checksumsName: true}
		logs := map[string][]byte{}
		for _, proof := range signatures {
			name := strings.TrimPrefix(proof.Artifact.Path, "caddy/")
			if !required[name] || proof.Certificate.Path != "caddy/"+strings.TrimSuffix(name, ".tar.gz")+".pem" || proof.Signature.Path != "caddy/"+name+".sig" || proof.Log.Path != "caddy/evidence/"+name+".verification.json" {
				return nil, nil, fmt.Errorf("retained native Caddy signature subject coverage differs")
			}
			delete(required, name)
			if proof.WorkflowCommit != revision || proof.Issuer != "https://token.actions.githubusercontent.com" || proof.Signer != "https://github.com/caddyserver/caddy/.github/workflows/release.yml@refs/tags/"+version || proof.VerifierVersion != caddyCosignVersion || proof.Platform != o.Platform || proof.Verifier.SHA256 != caddyCosignHashes[o.Platform] {
				return nil, nil, fmt.Errorf("retained Caddy producer verifier/signer identity differs")
			}
			for _, f := range []File{proof.Artifact, proof.Certificate, proof.Signature, proof.Log} {
				if fact(f.Path, files[f.Path]) != f {
					return nil, nil, fmt.Errorf("retained Caddy signature inputs/log changed")
				}
			}
			var log struct {
				Stdout, Stderr string
				ExitCode       int `json:"exit_code"`
			}
			object, err := jsonObject(files[proof.Log.Path])
			if err != nil || len(object) != 3 || object["stdout"] == nil || object["stderr"] == nil || object["exit_code"] == nil || contract.Decode(files[proof.Log.Path], MetadataLimit, &log) != nil || log.ExitCode != 0 {
				return nil, nil, fmt.Errorf("retained native signature execution did not complete")
			}
			logs[proof.Log.Path] = files[proof.Log.Path]
			actual, _ := contract.JSON(proof.SHA512Bindings)
			expected, _ := contract.JSON(bindings)
			if !bytes.Equal(actual, expected) {
				return nil, nil, fmt.Errorf("retained signed Caddy checksum binding differs")
			}

		}
		return signatures, logs, nil
	}
	item, retained, _, err := retainedCaddy(ctx, SavedImage{Architecture: arch, Files: map[string][]byte{"usr/bin/caddy": binary}}, metadata["base_index"], "", fetch, execute, verifier)
	return item, retained, err
}
