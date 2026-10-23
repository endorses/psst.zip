// Package source retains original source/build inputs and verifies their inventories.
// It is development tooling, independent of the backend and end-user CLI modules.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/artifact"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

const MetadataLimit int64 = 16 << 20
const OriginalLimit int64 = 512 << 20
const OfferingLimit int64 = 2 << 30

var commitRE = regexp.MustCompile(`^[a-f0-9]{40}$`)
var digestRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var versionRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var repositoryRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9._-]*$`)

type Context struct {
	Repository string `json:"repository"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
}

func (c Context) Validate() error {
	if !repositoryRE.MatchString(c.Repository) || !versionRE.MatchString(c.Version) || !commitRE.MatchString(c.Commit) {
		return fmt.Errorf("invalid source identity")
	}
	return nil
}

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Source struct {
	ID                   string            `json:"id"`
	Ecosystem            string            `json:"ecosystem"`
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Origin               string            `json:"origin"`
	URL                  string            `json:"url"`
	Original             File              `json:"original"`
	LicensePaths         []string          `json:"license_paths"`
	BuildInputPaths      []string          `json:"build_input_paths"`
	PreferredSourcePaths []string          `json:"preferred_source_paths"`
	Associations         map[string]string `json:"associations,omitempty"`
	ObservedInputs       map[string]File   `json:"observed_inputs,omitempty"`
	LockPath             string            `json:"lock_path,omitempty"`
	Integrity            string            `json:"integrity,omitempty"`
	Development          bool              `json:"development,omitempty"`
	Optional             bool              `json:"optional,omitempty"`
	MemberInventory      *File             `json:"member_inventory,omitempty"`
	Auxiliary            []File            `json:"auxiliary,omitempty"`
	Authentication       map[string]string `json:"authentication,omitempty"`
	NoticeFiles          map[string]File   `json:"notice_files,omitempty"`
	Recipe               *Recipe           `json:"recipe,omitempty"`
}
type Image struct {
	Component   string   `json:"component"`
	Platform    string   `json:"platform"`
	Config      string   `json:"config"`
	Manifest    string   `json:"manifest"`
	SourceIDs   []string `json:"source_ids"`
	NoticeFiles []File   `json:"notice_files"`
}
type Inventory struct {
	SchemaVersion int                     `json:"schema_version"`
	Kind          string                  `json:"kind"`
	Source        Context                 `json:"source"`
	Inputs        map[string]File         `json:"inputs"`
	Sources       []Source                `json:"sources"`
	Images        []Image                 `json:"images"`
	Artifacts     []File                  `json:"artifacts"`
	Collectors    []Collector             `json:"collectors,omitempty"`
	Runtime       []RuntimeGraph          `json:"runtime,omitempty"`
	Signatures    []SignatureVerification `json:"signatures,omitempty"`
}

type Collector struct {
	Name        string            `json:"name"`
	Platform    string            `json:"platform"`
	Image       string            `json:"image"`
	Config      string            `json:"config"`
	Version     string            `json:"version"`
	Environment map[string]string `json:"environment"`
	Reports     []File            `json:"reports"`
}

func newInventory(c Context) Inventory {
	return Inventory{SchemaVersion: 2, Kind: "container-source-inventory", Source: c, Inputs: map[string]File{}, Sources: []Source{}, Images: []Image{}, Artifacts: []File{}}
}
func hash(raw []byte) string            { h := sha256.Sum256(raw); return "sha256:" + hex.EncodeToString(h[:]) }
func fact(name string, raw []byte) File { return File{name, hash(raw), int64(len(raw))} }
func readRegular(name string, limit int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("source input must be a bounded regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("source input changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || int64(len(data)) != info.Size() || len(data) > int(limit) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, fmt.Errorf("source input changed or exceeded limit")
	}
	return data, nil
}
func committed(ctx context.Context, root, commit, name string) ([]byte, error) {
	if !safePath(name) || !commitRE.MatchString(commit) {
		return nil, fmt.Errorf("invalid committed source input")
	}
	return (process.Runner{Dir: root}).Output(ctx, 30*time.Second, "git", "show", commit+":"+name)
}
func writeNew(name string, raw []byte) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func Decode(raw []byte) (Inventory, error) {
	var result Inventory
	if err := contract.Decode(raw, MetadataLimit, &result); err != nil {
		return result, err
	}
	if result.SchemaVersion != 2 || result.Kind != "container-source-inventory" {
		return result, fmt.Errorf("unsupported source inventory")
	}
	if err := result.Source.Validate(); err != nil {
		return result, err
	}
	if result.Inputs == nil || result.Sources == nil || result.Images == nil || result.Artifacts == nil || len(result.Sources) > 8192 || len(result.Images) > 4 {
		return result, fmt.Errorf("incomplete source inventory")
	}
	checkFile := func(f File) error {
		if !safePath(f.Path) || !digestRE.MatchString(f.SHA256) || f.Size < 0 || f.Size > OfferingLimit {
			return fmt.Errorf("invalid source file inventory")
		}
		return nil
	}
	ids := map[string]bool{}
	for _, item := range result.Sources {
		if item.ID == "" || ids[item.ID] || item.Name == "" || item.Version == "" || item.Origin == "" || item.URL == "" {
			return result, fmt.Errorf("invalid or duplicate source identity")
		}
		ids[item.ID] = true
		if item.LicensePaths == nil || item.BuildInputPaths == nil || item.PreferredSourcePaths == nil {
			return result, fmt.Errorf("source legal/build path inventory is missing")
		}
		if err := checkFile(item.Original); err != nil {
			return result, err
		}
		if item.MemberInventory != nil {
			if err := checkFile(*item.MemberInventory); err != nil {
				return result, err
			}
		}
		for name, f := range item.ObservedInputs {
			if name != f.Path {
				return result, fmt.Errorf("observed source input path differs")
			}
			if err := checkFile(f); err != nil {
				return result, err
			}
		}
		for _, f := range item.Auxiliary {
			if err := checkFile(f); err != nil {
				return result, err
			}
		}
		for label, f := range item.NoticeFiles {
			if label == "" || len(label) > 4096 || strings.IndexFunc(label, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
				return result, fmt.Errorf("invalid source notice label")
			}
			if err := checkFile(f); err != nil {
				return result, err
			}
		}
		if recipe := item.Recipe; recipe != nil {
			if item.Ecosystem != "apk" || !commitRE.MatchString(recipe.Commit) || (recipe.Repository != "main" && recipe.Repository != "community") || len(recipe.Files) == 0 || len(recipe.Files) > 2048 || len(recipe.DeclaredPackages) == 0 || len(recipe.DeclaredPackages) > 2048 || recipe.SourceSHA512 == nil {
				return result, fmt.Errorf("runtime origin recipe inventory is incomplete")
			}
			for _, f := range recipe.Files {
				if err := checkFile(f); err != nil {
					return result, err
				}
			}
		}
		for _, paths := range [][]string{item.LicensePaths, item.BuildInputPaths, item.PreferredSourcePaths} {
			for _, path := range paths {
				if !safePath(path) {
					return result, fmt.Errorf("invalid source legal/build path")
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, image := range result.Images {
		key := image.Component + "/" + image.Platform
		if seen[key] || !(image.Component == "backend" || image.Component == "web") || !(image.Platform == "linux/amd64" || image.Platform == "linux/arm64") || !digestRE.MatchString(image.Config) || !digestRE.MatchString(image.Manifest) {
			return result, fmt.Errorf("invalid image/source association")
		}
		seen[key] = true
		for _, id := range image.SourceIDs {
			if !ids[id] {
				return result, fmt.Errorf("image refers to missing source")
			}
		}
		for _, f := range image.NoticeFiles {
			if err := checkFile(f); err != nil {
				return result, err
			}
		}
	}
	for _, file := range result.Inputs {
		if err := checkFile(file); err != nil {
			return result, err
		}
	}
	for _, file := range result.Artifacts {
		if err := checkFile(file); err != nil {
			return result, err
		}
	}
	for _, c := range result.Collectors {
		if c.Name == "" || c.Version == "" || !digestRE.MatchString(c.Config) || c.Environment == nil || !(c.Platform == "linux/amd64" || c.Platform == "linux/arm64") || len(c.Reports) == 0 {
			return result, fmt.Errorf("invalid source collector identity")
		}
		for _, f := range c.Reports {
			if err := checkFile(f); err != nil {
				return result, err
			}
		}
	}
	components := map[string]bool{}
	for _, graph := range result.Runtime {
		if components[graph.Component] || (graph.Component != "backend" && graph.Component != "web") || (graph.Platform != "linux/amd64" && graph.Platform != "linux/arm64") || !digestRE.MatchString(graph.Config) || !digestRE.MatchString(graph.RuntimeConfigSHA256) || !goVersionRE.MatchString(graph.GoVersion) || len(graph.RootFS) == 0 || len(graph.RootFS) > 128 || len(graph.Layers) == 0 || len(graph.Packages) == 0 {
			return result, fmt.Errorf("runtime source graph inventory is incomplete")
		}
		components[graph.Component] = true
		for _, f := range append([]File{graph.Binary, graph.Notices}, graphLayerFiles(graph)...) {
			if err := checkFile(f); err != nil {
				return result, err
			}
		}
		seen := map[string]bool{}
		for _, id := range graph.SourceIDs {
			if !ids[id] || seen[id] {
				return result, fmt.Errorf("runtime image references missing or duplicate source")
			}
			seen[id] = true
		}
	}
	return result, nil
}

func graphLayerFiles(graph RuntimeGraph) []File {
	files := make([]File, 0, len(graph.Layers)+1)
	for _, layer := range graph.Layers {
		files = append(files, layer.File)
	}
	if graph.WebRelease != nil {
		files = append(files, *graph.WebRelease)
	}
	return files
}
func encode(result Inventory) ([]byte, error) {
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].ID < result.Sources[j].ID })
	raw, err := contract.JSON(result)
	if err != nil {
		return nil, err
	}
	_, err = Decode(raw)
	return raw, err
}
func regularTree(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	var total int64
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source offering tree contains a link")
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !safePath(relative) {
			return fmt.Errorf("unsafe source offering path")
		}
		raw, err := readRegular(name, OriginalLimit)
		if err != nil {
			return err
		}
		total += int64(len(raw))
		if total > OfferingLimit || len(files) >= 100000 {
			return fmt.Errorf("source offering exceeds limit")
		}
		files[relative] = raw
		return nil
	})
	return files, err
}
func safePath(name string) bool {
	return artifact.ValidPath(name) && strings.IndexFunc(name, func(r rune) bool { return r < 32 || r == 127 }) < 0
}
func stripPrefix(name, prefix string) (string, error) {
	if !strings.HasPrefix(name, prefix+"/") {
		return "", fmt.Errorf("source archive prefix differs")
	}
	name = strings.TrimPrefix(name, prefix+"/")
	if !safePath(name) {
		return "", fmt.Errorf("invalid source member")
	}
	return name, nil
}
func jsonObject(raw []byte) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := contract.Decode(raw, MetadataLimit, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("source metadata must be an object")
	}
	return value, nil
}

// SourceMembers returns a retained original member inventory after the enclosing
// collection has been verified. Browser/source consumers can compare actual
// observed bytes against this index without decompressing the original again.
func SourceMembers(source Source, files map[string][]byte) ([]Member, error) {
	if source.MemberInventory == nil {
		return nil, fmt.Errorf("original source has no member inventory")
	}
	raw := files[source.MemberInventory.Path]
	if fact(source.MemberInventory.Path, raw) != *source.MemberInventory {
		return nil, fmt.Errorf("original member inventory changed")
	}
	var inventory memberInventory
	if err := contract.Decode(raw, MetadataLimit, &inventory); err != nil {
		return nil, err
	}
	if inventory.SchemaVersion != 1 || inventory.Kind != "retained-original-source-members" || len(inventory.Members) == 0 {
		return nil, fmt.Errorf("invalid original member inventory")
	}
	return inventory.Members, nil
}
