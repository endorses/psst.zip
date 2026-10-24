package oci

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const (
	IndexMediaType    = "application/vnd.oci.image.index.v1+json"
	ManifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	ConfigMediaType   = "application/vnd.oci.image.config.v1+json"
	LayerMediaType    = "application/vnd.oci.image.layer.v1.tar"
	GzipLayerType     = "application/vnd.oci.image.layer.v1.tar+gzip"
)

var (
	digestRE     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitRE     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repositoryRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*/[a-z0-9][a-z0-9_.-]*$`)
)

// Options binds a native export to the exact release and configuration tested.
type Options struct {
	Repository, Version, Commit, Component, Platform, TestedConfig string
}

func (o Options) validate() error {
	owner, name, _ := strings.Cut(o.Repository, "/")
	if !repositoryRE.MatchString(o.Repository) || len(owner) > 39 || len(name) > 100 || name == "." || name == ".." || !contract.ReleaseVersion.MatchString(o.Version) || !commitRE.MatchString(o.Commit) || !digestRE.MatchString(o.TestedConfig) || o.Component != "backend" && o.Component != "web" || o.Platform != "linux/amd64" && o.Platform != "linux/arm64" {
		return fmt.Errorf("invalid OCI release, component, platform or tested configuration")
	}
	return nil
}

// Descriptor identifies raw content bytes. Layers remain encoded as exported.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Image is the validated runnable child and its tested runtime configuration.
type Image struct {
	Platform, ManifestDigest, ManifestMediaType, ConfigDigest, ArchiveDigest string
	ManifestSize                                                             int64
	BlobCount                                                                int
	Config                                                                   []byte
	RootFS                                                                   []string
	Architecture                                                             string
	Labels                                                                   map[string]string
	Layers                                                                   []Descriptor
}

type platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}

func (p platform) matches(expected string) bool {
	return p.OS+"/"+p.Architecture == expected && (p.Variant == "" || expected == "linux/arm64" && p.Variant == "v8")
}

type wireDescriptor struct {
	Descriptor
	Platform     *platform         `json:"platform"`
	URLs         []string          `json:"urls"`
	Data         json.RawMessage   `json:"data"`
	ArtifactType string            `json:"artifactType"`
	Annotations  map[string]string `json:"annotations"`
}

func (d wireDescriptor) valid(allowed func(string) bool) bool {
	return allowed(d.MediaType) && digestRE.MatchString(d.Digest) && d.Size > 0 && d.Size <= maxArchive && len(d.URLs) == 0 && (len(d.Data) == 0 || bytes.Equal(d.Data, []byte("null")) || bytes.Equal(d.Data, []byte(`""`))) && d.ArtifactType == "" && d.Annotations["vnd.docker.reference.type"] != "attestation-manifest"
}

type document struct {
	SchemaVersion int64            `json:"schemaVersion"`
	MediaType     string           `json:"mediaType"`
	Manifests     []wireDescriptor `json:"manifests"`
	Config        wireDescriptor   `json:"config"`
	Layers        []wireDescriptor `json:"layers"`
	Subject       json.RawMessage  `json:"subject"`
	ArtifactType  string           `json:"artifactType"`
}

func isIndex(v string) bool {
	return v == IndexMediaType || v == "application/vnd.docker.distribution.manifest.list.v2+json"
}
func isManifest(v string) bool {
	return v == ManifestMediaType || v == "application/vnd.docker.distribution.manifest.v2+json"
}
func isConfig(v string) bool {
	return v == ConfigMediaType || v == "application/vnd.docker.container.image.v1+json"
}
func isLayer(v string) bool {
	return v == LayerMediaType || v == GzipLayerType || v == "application/vnd.docker.image.rootfs.diff.tar.gzip"
}

// OCI permits extensible metadata. The shared decoder enforces duplicates,
// nesting and byte bounds; decoding into RawMessage preserves valid extra fields.
func decode(raw []byte, target any) error {
	var checked json.RawMessage
	if err := contract.Decode(raw, maxDocument, &checked); err != nil {
		return fmt.Errorf("invalid bounded OCI JSON: %w", err)
	}
	if err := json.Unmarshal(checked, target); err != nil {
		return fmt.Errorf("invalid OCI JSON fields: %w", err)
	}
	return nil
}

func parseDocument(raw []byte) (document, error) {
	var d document
	if err := decode(raw, &d); err != nil {
		return d, err
	}
	if d.SchemaVersion != 2 || d.ArtifactType != "" || len(d.Subject) > 0 && !bytes.Equal(d.Subject, []byte("null")) {
		return d, fmt.Errorf("invalid OCI schema or attached artifact")
	}
	return d, nil
}

func (a *Archive) graph(opts Options) error {
	var layout map[string]string
	if err := decode(a.docs["oci-layout"], &layout); err != nil || len(layout) != 1 || layout["imageLayoutVersion"] != "1.0.0" {
		return fmt.Errorf("missing or invalid OCI layout")
	}
	index, err := parseDocument(a.docs["index.json"])
	if err != nil || !isIndex(index.MediaType) {
		return fmt.Errorf("export is not a valid OCI index")
	}
	used := map[string]bool{}
	resolve := func(d wireDescriptor, allowed func(string) bool) ([]byte, error) {
		m, ok := a.blobs[d.Digest]
		if !d.valid(allowed) || !ok || m.size != d.Size {
			return nil, fmt.Errorf("OCI descriptor type, size or payload differs")
		}
		used[d.Digest] = true
		return a.docs[d.Digest], nil
	}
	var image document
	var child wireDescriptor
	for depth := 0; ; depth++ {
		if depth == 4 {
			return fmt.Errorf("OCI index nesting exceeds bounds")
		}
		if len(index.Manifests) != 1 {
			return fmt.Errorf("native OCI archive must have one runnable image and no attachments")
		}
		child = index.Manifests[0]
		raw, err := resolve(child, func(v string) bool { return isManifest(v) || isIndex(v) })
		if err != nil {
			return err
		}
		if child.Platform != nil && !child.Platform.matches(opts.Platform) {
			return fmt.Errorf("OCI descriptor announces another platform")
		}
		image, err = parseDocument(raw)
		if err != nil || image.MediaType != child.MediaType {
			return fmt.Errorf("OCI descriptor/document media types differ or document exceeds bounds")
		}
		if isManifest(image.MediaType) {
			break
		}
		index = image
	}
	configRaw, err := resolve(image.Config, isConfig)
	if err != nil {
		return err
	}
	if image.Config.Digest != opts.TestedConfig {
		return fmt.Errorf("OCI export differs from the configuration actually tested")
	}
	var config struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Config       *struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
		RootFS struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if err := decode(configRaw, &config); err != nil || config.Config == nil || config.OS+"/"+config.Architecture != opts.Platform {
		return fmt.Errorf("OCI runtime configuration is invalid or has another platform")
	}
	expected := map[string]string{"org.opencontainers.image.title": "psst.zip " + opts.Component, "org.opencontainers.image.source": "https://github.com/" + opts.Repository, "org.opencontainers.image.version": opts.Version, "org.opencontainers.image.revision": opts.Commit, "org.opencontainers.image.licenses": "AGPL-3.0-only", "zip.psst.source.archive": "https://github.com/" + opts.Repository + "/archive/" + opts.Commit + ".tar.gz"}
	for key, value := range expected {
		if config.Config.Labels[key] != value {
			return fmt.Errorf("OCI configuration has substituted release/source labels")
		}
	}
	if len(image.Layers) == 0 || len(image.Layers) > maxLayers || config.RootFS.Type != "layers" || len(config.RootFS.DiffIDs) != len(image.Layers) {
		return fmt.Errorf("OCI configuration layer inventory differs")
	}
	for i, layer := range image.Layers {
		if _, err := resolve(layer, isLayer); err != nil {
			return err
		}
		if !digestRE.MatchString(config.RootFS.DiffIDs[i]) {
			return fmt.Errorf("invalid OCI uncompressed layer identifier")
		}
		a.image.Layers = append(a.image.Layers, layer.Descriptor)
	}
	if len(used) != len(a.blobs) {
		return fmt.Errorf("OCI export contains unreferenced blobs")
	}
	a.image.Platform = opts.Platform
	a.image.ManifestDigest, a.image.ManifestSize, a.image.ManifestMediaType = child.Digest, child.Size, child.MediaType
	a.image.ConfigDigest, a.image.Config = image.Config.Digest, configRaw
	a.image.RootFS, a.image.Architecture, a.image.Labels = config.RootFS.DiffIDs, config.Architecture, config.Config.Labels
	return nil
}

func (a *Archive) verifyLayers() error {
	var expanded int64
	for i, layer := range a.image.Layers {
		encoded, err := a.OpenLayer(i)
		if err != nil {
			return err
		}
		var decoded io.Reader = encoded
		var gz *gzip.Reader
		if strings.HasSuffix(layer.MediaType, "gzip") {
			gz, err = gzip.NewReader(encoded)
			if err != nil {
				_ = encoded.Close()
				return fmt.Errorf("invalid OCI layer gzip header")
			}
			decoded = gz
		}
		h := sha256.New()
		count, readErr := io.Copy(h, io.LimitReader(decoded, min(maxLayer, maxArchive-expanded)+1))
		if gz != nil {
			if closeErr := gz.Close(); readErr == nil {
				readErr = closeErr
			}
		}
		closeErr := encoded.Close()
		if readErr != nil || closeErr != nil {
			return fmt.Errorf("invalid, truncated or changed OCI layer")
		}
		expanded += count
		if count > maxLayer || expanded > maxArchive {
			return fmt.Errorf("OCI uncompressed layers exceed bounds")
		}
		if digest(h) != a.image.RootFS[i] {
			return fmt.Errorf("OCI layer differs from the tested image configuration")
		}
	}
	return nil
}

// ValidateIndex binds raw paired index bytes to the manifest's exact two runnable
// children. Attached attestations are separate provenance, never runnable inputs.
func ValidateIndex(raw []byte, record contract.ImageV1) error {
	repository, expected, ok := strings.Cut(record.Index, "@")
	if !ok || repository == "" || !digestRE.MatchString(expected) || hashBytes(raw) != expected || len(record.PlatformDigests) != 2 {
		return fmt.Errorf("registry index bytes or child map differs from the release")
	}
	d, err := parseDocument(raw)
	if err != nil || !isIndex(d.MediaType) || len(d.Manifests) != 2 {
		return fmt.Errorf("registry index must have exactly two runnable children")
	}
	seen, names := map[string]bool{}, map[string]bool{}
	for _, child := range d.Manifests {
		if !child.valid(isManifest) || child.Platform == nil || seen[child.Digest] || child.Digest == expected {
			return fmt.Errorf("invalid, duplicate or attached registry child")
		}
		name := child.Platform.OS + "/" + child.Platform.Architecture
		if name != "linux/amd64" && name != "linux/arm64" || names[name] || !child.Platform.matches(name) || record.PlatformDigests[name] != child.Digest {
			return fmt.Errorf("registry child platform or digest differs")
		}
		seen[child.Digest], names[name] = true, true
	}
	return nil
}
