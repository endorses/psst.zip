package source

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

const catalogPath = "tools/upstream-application-sources.json"
const lockPath = "web/package-lock.json"

type originalPin struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type upstreamRecord struct {
	ID           string            `json:"id"`
	Repository   string            `json:"repository"`
	Commit       string            `json:"commit"`
	Archive      originalPin       `json:"archive"`
	InspectPaths []string          `json:"inspect_paths"`
	Packages     map[string]string `json:"packages,omitempty"`
	GoModules    map[string]string `json:"go_modules,omitempty"`
	SourceHost   string            `json:"source_host,omitempty"`
	FixtureLinks map[string]string `json:"source_fixture_links,omitempty"`
	Relationship *relationship     `json:"relationship,omitempty"`
}
type relationship struct {
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	Version             string `json:"version"`
	Module              string `json:"module,omitempty"`
	Package             string `json:"package,omitempty"`
	Integrity           string `json:"integrity,omitempty"`
	ConfigurationSHA256 string `json:"configuration_sha256,omitempty"`
}
type upstreamCatalog struct {
	SchemaVersion int              `json:"schema_version"`
	Kind          string           `json:"kind"`
	Upstreams     []upstreamRecord `json:"upstreams"`
}
type memberInventory struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Origin        string   `json:"origin"`
	Version       string   `json:"version"`
	Members       []Member `json:"members"`
}
type Fetch func(context.Context, string, int64) ([]byte, error)

var sourceHTTP = http.Client{Timeout: 180 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConnsPerHost: 4}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("source redirects refused") }}

// HTTPS originals are public, pinned and unauthenticated. Redirects, ambient
// proxies, caller-selected hosts and non-TLS endpoints are deliberately absent.
func officialFetch(ctx context.Context, address string, limit int64) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
		return nil, fmt.Errorf("invalid official source URL")
	}
	switch u.Hostname() {
	case "codeload.github.com", "gitlab.com", "git.musl-libc.org", "registry.npmjs.org":
	default:
		return nil, fmt.Errorf("untrusted source host")
	}
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "psst.zip-release-sources")
	res, err := sourceHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("official source acquisition failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.ContentLength > limit {
		return nil, fmt.Errorf("official source request failed or exceeds limit")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > limit {
		return nil, fmt.Errorf("official source body exceeds bounds or failed")
	}
	return raw, nil
}
func upstreamURL(r upstreamRecord) (string, error) {
	if !commitRE.MatchString(r.Commit) || !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(r.Repository) {
		return "", fmt.Errorf("upstream requires an exact source repository and commit")
	}
	switch r.SourceHost {
	case "":
		return "https://codeload.github.com/" + r.Repository + "/tar.gz/" + r.Commit, nil
	case "gitlab":
		if r.Repository != "cznic/sqlite" && r.Repository != "cznic/libc" && r.Repository != "cznic/libsqlite3" {
			return "", fmt.Errorf("unreviewed GitLab source route")
		}
		return "https://gitlab.com/" + r.Repository + "/-/archive/" + r.Commit + "/" + path.Base(r.Repository) + "-" + r.Commit + ".tar.gz", nil
	case "musl":
		if r.Repository != "musl-libc/musl" {
			return "", fmt.Errorf("unreviewed musl source route")
		}
		return "https://git.musl-libc.org/cgit/musl/snapshot/musl-" + r.Commit + ".tar.gz", nil
	default:
		return "", fmt.Errorf("unsupported upstream source host")
	}
}
func goRequirements(ctx context.Context, mod []byte) (map[string]string, error) {
	private, err := os.MkdirTemp("", "psst-source-mod-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(private)
	if err = os.WriteFile(filepath.Join(private, "go.mod"), mod, 0600); err != nil {
		return nil, err
	}
	raw, err := (process.Runner{Dir: private, Env: map[string]string{"GOWORK": "off", "GOTOOLCHAIN": "local"}}).Output(ctx, 30*time.Second, "go", "mod", "edit", "-json")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Require []struct{ Path, Version string }
		Replace []json.RawMessage
		Exclude []json.RawMessage
	}
	if err = json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Replace) != 0 || len(parsed.Exclude) != 0 {
		return nil, fmt.Errorf("source catalog requires the exact unreplaced module graph")
	}
	result := map[string]string{}
	for _, r := range parsed.Require {
		if _, ok := result[r.Path]; ok {
			return nil, fmt.Errorf("duplicate module requirement")
		}
		result[r.Path] = r.Version
	}
	return result, nil
}
func validateCatalog(ctx context.Context, raw, lock, mod []byte) (upstreamCatalog, error) {
	var catalog upstreamCatalog
	if err := contract.Decode(raw, MetadataLimit, &catalog); err != nil {
		return catalog, err
	}
	if catalog.SchemaVersion != 1 || catalog.Kind != "pinned-upstream-application-source-inputs" || len(catalog.Upstreams) == 0 || len(catalog.Upstreams) > 32 {
		return catalog, fmt.Errorf("unsupported upstream source catalog")
	}
	value, err := jsonObject(lock)
	if err != nil {
		return catalog, err
	}
	var lockVersion int
	json.Unmarshal(value["lockfileVersion"], &lockVersion)
	if lockVersion != 3 {
		return catalog, fmt.Errorf("unsupported npm lock")
	}
	var packages map[string]struct {
		Version string
		Link    bool
	}
	if err = json.Unmarshal(value["packages"], &packages); err != nil {
		return catalog, err
	}
	modules, err := goRequirements(ctx, mod)
	if err != nil {
		return catalog, err
	}
	ids := map[string]bool{}
	primaries := map[string]int{}
	related := map[string]bool{}
	for _, record := range catalog.Upstreams {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`).MatchString(record.ID) || ids[record.ID] || record.Archive.File != path.Base(record.Repository)+"-"+record.Commit+".tar.gz" || !digestRE.MatchString(record.Archive.SHA256) || record.Archive.Size <= 0 || record.Archive.Size > 80<<20 {
			return catalog, fmt.Errorf("invalid catalog source pin")
		}
		ids[record.ID] = true
		if _, err = upstreamURL(record); err != nil {
			return catalog, err
		}
		if (len(record.Packages) == 0) == (len(record.GoModules) == 0) {
			return catalog, fmt.Errorf("catalog must associate exactly one ecosystem")
		}
		for name, version := range record.Packages {
			p, ok := packages["node_modules/"+name]
			if !ok || p.Link || p.Version != version {
				return catalog, fmt.Errorf("catalog differs from exact npm lock")
			}
			related[name] = true
			if record.Relationship == nil {
				primaries[name]++
			}
		}
		for name, version := range record.GoModules {
			if (name != "modernc.org/sqlite" && name != "modernc.org/libc") || modules[name] != version {
				return catalog, fmt.Errorf("catalog differs from backend lock")
			}
		}
		if len(record.InspectPaths) == 0 || len(record.InspectPaths) > 64 {
			return catalog, fmt.Errorf("catalog lacks inspected build inputs")
		}
		seen := map[string]bool{}
		for _, name := range record.InspectPaths {
			if !safePath(name) || seen[name] {
				return catalog, fmt.Errorf("invalid inspected source path")
			}
			seen[name] = true
		}
		for name, target := range record.FixtureLinks {
			if !regexp.MustCompile(`^packages/[A-Za-z0-9_.-]+/test/node_modules/current-package$`).MatchString(name) || target != "../.." {
				return catalog, fmt.Errorf("unreviewed source fixture link")
			}
		}
		if r := record.Relationship; r != nil {
			if r.Name == "" || r.Version == "" {
				return catalog, fmt.Errorf("reviewed relationship lacks identity")
			}
			switch r.Kind {
			case "generator-project":
				if r.Module == "" || record.GoModules[r.Module] == "" || r.Package != "" || r.Integrity != "" || r.ConfigurationSHA256 != "" {
					return catalog, fmt.Errorf("unreviewed generator source association")
				}
			case "embedded-component", "build-configuration":
				if r.Package == "" || record.Packages[r.Package] == "" || len(record.Packages) != 1 || r.Name == r.Package || r.Module != "" {
					return catalog, fmt.Errorf("unreviewed embedded source association")
				}
				if r.Kind == "build-configuration" {
					if !strings.HasPrefix(r.Integrity, "sha512-") || !digestRE.MatchString(r.ConfigurationSHA256) {
						return catalog, fmt.Errorf("external configuration input lacks exact integrity")
					}
				} else if r.Integrity != "" || r.ConfigurationSHA256 != "" {
					return catalog, fmt.Errorf("unexpected embedded relationship fields")
				}
			default:
				return catalog, fmt.Errorf("unsupported reviewed source relationship")
			}
		}
	}
	for name := range related {
		if primaries[name] != 1 {
			return catalog, fmt.Errorf("each locked package needs one primary preferred-source offering")
		}
	}
	sort.Slice(catalog.Upstreams, func(i, j int) bool { return catalog.Upstreams[i].ID < catalog.Upstreams[j].ID })
	return catalog, nil
}
func inspectUpstream(raw []byte, record upstreamRecord) (Source, []byte, error) {
	address, err := upstreamURL(record)
	if err != nil {
		return Source{}, nil, err
	}
	if int64(len(raw)) != record.Archive.Size || hash(raw) != record.Archive.SHA256 {
		return Source{}, nil, fmt.Errorf("original upstream differs from committed pin")
	}
	selected := map[string]bool{}
	for _, name := range record.InspectPaths {
		selected[name] = true
	}
	prefix := path.Base(record.Repository) + "-" + record.Commit
	members, inputs, err := inspectArchive(raw, archivePolicy{Expanded: 512 << 20, Member: 512 << 20, Members: 20000, Prefix: prefix, Commit: record.Commit, Links: record.FixtureLinks, Selected: selected, Original: true})
	if err != nil {
		return Source{}, nil, err
	}
	if len(inputs) != len(selected) {
		return Source{}, nil, fmt.Errorf("preferred source/build inputs are missing")
	}
	if r := record.Relationship; r != nil && r.Kind == "build-configuration" {
		var identity struct{ Name, Version string }
		if json.Unmarshal(inputs["package.json"], &identity) != nil || identity.Name != r.Name || identity.Version != r.Version || hash(inputs["tsconfig.json"]) != r.ConfigurationSHA256 {
			return Source{}, nil, fmt.Errorf("external build configuration original bytes differ from reviewed catalog")
		}
	}
	item := Source{ID: record.ID, Ecosystem: "upstream", Name: record.Repository, Version: record.Commit, Origin: record.Repository, URL: address, Original: fact("archives/"+record.ID+"/"+record.Archive.File, raw), LicensePaths: []string{}, BuildInputPaths: []string{}, PreferredSourcePaths: []string{}, Associations: map[string]string{}, ObservedInputs: map[string]File{}}
	for name, version := range record.Packages {
		item.Associations["npm:"+name] = version
	}
	for name, version := range record.GoModules {
		item.Associations["go:"+name] = version
	}
	for _, member := range members {
		if member.Kind != "file" {
			continue
		}
		relative, _ := stripPrefix(member.Path, prefix)
		base := strings.ToLower(path.Base(relative))
		for _, legal := range []string{"license", "licence", "copying", "copyright", "notice", "patents"} {
			if strings.HasPrefix(base, legal) {
				item.LicensePaths = append(item.LicensePaths, relative)
				break
			}
		}
	}
	for _, name := range record.InspectPaths {
		item.ObservedInputs[name] = fact(name, inputs[name])
		if strings.HasSuffix(name, "package.json") || strings.HasSuffix(name, ".mod") || strings.HasSuffix(name, ".sum") || strings.Contains(path.Base(name), "config") || strings.HasPrefix(path.Base(name), "Makefile") {
			item.BuildInputPaths = append(item.BuildInputPaths, name)
		} else {
			item.PreferredSourcePaths = append(item.PreferredSourcePaths, name)
		}
	}
	inventory, err := contract.JSON(memberInventory{1, "retained-original-source-members", record.Repository, record.Commit, members})
	if err != nil {
		return item, nil, err
	}
	f := fact("inventory/"+record.ID+".json", inventory)
	item.MemberInventory = &f
	return item, inventory, nil
}

type CommonOptions struct {
	Root, Output, Collection string
	Source                   Context
	Fetch                    Fetch
}

func commonInputs(ctx context.Context, o CommonOptions) (map[string][]byte, upstreamCatalog, error) {
	if err := o.Source.Validate(); err != nil {
		return nil, upstreamCatalog{}, err
	}
	runner := process.Runner{Dir: o.Root}
	origin, err := runner.Output(ctx, 30*time.Second, "git", "remote", "get-url", "origin")
	if err != nil {
		return nil, upstreamCatalog{}, err
	}
	value := strings.TrimSpace(string(origin))
	repo := o.Source.Repository
	if value != "https://github.com/"+repo+".git" && value != "https://github.com/"+repo && value != "git@github.com:"+repo+".git" && value != "git@github.com:"+repo {
		return nil, upstreamCatalog{}, fmt.Errorf("source checkout origin differs")
	}
	inputs := map[string][]byte{}
	for _, name := range []string{catalogPath, backendRecipeCatalog, lockPath, "web/package.json", "backend/go.mod", "backend/go.sum"} {
		raw, err := committed(ctx, o.Root, o.Source.Commit, name)
		if err != nil {
			return nil, upstreamCatalog{}, err
		}
		inputs[name] = raw
	}
	catalog, err := validateCatalog(ctx, inputs[catalogPath], inputs[lockPath], inputs["backend/go.mod"])
	return inputs, catalog, err
}
func CollectCommon(ctx context.Context, o CommonOptions) (Inventory, error) {
	inputs, catalog, err := commonInputs(ctx, o)
	if err != nil {
		return Inventory{}, err
	}
	if _, err = os.Lstat(o.Output); !os.IsNotExist(err) {
		return Inventory{}, fmt.Errorf("source collection output already exists")
	}
	inventory := newInventory(o.Source)
	files := map[string][]byte{}
	for name, raw := range inputs {
		target := "inputs/" + name
		files[target] = raw
		inventory.Inputs[name] = fact(target, raw)
	}
	fetch := o.Fetch
	if fetch == nil {
		fetch = officialFetch
	}
	for _, record := range catalog.Upstreams {
		address, _ := upstreamURL(record)
		raw, err := fetch(ctx, address, 80<<20)
		if err != nil {
			return inventory, err
		}
		item, members, err := inspectUpstream(raw, record)
		if err != nil {
			return inventory, err
		}
		inventory.Sources = append(inventory.Sources, item)
		files[item.Original.Path] = raw
		files[item.MemberInventory.Path] = members
	}
	npmSources, npmFiles, err := collectNpm(ctx, inputs[lockPath], fetch)
	if err != nil {
		return inventory, err
	}
	inventory.Sources = append(inventory.Sources, npmSources...)
	for name, raw := range npmFiles {
		files[name] = raw
	}
	files["SOURCE.md"] = commonSourceNote(o.Source)
	rawInventory, err := encode(inventory)
	if err != nil {
		return inventory, err
	}
	files["source-inventory.json"] = rawInventory
	payload, err := offering(files)
	if err != nil {
		return inventory, err
	}
	assetName := "psst.zip-upstream-inputs-" + o.Source.Version + ".tar.gz"
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
func VerifyCommon(ctx context.Context, o CommonOptions) (Inventory, error) {
	inventory, _, err := ReadCommon(ctx, o)
	return inventory, err
}
func ReadCommon(ctx context.Context, o CommonOptions) (Inventory, map[string][]byte, error) {
	inputs, catalog, err := commonInputs(ctx, o)
	if err != nil {
		return Inventory{}, nil, err
	}
	raw, err := readRegular(filepath.Join(o.Collection, "source-inventory.json"), MetadataLimit)
	if err != nil {
		return Inventory{}, nil, err
	}
	inventory, err := Decode(raw)
	if err != nil {
		return inventory, nil, err
	}
	npmInputs, err := lockedNpm(inputs[lockPath])
	if err != nil {
		return inventory, nil, err
	}
	if inventory.Source != o.Source || len(inventory.Images) != 0 || len(inventory.Artifacts) != 0 || len(inventory.Sources) != len(catalog.Upstreams)+len(npmInputs) || len(inventory.Inputs) != len(inputs) {
		return inventory, nil, fmt.Errorf("upstream source inventory scope differs")
	}
	payload, err := readRegular(filepath.Join(o.Collection, "psst.zip-upstream-inputs-"+o.Source.Version+".tar.gz"), OfferingLimit)
	if err != nil {
		return inventory, nil, err
	}
	entries, err := os.ReadDir(o.Collection)
	if err != nil {
		return inventory, nil, err
	}
	if len(entries) != 2 {
		return inventory, nil, fmt.Errorf("unexpected source collection files")
	}
	selected := map[string]bool{"SOURCE.md": true, "source-inventory.json": true}
	for name := range inputs {
		selected["inputs/"+name] = true
	}
	for _, record := range catalog.Upstreams {
		selected["archives/"+record.ID+"/"+record.Archive.File] = true
		selected["inventory/"+record.ID+".json"] = true
	}
	for _, item := range inventory.Sources {
		if item.Ecosystem == "npm" {
			if item.MemberInventory == nil {
				return inventory, nil, fmt.Errorf("npm original member inventory missing")
			}
			selected[item.Original.Path] = true
			selected[item.MemberInventory.Path] = true
		}
	}
	members, files, err := inspectArchive(payload, archivePolicy{Expanded: OfferingLimit, Member: OriginalLimit, Members: 100000, Selected: selected})
	if err != nil {
		return inventory, nil, err
	}
	if len(members) != len(selected) || len(files) != len(selected) {
		return inventory, nil, fmt.Errorf("missing or unexpected retained source payload")
	}
	if !bytes.Equal(files["source-inventory.json"], raw) {
		return inventory, nil, fmt.Errorf("source offering canonical inventory differs")
	}
	if !bytes.Equal(files["SOURCE.md"], commonSourceNote(o.Source)) {
		return inventory, nil, fmt.Errorf("source offering explanation differs")
	}
	for name, original := range inputs {
		if !bytes.Equal(files["inputs/"+name], original) || inventory.Inputs[name] != fact("inputs/"+name, original) {
			return inventory, nil, fmt.Errorf("source offering locks differ from committed inputs")
		}
	}
	byID := map[string]Source{}
	for _, item := range inventory.Sources {
		byID[item.ID] = item
	}
	for _, record := range catalog.Upstreams {
		item, members, err := inspectUpstream(files["archives/"+record.ID+"/"+record.Archive.File], record)
		if err != nil {
			return inventory, nil, err
		}
		got, err := contract.JSON(byID[record.ID])
		if err != nil {
			return inventory, nil, err
		}
		want, _ := contract.JSON(item)
		if !bytes.Equal(got, want) || !bytes.Equal(files[item.MemberInventory.Path], members) {
			return inventory, nil, fmt.Errorf("source inventory differs from retained originals")
		}
	}
	checkedNpm := map[string]Source{}
	for _, input := range npmInputs {
		id := npmID(input)
		existing := byID[id]
		original, ok := checkedNpm[existing.Original.Path]
		if !ok {
			var members []byte
			original, members, err = inspectNpm(files[existing.Original.Path], input)
			if err != nil {
				return inventory, nil, err
			}
			if !bytes.Equal(files[original.MemberInventory.Path], members) {
				return inventory, nil, fmt.Errorf("npm member inventory differs from authenticated original")
			}
			checkedNpm[existing.Original.Path] = original
		}
		item, err := npmLockedSource(original, input)
		if err != nil {
			return inventory, nil, err
		}
		got, _ := contract.JSON(existing)
		want, _ := contract.JSON(item)
		if !bytes.Equal(got, want) {
			return inventory, nil, fmt.Errorf("npm inventory differs from locked original")
		}
	}
	return inventory, files, nil
}

func commonSourceNote(source Context) []byte {
	return []byte("# psst.zip retained common dependency sources\n\nVersion: " + source.Version + "\nSource commit: " + source.Commit + "\n\nOriginal full-commit archives, npm distributions, catalog and build locks are retained unchanged.\nEach npm content identity is retained once and mapped to every locked dependency path.\nThe sibling source-inventory.json records exact hashes and legal/build paths.\nOriginal upstream symbolic links are retained as source metadata only and never followed.\nSource associations are reviewed catalog relationships, not a claim of byte-for-byte binary reproduction.\n")
}

// CommonAsset describes the unchanged public asset name after source replay.
func CommonAsset(collection string, source Context) (File, error) {
	if err := source.Validate(); err != nil {
		return File{}, err
	}
	name := "psst.zip-upstream-inputs-" + source.Version + ".tar.gz"
	return streamFact(filepath.Join(collection, name), name, OfferingLimit)
}
