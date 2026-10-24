package source

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/oci"
)

type imageMember struct{ offset, size int64 }
type imageArchive struct {
	file    *os.File
	members map[string]imageMember
}
type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) { n, e := c.r.Read(p); c.n += int64(n); return n, e }
func openImageArchive(name string) (*imageArchive, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 8<<30 {
		return nil, fmt.Errorf("image archive must be a bounded regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, fmt.Errorf("image archive changed")
	}
	result := &imageArchive{file: f, members: map[string]imageMember{}}
	counter := &countReader{r: f}
	reader := tar.NewReader(counter)
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			return nil, err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safePath(name) || seen[name] || len(seen) >= 20000 || header.Size < 0 || header.Size > 2<<30 || header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir || len(header.PAXRecords) != 0 {
			f.Close()
			return nil, fmt.Errorf("unsafe image archive member")
		}
		seen[name] = true
		if header.Typeflag == tar.TypeReg {
			result.members[name] = imageMember{counter.n, header.Size}
		}
	}
	after, err := f.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		f.Close()
		return nil, fmt.Errorf("image archive changed")
	}
	return result, nil
}
func (a *imageArchive) read(name string, limit int64) ([]byte, error) {
	m, ok := a.members[name]
	if !ok || m.size > limit {
		return nil, fmt.Errorf("image metadata missing or oversized")
	}
	return io.ReadAll(io.NewSectionReader(a.file, m.offset, m.size))
}
func (a *imageArchive) stream(name string) (io.Reader, error) {
	m, ok := a.members[name]
	if !ok {
		return nil, fmt.Errorf("missing image layer")
	}
	return io.NewSectionReader(a.file, m.offset, m.size), nil
}

type APK struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	License      string `json:"license"`
	Origin       string `json:"origin"`
	AportsCommit string `json:"aports_commit"`
	Checksum     string `json:"apk_checksum"`
	Present      bool   `json:"present_in_final_filesystem"`
}

func apkKey(p APK) string    { return p.Name + "/" + p.Version + "/" + p.AportsCommit }
func apkOrigin(p APK) string { return p.Origin + "/" + p.Version + "/" + p.AportsCommit }

var apkNameRE = regexpName()

func regexpName() *regexp.Regexp { return regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+-]{0,120}$`) }
func APKPackages(raw []byte) ([]APK, error) {
	if len(raw) == 0 || len(raw) > 8<<20 {
		return nil, fmt.Errorf("invalid APK database size")
	}
	var result []APK
	seen := map[string]bool{}
	for _, block := range strings.Split(strings.TrimSpace(string(raw)), "\n\n") {
		values := map[byte]string{}
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "F:") {
				break
			}
			if len(line) > 2 && line[1] == ':' {
				if _, ok := values[line[0]]; ok {
					return nil, fmt.Errorf("duplicate APK metadata")
				}
				values[line[0]] = line[2:]
			}
		}
		for _, key := range []byte{'P', 'V', 'A', 'L', 'o', 'c', 'C'} {
			if values[key] == "" {
				return nil, fmt.Errorf("missing APK origin metadata")
			}
		}
		if !apkNameRE.MatchString(values['P']) || !apkNameRE.MatchString(values['V']) || !apkNameRE.MatchString(values['o']) || !commitRE.MatchString(values['c']) || (values['A'] != "x86_64" && values['A'] != "aarch64" && values['A'] != "noarch") || seen[values['P']] {
			return nil, fmt.Errorf("invalid or duplicate APK identity")
		}
		seen[values['P']] = true
		result = append(result, APK{Name: values['P'], Version: values['V'], Architecture: values['A'], License: values['L'], Origin: values['o'], AportsCommit: values['c'], Checksum: values['C']})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("empty APK database")
	}
	return result, nil
}

type LayerDatabase struct {
	Layer          int    `json:"layer"`
	LayerSHA256    string `json:"layer_sha256"`
	DatabaseSHA256 string `json:"database_sha256"`
	File           File   `json:"file"`
}
type SavedImage struct {
	Config        string
	Architecture  string
	Labels        map[string]string
	RootFS        []string
	RuntimeConfig json.RawMessage
	Files         map[string][]byte
	Packages      []APK
	Databases     []LayerDatabase
	DatabaseFiles map[string][]byte
	LayerEntries  [][]LayerEntry
}

type LayerEntry struct {
	Path string
	Type byte
}

func trackedRuntime(name string) bool {
	return name == "lib/apk/db/installed" || name == "app/server" || name == "usr/bin/caddy" || strings.HasPrefix(name, "app/licenses/") || strings.HasPrefix(name, "srv/web/")
}
func cleanLayerName(name string) (string, error) {
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	name = strings.TrimSuffix(name, "/")
	if name == "." || name == "" {
		return "", nil
	}
	if !safePath(name) {
		return "", fmt.Errorf("unsafe image layer path")
	}
	return name, nil
}
func removeWhiteout(name string, files map[string][]byte) bool {
	base := path.Base(name)
	directory := path.Dir(name)
	if !strings.HasPrefix(base, ".wh.") {
		return false
	}
	target := path.Join(directory, strings.TrimPrefix(base, ".wh."))
	if base == ".wh..wh..opq" {
		target = directory
	}
	for name := range files {
		if target == "." || name == target || strings.HasPrefix(name, target+"/") {
			delete(files, name)
		}
	}
	return true
}
func (a *imageArchive) saved(config string) (SavedImage, error) {
	result := SavedImage{Config: config, Files: map[string][]byte{}, DatabaseFiles: map[string][]byte{}}
	raw, err := a.read("manifest.json", MetadataLimit)
	if err != nil {
		return result, err
	}
	var manifest []struct {
		Config           string
		RepoTags, Layers []string
	}
	if json.Unmarshal(raw, &manifest) != nil || len(manifest) == 0 || len(manifest) > 2 {
		return result, fmt.Errorf("invalid saved image pair")
	}
	var selected []struct {
		raw    []byte
		layers []string
	}
	for _, item := range manifest {
		raw, err := a.read(item.Config, MetadataLimit)
		if err != nil {
			return result, err
		}
		if hash(raw) == config {
			selected = append(selected, struct {
				raw    []byte
				layers []string
			}{raw, item.Layers})
		}
	}
	if len(selected) != 1 {
		return result, fmt.Errorf("saved image configuration missing or ambiguous")
	}
	var cfg struct {
		Architecture, Os string
		Config           json.RawMessage
		RootFS           struct {
			DiffIDs []string `json:"diff_ids"`
		}
	}
	if json.Unmarshal(selected[0].raw, &cfg) != nil || cfg.Os != "linux" || (cfg.Architecture != "amd64" && cfg.Architecture != "arm64") || len(cfg.RootFS.DiffIDs) == 0 || len(cfg.RootFS.DiffIDs) != len(selected[0].layers) || len(cfg.RootFS.DiffIDs) > 128 {
		return result, fmt.Errorf("saved image native config/layers differ")
	}
	var runtime struct{ Labels map[string]string }
	if json.Unmarshal(cfg.Config, &runtime) != nil {
		return result, fmt.Errorf("invalid runtime configuration")
	}
	result.Architecture = cfg.Architecture
	result.Labels = runtime.Labels
	result.RootFS = cfg.RootFS.DiffIDs
	result.RuntimeConfig = cfg.Config
	return replayLayers(result, selected[0].layers, func(index int) (io.ReadCloser, error) {
		reader, err := a.stream(selected[0].layers[index])
		if err != nil {
			return nil, err
		}
		return io.NopCloser(reader), nil
	})
}

func replayLayers(result SavedImage, layers []string, open func(int) (io.ReadCloser, error)) (SavedImage, error) {
	retained := map[string]APK{}
	for index := range layers {
		var layerEntries []LayerEntry
		var pathBytes int
		stream, err := open(index)
		if err != nil {
			return result, err
		}
		defer stream.Close()
		compressedHash := sha256.New()
		compressed := io.TeeReader(stream, compressedHash)
		prefix := make([]byte, 2)
		if _, err = io.ReadFull(compressed, prefix); err != nil {
			return result, err
		}
		buffered := bufio.NewReader(io.MultiReader(bytes.NewReader(prefix), compressed))
		var expanded io.Reader = buffered
		var gz *gzip.Reader
		if bytes.Equal(prefix, []byte{0x1f, 0x8b}) {
			gz, err = gzip.NewReader(expanded)
			if err != nil {
				return result, err
			}
			gz.Multistream(false)
			expanded = gz
		}
		diff := sha256.New()
		limited := &io.LimitedReader{R: io.TeeReader(expanded, diff), N: (8 << 30) + 1}
		reader := tar.NewReader(limited)
		seen := map[string]bool{}
		// Whiteouts are evaluated before entries irrespective of tar ordering.
		var whiteouts []string
		updates := map[string][]byte{}
		deletes := map[string]bool{}
		var directories []string
		var selectedBytes int64
		for count := 0; ; count++ {
			header, e := reader.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return result, e
			}
			member, e := cleanLayerName(header.Name)
			if e != nil {
				return result, e
			}
			if member == "" {
				continue
			}
			if count >= 1000000 || seen[member] || header.Size < 0 || header.Size > 2<<30 {
				return result, fmt.Errorf("invalid image layer member")
			}
			seen[member] = true
			pathBytes += len(member)
			if pathBytes > 16<<20 {
				return result, fmt.Errorf("image layer path inventory exceeds bounds")
			}
			layerEntries = append(layerEntries, LayerEntry{member, header.Typeflag})
			if strings.HasPrefix(path.Base(member), ".wh.") {
				whiteouts = append(whiteouts, member)
				continue
			}
			if header.Typeflag == tar.TypeDir {
				directories = append(directories, member)
				continue
			}
			deletes[member] = true
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
				deletes[member] = true
				continue
			}
			if !trackedRuntime(member) {
				continue
			}
			if header.Typeflag != tar.TypeReg {
				deletes[member] = true
				continue
			}
			limit := int64(32 << 20)
			if member == "app/server" || member == "usr/bin/caddy" {
				limit = OriginalLimit
			}
			if header.Size > limit {
				return result, fmt.Errorf("tracked image payload exceeds bounds")
			}
			selectedBytes += header.Size
			if selectedBytes > OriginalLimit {
				return result, fmt.Errorf("selected image payload exceeds bounds")
			}
			body, e := io.ReadAll(io.LimitReader(reader, limit+1))
			if e != nil || int64(len(body)) != header.Size {
				return result, fmt.Errorf("truncated tracked image payload")
			}
			updates[member] = body
			if member == "lib/apk/db/installed" {
				packages, e := APKPackages(body)
				if e != nil {
					return result, e
				}
				for _, p := range packages {
					retained[apkKey(p)] = p
				}
				name := fmt.Sprintf("databases/%s/%d.apkdb", strings.TrimPrefix(result.Config, "sha256:"), index)
				result.DatabaseFiles[name] = body
				result.Databases = append(result.Databases, LayerDatabase{Layer: index, DatabaseSHA256: strings.TrimPrefix(hash(body), "sha256:"), File: fact(name, body)})
			}
		}
		// Include tar padding in diff ID and validate any compression trailer.
		if _, err = io.Copy(io.Discard, limited); err != nil || limited.N <= 0 {
			return result, fmt.Errorf("image layer expansion failed or exceeds bounds")
		}
		if gz != nil {
			if err = gz.Close(); err != nil {
				return result, err
			}
			if _, err = buffered.Peek(1); err != io.EOF {
				return result, fmt.Errorf("image layer contains trailing compressed payload")
			}
		}
		if "sha256:"+hex.EncodeToString(diff.Sum(nil)) != result.RootFS[index] {
			return result, fmt.Errorf("image layer diff ID differs")
		}
		if _, err = io.Copy(io.Discard, compressed); err != nil {
			return result, err
		}
		if err = stream.Close(); err != nil {
			return result, err
		}
		layerDigest := hex.EncodeToString(compressedHash.Sum(nil))
		for i := range result.Databases {
			if result.Databases[i].Layer == index {
				result.Databases[i].LayerSHA256 = layerDigest
			}
		}
		for _, name := range whiteouts {
			removeWhiteout(name, result.Files)
		}
		for name := range deletes {
			delete(result.Files, name)
			for candidate := range result.Files {
				if strings.HasPrefix(candidate, name+"/") {
					delete(result.Files, candidate)
				}
			}
		}
		for _, name := range directories {
			delete(result.Files, name)
		}
		for name, body := range updates {
			for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
				if deletes[parent] {
					return result, fmt.Errorf("tracked image file has a nondirectory layer ancestor")
				}
			}
			result.Files[name] = body
		}
		result.LayerEntries = append(result.LayerEntries, layerEntries)
	}
	final, err := APKPackages(result.Files["lib/apk/db/installed"])
	if err != nil {
		return result, err
	}
	for _, p := range final {
		p.Present = true
		retained[apkKey(p)] = p
	}
	for _, p := range retained {
		result.Packages = append(result.Packages, p)
	}
	sort.Slice(result.Packages, func(i, j int) bool { return apkKey(result.Packages[i]) < apkKey(result.Packages[j]) })
	if len(result.Databases) == 0 {
		return result, fmt.Errorf("image layers lack retained APK origins")
	}
	return result, nil
}
func InspectSaved(name, config string) (SavedImage, error) {
	archive, err := openImageArchive(name)
	if err != nil {
		return SavedImage{}, err
	}
	defer archive.file.Close()
	return archive.saved(config)
}

// InspectOCI shares the validated OCI graph/blob boundary with publication and
// replays actual filesystem bytes for source/license/browser correspondence.
func InspectOCI(name string, options oci.Options) (SavedImage, oci.Image, error) {
	archive, err := oci.OpenArchive(name, options)
	if err != nil {
		return SavedImage{}, oci.Image{}, err
	}
	defer archive.Close()
	image := archive.Inspect()
	var config struct{ Config json.RawMessage }
	if err = json.Unmarshal(image.Config, &config); err != nil {
		return SavedImage{}, image, err
	}
	result := SavedImage{Config: image.ConfigDigest, Architecture: image.Architecture, Labels: image.Labels, RootFS: image.RootFS, RuntimeConfig: config.Config, Files: map[string][]byte{}, DatabaseFiles: map[string][]byte{}}
	layers := make([]string, len(image.Layers))
	for i, layer := range image.Layers {
		layers[i] = layer.Digest
	}
	result, err = replayLayers(result, layers, archive.OpenLayer)
	if err != nil {
		return result, image, err
	}
	if err = archive.Close(); err != nil {
		return result, image, err
	}
	return result, image, nil
}
