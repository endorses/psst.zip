package source

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

var goImageRE = regexp.MustCompile(`^docker.io/library/golang@sha256:[a-f0-9]{64}$`)
var goVersionRE = regexp.MustCompile(`^go1\.[0-9]+\.[0-9]+$`)
var publicGoEnvironment = map[string]string{"GOTOOLCHAIN": "local", "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org", "GOPRIVATE": "", "GONOPROXY": "", "GONOSUMDB": "", "GOFLAGS": "-mod=readonly"}
var goReportNames = []string{"go-downloads.json", "go-modules.json", "go-environment.json", "go-version.txt"}

const goCollectScript = `set -eu
export PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin
export GOTOOLCHAIN=local GOPATH=/tmp/go GOMODCACHE=/reports/cache GOCACHE=/tmp/build
export GOWORK=off GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org
export GOPRIVATE= GONOPROXY= GONOSUMDB= GOFLAGS=-mod=readonly
mkdir /reports/work
cp /source/go.mod /source/go.sum /reports/work/
cd /reports/work
go version > /reports/go-version.txt
go env -json GOTOOLCHAIN GOPROXY GOSUMDB GOPRIVATE GONOPROXY GONOSUMDB GOFLAGS > /reports/go-environment.json
go mod download -json all > /reports/go-downloads.json
go list -m -json all > /reports/go-modules.json
cmp /source/go.mod go.mod
cp go.sum /reports/authenticated-go.sum
`

type ModuleOptions struct {
	Root, Output, Collection, Platform, GoImage string
	Source                                      Context
	// Execute is a seam for a real command recorder in tests, never a replacement
	// for module checksum verification. Production uses the bounded clean runner.
	Execute func(context.Context, time.Duration, ...string) ([]byte, error)
}
type moduleRecord struct {
	Path, Version, Zip, GoMod, Info, Sum, GoModSum string
	Main                                           bool
}

func commandEnvironment() map[string]string {
	result := map[string]string{}
	for _, name := range []string{"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "XDG_RUNTIME_DIR"} {
		if v, ok := os.LookupEnv(name); ok {
			result[name] = v
		}
	}
	return result
}
func moduleExecute(o ModuleOptions) func(context.Context, time.Duration, ...string) ([]byte, error) {
	if o.Execute != nil {
		return o.Execute
	}
	runner := process.Runner{Dir: o.Root, Env: commandEnvironment(), CleanEnv: true}
	return runner.Output
}
func streamModules(raw []byte) ([]moduleRecord, error) {
	if len(raw) == 0 || int64(len(raw)) > MetadataLimit {
		return nil, fmt.Errorf("invalid module metadata size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var result []moduleRecord
	for {
		var value json.RawMessage
		if err := decoder.Decode(&value); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("invalid module metadata stream")
		}
		if len(result) >= 4096 {
			return nil, fmt.Errorf("module metadata exceeds limit")
		}
		object, err := jsonObject(value)
		if err != nil {
			return nil, err
		}
		if len(object["Error"]) != 0 && string(object["Error"]) != "null" || len(object["Replace"]) != 0 && string(object["Replace"]) != "null" {
			return nil, fmt.Errorf("failed or replaced module input")
		}
		var r moduleRecord
		if err = json.Unmarshal(value, &r); err != nil || r.Path == "" || !safePath(r.Path) || (!r.Main && (r.Version == "" || strings.ContainsAny(r.Version, "/\\\r\n\t "))) {
			return nil, fmt.Errorf("invalid selected module identity")
		}
		result = append(result, r)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("empty module graph")
	}
	return result, nil
}
func moduleSums(raw []byte) (map[string]string, error) {
	if int64(len(raw)) > MetadataLimit {
		return nil, fmt.Errorf("module sums exceed limit")
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] == "" || !strings.HasPrefix(fields[2], "h1:") {
			return nil, fmt.Errorf("invalid module checksum")
		}
		sum, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(fields[2], "h1:"))
		if err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("invalid module H1 checksum")
		}
		key := fields[0] + "@" + fields[1]
		if prior, ok := result[key]; ok && prior != fields[2] {
			return nil, fmt.Errorf("conflicting module checksum")
		}
		result[key] = fields[2]
	}
	return result, nil
}
func authenticatedSums(committed, authenticated []byte) (map[string]string, error) {
	before, err := moduleSums(committed)
	if err != nil {
		return nil, err
	}
	after, err := moduleSums(authenticated)
	if err != nil {
		return nil, err
	}
	for key, v := range before {
		if after[key] != v {
			return nil, fmt.Errorf("authenticated module sums removed or changed committed input")
		}
	}
	return after, nil
}
func h1(files map[string]string) string {
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		fmt.Fprintf(h, "%s  %s\n", files[name], name)
	}
	return "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil))
}
func goModuleID(module, version string) string {
	sum := sha256.Sum256([]byte(module + "@" + version))
	return "go-" + hex.EncodeToString(sum[:])
}
func inspectModule(raw, mod, info []byte, record moduleRecord, sums map[string]string) (Source, []byte, error) {
	if len(raw) == 0 || int64(len(raw)) > OriginalLimit || len(mod) == 0 || int64(len(mod)) > MetadataLimit || len(info) == 0 || int64(len(info)) > MetadataLimit {
		return Source{}, nil, fmt.Errorf("module original exceeds bounds")
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return Source{}, nil, fmt.Errorf("invalid module ZIP")
	}
	if len(archive.File) == 0 || len(archive.File) > 100000 {
		return Source{}, nil, fmt.Errorf("invalid module ZIP member count")
	}
	prefix := record.Path + "@" + record.Version + "/"
	names := map[string]bool{}
	hashes := map[string]string{}
	var expanded int64
	var members []Member
	item := Source{ID: goModuleID(record.Path, record.Version), Ecosystem: "go", Name: record.Path, Version: record.Version, Origin: record.Path, URL: "https://proxy.golang.org/" + escapeModule(record.Path) + "/@v/" + record.Version + ".zip", Original: fact("go/"+strings.TrimPrefix(goModuleID(record.Path, record.Version), "go-")+".zip", raw), LicensePaths: []string{}, BuildInputPaths: []string{"go.mod"}, PreferredSourcePaths: []string{}, ObservedInputs: map[string]File{}}
	for _, entry := range archive.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if !safePath(name) || !strings.HasPrefix(entry.Name, prefix) || names[entry.Name] || entry.Mode()&os.ModeSymlink != 0 || !(entry.Mode().IsRegular() || entry.FileInfo().IsDir()) || entry.UncompressedSize64 > uint64(OriginalLimit) {
			return item, nil, fmt.Errorf("unsafe or duplicate module ZIP member")
		}
		names[entry.Name] = true
		size := int64(entry.UncompressedSize64)
		expanded += size
		if expanded > OriginalLimit {
			return item, nil, fmt.Errorf("module ZIP expansion exceeds bounds")
		}
		stream, err := entry.Open()
		if err != nil {
			return item, nil, err
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(stream, size+1))
		closeErr := stream.Close()
		if err != nil || closeErr != nil || n != size {
			return item, nil, fmt.Errorf("module ZIP member failed integrity check")
		}
		digest := hex.EncodeToString(h.Sum(nil))
		hashes[entry.Name] = digest
		relative := strings.TrimPrefix(entry.Name, prefix)
		kind := "file"
		if entry.FileInfo().IsDir() {
			kind = "directory"
		}
		members = append(members, Member{Path: name, Kind: kind, Mode: int64(entry.Mode().Perm()), Size: size, SHA256: "sha256:" + digest})
		lower := strings.ToLower(path.Base(relative))
		for _, legal := range []string{"license", "licence", "copying", "copyright", "notice", "sqlite-license"} {
			if strings.HasPrefix(lower, legal) {
				item.LicensePaths = append(item.LicensePaths, strings.TrimSuffix(relative, "/"))
				item.ObservedInputs[strings.TrimSuffix(relative, "/")] = File{strings.TrimSuffix(relative, "/"), "sha256:" + digest, size}
				break
			}
		}
	}
	zipSum := h1(hashes)
	modHash := sha256.Sum256(mod)
	modSum := h1(map[string]string{"go.mod": hex.EncodeToString(modHash[:])})
	key := record.Path + "@" + record.Version
	if zipSum != record.Sum || zipSum != sums[key] || modSum != record.GoModSum || modSum != sums[key+"/go.mod"] {
		return item, nil, fmt.Errorf("module originals differ from authenticated H1 checksums")
	}
	value, err := jsonObject(info)
	if err != nil {
		return item, nil, err
	}
	var version string
	json.Unmarshal(value["Version"], &version)
	if version != record.Version {
		return item, nil, fmt.Errorf("module version original differs")
	}
	base := strings.TrimSuffix(item.Original.Path, ".zip")
	item.Auxiliary = []File{fact(base+".mod", mod), fact(base+".info", info)}
	item.Authentication = map[string]string{"zip_h1": zipSum, "go_mod_h1": modSum}
	sort.Slice(members, func(i, j int) bool { return members[i].Path < members[j].Path })
	membersRaw, err := contract.JSON(memberInventory{1, "retained-original-source-members", record.Path, record.Version, members})
	if err != nil {
		return item, nil, err
	}
	f := fact("inventory/"+item.ID+".json", membersRaw)
	item.MemberInventory = &f
	return item, membersRaw, nil
}
func escapeModule(module string) string {
	var b strings.Builder
	for _, r := range module {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + 'a' - 'A')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func selectedGraph(rows []moduleRecord) (map[string]moduleRecord, error) {
	result := map[string]moduleRecord{}
	main := 0
	for _, r := range rows {
		key := r.Path + "@" + r.Version
		if _, ok := result[key]; ok {
			return nil, fmt.Errorf("duplicate selected module")
		}
		if r.Main {
			main++
		} else {
			result[key] = r
		}
	}
	if main != 1 || len(result) == 0 {
		return nil, fmt.Errorf("incomplete selected module graph")
	}
	return result, nil
}
func retainedModule(cache, recordPath string) ([]byte, error) {
	if !strings.HasPrefix(recordPath, "/reports/cache/") {
		return nil, fmt.Errorf("module input escaped private cache")
	}
	relative := strings.TrimPrefix(recordPath, "/reports/cache/")
	if !safePath(relative) {
		return nil, fmt.Errorf("unsafe module cache path")
	}
	local := filepath.Join(cache, filepath.FromSlash(relative))
	for current := local; current != cache; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("module cache contains missing or linked input")
		}
	}
	return readRegular(local, OriginalLimit)
}
func moduleInputs(ctx context.Context, o ModuleOptions) (map[string][]byte, error) {
	if err := o.Source.Validate(); err != nil {
		return nil, err
	}
	if o.Platform != "linux/amd64" && o.Platform != "linux/arm64" {
		return nil, fmt.Errorf("unsupported module platform")
	}
	if !goImageRE.MatchString(o.GoImage) {
		return nil, fmt.Errorf("module collection requires immutable official Go builder")
	}
	result := map[string][]byte{}
	for _, name := range []string{"backend/go.mod", "backend/go.sum"} {
		raw, err := committed(ctx, o.Root, o.Source.Commit, name)
		if err != nil {
			return nil, err
		}
		result[name] = raw
	}
	return result, nil
}
func collectModuleFiles(records, selected []moduleRecord, sums map[string]string, read func(moduleRecord, string) ([]byte, error)) ([]Source, map[string][]byte, error) {
	required, err := selectedGraph(selected)
	if err != nil {
		return nil, nil, err
	}
	if len(records) != len(required) {
		return nil, nil, fmt.Errorf("downloaded modules differ from selected graph")
	}
	seen := map[string]bool{}
	files := map[string][]byte{}
	var sources []Source
	for _, record := range records {
		key := record.Path + "@" + record.Version
		if _, ok := required[key]; !ok || seen[key] || record.Main {
			return nil, nil, fmt.Errorf("downloaded module not in selected graph")
		}
		seen[key] = true
		raw, err := read(record, "zip")
		if err != nil {
			return nil, nil, err
		}
		mod, err := read(record, "mod")
		if err != nil {
			return nil, nil, err
		}
		info, err := read(record, "info")
		if err != nil {
			return nil, nil, err
		}
		item, members, err := inspectModule(raw, mod, info, record, sums)
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, item)
		files[item.Original.Path] = raw
		files[item.Auxiliary[0].Path] = mod
		files[item.Auxiliary[1].Path] = info
		files[item.MemberInventory.Path] = members
	}
	return sources, files, nil
}
func CollectModules(ctx context.Context, o ModuleOptions) (Inventory, error) {
	inputs, err := moduleInputs(ctx, o)
	if err != nil {
		return Inventory{}, err
	}
	if _, err = os.Lstat(o.Output); !os.IsNotExist(err) {
		return Inventory{}, fmt.Errorf("module output already exists")
	}
	execute := moduleExecute(o)
	inspected, err := execute(ctx, 55*time.Second, "docker", "image", "inspect", o.GoImage)
	if err != nil {
		return Inventory{}, err
	}
	var configs []struct{ Id, Os, Architecture string }
	if json.Unmarshal(inspected, &configs) != nil || len(configs) != 1 || configs[0].Os != "linux" || configs[0].Architecture != strings.TrimPrefix(o.Platform, "linux/") || !digestRE.MatchString(configs[0].Id) {
		return Inventory{}, fmt.Errorf("Go builder platform/config differs")
	}
	private, err := os.MkdirTemp("", "psst-module-sources-")
	if err != nil {
		return Inventory{}, err
	}
	defer os.RemoveAll(private)
	source := filepath.Join(private, "source")
	reports := filepath.Join(private, "reports")
	for _, dir := range []string{source, reports} {
		if err = os.Mkdir(dir, 0700); err != nil {
			return Inventory{}, err
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		if err = os.WriteFile(filepath.Join(source, name), inputs["backend/"+name], 0600); err != nil {
			return Inventory{}, err
		}
	}
	_, err = execute(ctx, 20*time.Minute, "docker", "run", "--rm", "--platform", o.Platform, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--pids-limit", "128", "--memory", "2g", "--cpus", "2", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=256m", "--env", "HOME=/tmp", "--mount", "type=bind,src="+source+",dst=/source,readonly", "--mount", "type=bind,src="+reports+",dst=/reports", "--entrypoint", "/bin/sh", configs[0].Id, "-ec", goCollectScript)
	if err != nil {
		return Inventory{}, err
	}
	files := map[string][]byte{}
	for name, raw := range inputs {
		files["inputs/"+name] = raw
	}
	for _, name := range append(append([]string{}, goReportNames...), "authenticated-go.sum") {
		raw, e := readRegular(filepath.Join(reports, name), MetadataLimit)
		if e != nil {
			return Inventory{}, e
		}
		prefix := "collection/"
		if name == "authenticated-go.sum" {
			prefix = "inputs/"
		}
		files[prefix+name] = raw
	}
	inventory, err := moduleInventory(o, inputs, files, configs[0].Id)
	if err != nil {
		return inventory, err
	}
	sums, err := authenticatedSums(inputs["backend/go.sum"], files["inputs/authenticated-go.sum"])
	if err != nil {
		return inventory, err
	}
	downloads, err := streamModules(files["collection/go-downloads.json"])
	if err != nil {
		return inventory, err
	}
	selected, err := streamModules(files["collection/go-modules.json"])
	if err != nil {
		return inventory, err
	}
	sources, moduleFiles, err := collectModuleFiles(downloads, selected, sums, func(record moduleRecord, kind string) ([]byte, error) {
		name := record.Zip
		if kind == "mod" {
			name = record.GoMod
		} else if kind == "info" {
			name = record.Info
		}
		return retainedModule(filepath.Join(reports, "cache"), name)
	})
	if err != nil {
		return inventory, err
	}
	inventory.Sources = sources
	for name, raw := range moduleFiles {
		files[name] = raw
	}
	files["SOURCE.md"] = moduleSourceNote(o)
	payload, err := offering(files)
	if err != nil {
		return inventory, err
	}
	assetName := "psst.zip-dependency-inputs-" + o.Source.Version + "-" + strings.TrimPrefix(o.Platform, "linux/") + ".tar.gz"
	inventory.Artifacts = []File{fact(assetName, payload)}
	encoded, err := encode(inventory)
	if err != nil {
		return inventory, err
	}
	if err = os.Mkdir(o.Output, 0700); err != nil {
		return inventory, err
	}
	if err = writeNew(filepath.Join(o.Output, assetName), payload); err != nil {
		return inventory, err
	}
	err = writeNew(filepath.Join(o.Output, "source-inventory.json"), encoded)
	return inventory, err
}
func moduleInventory(o ModuleOptions, inputs, files map[string][]byte, config string) (Inventory, error) {
	inventory := newInventory(o.Source)
	for name, raw := range inputs {
		inventory.Inputs[name] = fact("inputs/"+name, raw)
	}
	inventory.Inputs["authenticated-go.sum"] = fact("inputs/authenticated-go.sum", files["inputs/authenticated-go.sum"])
	var environment map[string]string
	if contract.Decode(files["collection/go-environment.json"], MetadataLimit, &environment) != nil {
		return inventory, fmt.Errorf("invalid module authentication environment")
	}
	actual, _ := contract.JSON(environment)
	expected, _ := contract.JSON(publicGoEnvironment)
	if !bytes.Equal(actual, expected) {
		return inventory, fmt.Errorf("module authentication environment differs")
	}
	version := strings.Fields(string(files["collection/go-version.txt"]))
	if len(version) != 4 || version[0] != "go" || version[1] != "version" || !goVersionRE.MatchString(version[2]) || version[3] != o.Platform {
		return inventory, fmt.Errorf("module collector actual Go version/platform differs")
	}
	collector := Collector{Name: "go-module-originals", Platform: o.Platform, Image: o.GoImage, Config: config, Version: version[2], Environment: environment, Reports: []File{}}
	for _, name := range goReportNames {
		collector.Reports = append(collector.Reports, fact("collection/"+name, files["collection/"+name]))
	}
	inventory.Collectors = []Collector{collector}
	return inventory, nil
}
func moduleSourceNote(o ModuleOptions) []byte {
	return []byte("# psst.zip platform Go module originals\n\nVersion: " + o.Source.Version + "\nSource commit: " + o.Source.Commit + "\nPlatform: " + o.Platform + "\n\nOriginal module ZIP, go.mod and version inputs are retained unchanged.\nEvery ZIP and go.mod is independently verified against authenticated public Go H1 sums.\nCommitted sums remain unchanged; additional authenticated sums are retained separately.\nCommon npm originals and upstream preferred sources are retained once in the common upstream offering.\n")
}

// VerifyModules replays the public-checksum authentication and graph coverage
// using retained bytes only. It never downloads modules or executes source code.
func VerifyModules(ctx context.Context, o ModuleOptions) (Inventory, error) {
	inputs, err := moduleInputs(ctx, o)
	if err != nil {
		return Inventory{}, err
	}
	raw, err := readRegular(filepath.Join(o.Collection, "source-inventory.json"), MetadataLimit)
	if err != nil {
		return Inventory{}, err
	}
	inventory, err := Decode(raw)
	if err != nil {
		return inventory, err
	}
	if inventory.Source != o.Source || len(inventory.Images) != 0 || len(inventory.Collectors) != 1 || len(inventory.Artifacts) != 1 {
		return inventory, fmt.Errorf("module inventory scope differs")
	}
	collector := inventory.Collectors[0]
	if collector.Name != "go-module-originals" || collector.Platform != o.Platform || collector.Image != o.GoImage {
		return inventory, fmt.Errorf("module collector identity differs")
	}
	asset := inventory.Artifacts[0]
	if asset.Path != "psst.zip-dependency-inputs-"+o.Source.Version+"-"+strings.TrimPrefix(o.Platform, "linux/")+".tar.gz" {
		return inventory, fmt.Errorf("module offering name differs")
	}
	payload, err := readRegular(filepath.Join(o.Collection, asset.Path), OfferingLimit)
	if err != nil {
		return inventory, err
	}
	if fact(asset.Path, payload) != asset {
		return inventory, fmt.Errorf("module offering bytes differ")
	}
	entries, err := os.ReadDir(o.Collection)
	if err != nil || len(entries) != 2 {
		return inventory, fmt.Errorf("unexpected module collection files")
	}
	selected := map[string]bool{"SOURCE.md": true, "inputs/authenticated-go.sum": true}
	for name := range inputs {
		selected["inputs/"+name] = true
	}
	for _, name := range goReportNames {
		selected["collection/"+name] = true
	}
	byID := map[string]Source{}
	for _, source := range inventory.Sources {
		if source.Ecosystem != "go" || source.MemberInventory == nil || len(source.Auxiliary) != 2 {
			return inventory, fmt.Errorf("unexpected source in platform module collection")
		}
		byID[source.ID] = source
		selected[source.Original.Path] = true
		selected[source.MemberInventory.Path] = true
		for _, file := range source.Auxiliary {
			selected[file.Path] = true
		}
	}
	members, files, err := inspectArchive(payload, archivePolicy{Expanded: OfferingLimit, Member: OriginalLimit, Members: 100000, Selected: selected})
	if err != nil {
		return inventory, err
	}
	if len(members) != len(selected) || len(files) != len(selected) {
		return inventory, fmt.Errorf("missing or unexpected module payload")
	}
	for name, committed := range inputs {
		if !bytes.Equal(committed, files["inputs/"+name]) {
			return inventory, fmt.Errorf("retained module locks differ from committed source")
		}
	}
	expected, err := moduleInventory(o, inputs, files, collector.Config)
	if err != nil {
		return inventory, err
	}
	gotInputs, _ := contract.JSON(inventory.Inputs)
	wantInputs, _ := contract.JSON(expected.Inputs)
	gotCollector, _ := contract.JSON(inventory.Collectors)
	wantCollector, _ := contract.JSON(expected.Collectors)
	if !bytes.Equal(gotInputs, wantInputs) || !bytes.Equal(gotCollector, wantCollector) || !bytes.Equal(files["SOURCE.md"], moduleSourceNote(o)) {
		return inventory, fmt.Errorf("module collector/raw input inventory differs")
	}
	sums, err := authenticatedSums(inputs["backend/go.sum"], files["inputs/authenticated-go.sum"])
	if err != nil {
		return inventory, err
	}
	downloads, err := streamModules(files["collection/go-downloads.json"])
	if err != nil {
		return inventory, err
	}
	graph, err := streamModules(files["collection/go-modules.json"])
	if err != nil {
		return inventory, err
	}
	sources, retained, err := collectModuleFiles(downloads, graph, sums, func(r moduleRecord, kind string) ([]byte, error) {
		source := byID[goModuleID(r.Path, r.Version)]
		name := strings.TrimSuffix(source.Original.Path, ".zip") + "." + kind
		raw, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("missing retained module original")
		}
		return raw, nil
	})
	if err != nil {
		return inventory, err
	}
	if len(sources) != len(inventory.Sources) {
		return inventory, fmt.Errorf("module inventory graph coverage differs")
	}
	for _, source := range sources {
		got, _ := contract.JSON(byID[source.ID])
		want, _ := contract.JSON(source)
		if !bytes.Equal(got, want) {
			return inventory, fmt.Errorf("module source inventory differs from authenticated originals")
		}
	}
	for name, raw := range retained {
		if !bytes.Equal(files[name], raw) {
			return inventory, fmt.Errorf("module member inventory differs")
		}
	}
	return inventory, nil
}
