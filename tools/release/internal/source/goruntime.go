package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const goRuntimeCatalog = "tools/go-runtime-sources.json"

type goRuntimePin struct {
	Version, Commit, File, URL, SHA256 string
	Size                               int64
}

// GoRuntimePin identifies the reviewed complete upstream compiler/runtime tree.
type GoRuntimePin = goRuntimePin

// GoRuntimeSourcePin selects a full source pin for the actual executable compiler.
func GoRuntimeSourcePin(raw []byte, version string) (GoRuntimePin, error) {
	return goRuntimeSourcePin(raw, version)
}

type goRuntimePolicy struct {
	SchemaVersion int            `json:"schema_version"`
	Kind          string         `json:"kind"`
	Sources       []goRuntimePin `json:"sources"`
}

func goRuntimeSourcePin(raw []byte, version string) (goRuntimePin, error) {
	var policy goRuntimePolicy
	if err := contract.Decode(raw, MetadataLimit, &policy); err != nil {
		return goRuntimePin{}, err
	}
	if policy.SchemaVersion != 1 || policy.Kind != "pinned-go-runtime-sources" || len(policy.Sources) == 0 || len(policy.Sources) > 32 {
		return goRuntimePin{}, fmt.Errorf("invalid Go runtime source catalog")
	}
	seen := map[string]bool{}
	var selected goRuntimePin
	for _, p := range policy.Sources {
		if !goVersionRE.MatchString(p.Version) || seen[p.Version] || !commitRE.MatchString(p.Commit) || p.File != "go-"+p.Commit+".tar.gz" || p.URL != "https://codeload.github.com/golang/go/tar.gz/"+p.Commit || !digestRE.MatchString("sha256:"+p.SHA256) || p.Size <= 0 || p.Size > OriginalLimit {
			return selected, fmt.Errorf("invalid pinned original Go source")
		}
		seen[p.Version] = true
		if p.Version == version {
			selected = p
		}
	}
	if selected.Version == "" {
		return selected, fmt.Errorf("runtime executable compiler has no reviewed full-source pin")
	}
	return selected, nil
}
func resolvedGitTag(ctx context.Context, repository, tag string, fetch Fetch) (string, map[string][]byte, error) {
	raw, err := fetch(ctx, "https://api.github.com/repos/"+repository+"/git/ref/tags/"+tag, MetadataLimit)
	if err != nil {
		return "", nil, err
	}
	files := map[string][]byte{"tag-ref.json": raw}
	var response struct{ Object struct{ SHA, Type string } }
	if json.Unmarshal(raw, &response) != nil || !commitRE.MatchString(response.Object.SHA) {
		return "", nil, fmt.Errorf("invalid exact upstream tag response")
	}
	if response.Object.Type == "tag" {
		raw, err = fetch(ctx, "https://api.github.com/repos/"+repository+"/git/tags/"+response.Object.SHA, MetadataLimit)
		if err != nil {
			return "", nil, err
		}
		files["tag-object.json"] = raw
		if json.Unmarshal(raw, &response) != nil || !commitRE.MatchString(response.Object.SHA) {
			return "", nil, fmt.Errorf("invalid upstream annotated tag")
		}
	}
	if response.Object.Type != "commit" {
		return "", nil, fmt.Errorf("upstream tag does not identify a commit")
	}
	return response.Object.SHA, files, nil
}
func collectGoRuntime(ctx context.Context, policy []byte, version string, fetch Fetch) (Source, map[string][]byte, error) {
	pin, err := goRuntimeSourcePin(policy, version)
	if err != nil {
		return Source{}, nil, err
	}
	if fetch == nil {
		fetch = publicFetch
	}
	commit, proof, err := resolvedGitTag(ctx, "golang/go", version, fetch)
	if err != nil {
		return Source{}, nil, err
	}
	if commit != pin.Commit {
		return Source{}, nil, fmt.Errorf("Go runtime source tag differs from reviewed original pin")
	}
	raw, err := fetch(ctx, pin.URL, pin.Size)
	if err != nil {
		return Source{}, nil, err
	}
	item, files, err := inspectGoRuntime(raw, pin)
	if err != nil {
		return item, nil, err
	}
	for name, body := range proof {
		target := "go-runtime/" + version + "/" + name
		files[target] = body
		item.Auxiliary = append(item.Auxiliary, fact(target, body))
	}
	sort.Slice(item.Auxiliary, func(i, j int) bool { return item.Auxiliary[i].Path < item.Auxiliary[j].Path })
	return item, files, nil
}
func inspectGoRuntime(raw []byte, pin goRuntimePin) (Source, map[string][]byte, error) {
	item := Source{ID: "go-runtime-" + pin.Version, Ecosystem: "go-runtime", Name: "golang/go", Version: pin.Version, Origin: "golang/go", URL: pin.URL, Original: fact("go-runtime/"+pin.File, raw), LicensePaths: []string{}, BuildInputPaths: []string{"src/go.mod", "src/make.bash"}, PreferredSourcePaths: []string{"src/runtime/proc.go", "src/cmd/compile/main.go"}, ObservedInputs: map[string]File{}, Authentication: map[string]string{"source_commit": pin.Commit}}
	if int64(len(raw)) != pin.Size || hash(raw) != "sha256:"+pin.SHA256 {
		return item, nil, fmt.Errorf("full Go runtime original differs from reviewed pin")
	}
	required := map[string]bool{"LICENSE": true, "PATENTS": true, "src/go.mod": true, "src/runtime/proc.go": true, "src/cmd/compile/main.go": true, "src/make.bash": true}
	prefix := "go-" + pin.Commit
	members, selected, err := inspectArchive(raw, archivePolicy{Expanded: 2 << 30, Member: OriginalLimit, Members: 100000, Prefix: prefix, Commit: pin.Commit, Original: true, Capture: func(name string) bool {
		return name == "VERSION" || required[name] || legalNameRE.MatchString(path.Base(name))
	}})
	if err != nil {
		return item, nil, err
	}
	for name := range required {
		if len(selected[name]) == 0 {
			return item, nil, fmt.Errorf("full Go runtime original lacks required build/source member")
		}
		item.ObservedInputs[name] = fact(name, selected[name])
	}
	if data, ok := selected["VERSION"]; ok {
		line := bytes.SplitN(data, []byte("\n"), 2)[0]
		if string(line) != pin.Version {
			return item, nil, fmt.Errorf("Go runtime VERSION differs from executable")
		}
		item.ObservedInputs["VERSION"] = fact("VERSION", data)
	}
	documents := map[string][]byte{}
	for name, body := range selected {
		if legalNameRE.MatchString(path.Base(name)) {
			documents[prefix+"/"+name] = body
			item.LicensePaths = append(item.LicensePaths, name)
		}
	}
	files := map[string][]byte{item.Original.Path: raw}
	_, facts, retained := retainedDocuments("notices/"+item.ID, documents)
	item.NoticeFiles = facts
	for name, body := range retained {
		files[name] = body
	}
	memberRaw, err := contract.JSON(memberInventory{1, "retained-original-source-members", "golang/go", pin.Commit, members})
	if err != nil {
		return item, nil, err
	}
	f := fact("inventory/"+item.ID+".json", memberRaw)
	item.MemberInventory = &f
	files[f.Path] = memberRaw
	sort.Strings(item.LicensePaths)
	return item, files, nil
}
