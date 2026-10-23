package contract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

const MaxDeploymentBundleBytes int64 = 16 << 20

var (
	ReleaseVersion    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	releaseCommit     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	releaseDigest     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	releaseRepository = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*$`)
)
var DeploymentRequiredFiles = []string{
	"LICENSE", "backend/licenses/AGPL-3.0-only.txt", "backend/licenses/THIRD_PARTY_NOTICES.txt", "backend/licenses/dependency-inventory.json",
	"web/static/licenses/AGPL-3.0-only.txt", "web/static/licenses/THIRD_PARTY_NOTICES.txt", "web/static/licenses/dependency-inventory.json", "web/static/licenses/lucide.txt", "web/static/licenses/qr-scanner.txt",
	"deploy/compose.release.yml", "deploy/external-proxy.release.compose.yml", "deploy/release.env.example", "deploy/external-proxy/Caddyfile", "deploy/external-proxy/trusted-proxy.caddy", "deploy/release-manifest.schema.json", "tools/release_artifacts.py",
}

// ManifestV1 is the immutable deployment-ready artifact consumed by existing VPS
// installations. Internal release result changes must not change these fields.
type ManifestV1 struct {
	SchemaVersion  int                `json:"schema_version"`
	Version        string             `json:"version"`
	PayloadProfile string             `json:"payload_profile"`
	Source         SourceV1           `json:"source"`
	Platforms      []string           `json:"platforms"`
	Images         map[string]ImageV1 `json:"images"`
	Bundle         BundleV1           `json:"bundle"`
	Requirements   RequirementsV1     `json:"requirements"`
	Notes          NotesV1            `json:"notes"`
	Build          BuildV1            `json:"build"`
}
type SourceV1 struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	ArchiveURL string `json:"archive_url"`
}
type ImageV1 struct {
	Index           string            `json:"index"`
	PlatformDigests map[string]string `json:"platform_digests"`
}
type BundleV1 struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type RequirementsV1 struct {
	Docker  string `json:"docker"`
	Compose string `json:"compose"`
}
type NotesV1 struct {
	Migration          string `json:"migration"`
	Rollback           string `json:"rollback"`
	CheckpointRequired bool   `json:"checkpoint_required"`
}
type BuildV1 struct {
	BaseImages map[string]string `json:"base_images"`
	Toolchains map[string]string `json:"toolchains"`
}

func DecodeManifestV1(data []byte, repository string) (ManifestV1, error) {
	var m ManifestV1
	if err := Decode(data, MaxDeploymentBundleBytes, &m); err != nil {
		return m, err
	}
	if err := m.Validate(repository); err != nil {
		return ManifestV1{}, err
	}
	return m, nil
}
func (m ManifestV1) Validate(repository string) error {
	ownerName, repoName, _ := strings.Cut(repository, "/")
	if !releaseRepository.MatchString(repository) || !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(ownerName) || len(ownerName) > 39 || len(repoName) > 100 || repoName == "." || repoName == ".." || m.Source.Repository != repository {
		return fmt.Errorf("unexpected source repository")
	}
	if m.SchemaVersion != 1 || !ReleaseVersion.MatchString(m.Version) || !releaseCommit.MatchString(m.Source.Commit) || (m.PayloadProfile != "artifact-foundation" && m.PayloadProfile != "deployment-ready") {
		return fmt.Errorf("invalid deployment schema, version or source")
	}
	if m.Source.ArchiveURL != "https://github.com/"+repository+"/archive/"+m.Source.Commit+".tar.gz" {
		return fmt.Errorf("source archive does not match trusted repository/commit")
	}
	platforms := []string{"linux/amd64", "linux/arm64"}
	if !reflect.DeepEqual(m.Platforms, platforms) || len(m.Images) != 2 {
		return fmt.Errorf("release must cover paired backend/web and both platforms")
	}
	owner, _, _ := strings.Cut(repository, "/")
	for _, name := range []string{"backend", "web"} {
		image, ok := m.Images[name]
		prefix := "ghcr.io/" + owner + "/psst-zip-" + name + "@"
		if !ok || !strings.HasPrefix(image.Index, prefix) || !releaseDigest.MatchString(strings.TrimPrefix(image.Index, prefix)) || len(image.PlatformDigests) != 2 {
			return fmt.Errorf("invalid release image identity")
		}
		seen := map[string]bool{strings.TrimPrefix(image.Index, prefix): true}
		for _, p := range platforms {
			d := image.PlatformDigests[p]
			if !releaseDigest.MatchString(d) || seen[d] {
				return fmt.Errorf("index and architecture image digests must be distinct")
			}
			seen[d] = true
		}
	}
	if m.Bundle.Name != "psst.zip-deployment-"+m.Version+".tar.gz" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(m.Bundle.SHA256) {
		return fmt.Errorf("invalid deployment bundle identity")
	}
	if m.Requirements.Docker != "27.0.0" || m.Requirements.Compose != "2.24.4" {
		return fmt.Errorf("unsupported Docker/Compose requirements")
	}
	if !m.Notes.CheckpointRequired || strings.TrimSpace(m.Notes.Migration) == "" || strings.TrimSpace(m.Notes.Rollback) == "" || len(m.Notes.Migration) > 8192 || len(m.Notes.Rollback) > 8192 {
		return fmt.Errorf("missing checkpoint, migration or rollback notes")
	}
	for name, values := range map[string]map[string]string{"base_images": m.Build.BaseImages, "toolchains": m.Build.Toolchains} {
		required := []string{"go", "node"}
		if name == "base_images" {
			required = []string{"golang", "node", "alpine", "caddy"}
		}
		for _, key := range required {
			if values[key] == "" {
				return fmt.Errorf("missing release build input")
			}
		}
		for key, v := range values {
			if !regexp.MustCompile(`^[a-z][a-z0-9_-]*$`).MatchString(key) || len(v) >= 512 {
				return fmt.Errorf("invalid build input")
			}
			pattern := `^[a-zA-Z0-9][a-zA-Z0-9._+/-]*$`
			if name == "base_images" {
				pattern = `^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`
			}
			if !regexp.MustCompile(pattern).MatchString(v) {
				return fmt.Errorf("invalid or unpinned build input")
			}
		}
	}
	return nil
}

// ReadBundleV1 authenticates bounded archived bytes and checks every legacy
// required member. It returns inert file bytes; callers verify provenance before
// extracting or executing anything contained in this payload.
func ReadBundleV1(filename string, m ManifestV1) (map[string][]byte, error) {
	if filepath.Base(filename) != m.Bundle.Name {
		return nil, fmt.Errorf("unexpected bundle filename")
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxDeploymentBundleBytes {
		return nil, fmt.Errorf("invalid or oversized deployment bundle")
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxDeploymentBundleBytes+1))
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > MaxDeploymentBundleBytes {
		return nil, fmt.Errorf("bundle exceeds size bound")
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != m.Bundle.SHA256 {
		return nil, fmt.Errorf("bundle checksum mismatch")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	expanded, err := io.ReadAll(io.LimitReader(gz, MaxDeploymentBundleBytes+1))
	closeErr = gz.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(expanded)) > MaxDeploymentBundleBytes {
		return nil, fmt.Errorf("expanded bundle exceeds size bound")
	}
	r := tar.NewReader(bytes.NewReader(expanded))
	files := map[string][]byte{}
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if _, ok := files[h.Name]; ok {
			return nil, fmt.Errorf("duplicate bundle path")
		}
		if !deploymentPath(h.Name) || h.Typeflag != tar.TypeReg || h.Mode != 0o644 || h.Uid != 0 || h.Gid != 0 || h.Size < 0 || h.Size > MaxDeploymentBundleBytes {
			return nil, fmt.Errorf("unexpected or unsafe bundle member")
		}
		v, err := io.ReadAll(io.LimitReader(r, h.Size+1))
		if err != nil || int64(len(v)) != h.Size {
			return nil, fmt.Errorf("unreadable bundle member")
		}
		files[h.Name] = v
	}
	for _, name := range DeploymentRequiredFiles {
		if _, ok := files[name]; !ok {
			return nil, fmt.Errorf("incomplete deployment bundle")
		}
	}
	if _, ok := files["release-bundle.json"]; !ok {
		return nil, fmt.Errorf("bundle metadata missing")
	}
	var metadata struct {
		SchemaVersion  int    `json:"schema_version"`
		Version        string `json:"version"`
		SourceCommit   string `json:"source_commit"`
		PayloadProfile string `json:"payload_profile"`
	}
	if err := Decode(files["release-bundle.json"], MaxDeploymentBundleBytes, &metadata); err != nil {
		return nil, err
	}
	if metadata.SchemaVersion != 1 || metadata.Version != m.Version || metadata.SourceCommit != m.Source.Commit || metadata.PayloadProfile != m.PayloadProfile {
		return nil, fmt.Errorf("bundle metadata does not match manifest")
	}
	if m.PayloadProfile == "deployment-ready" {
		if _, ok := files["deploy/update.py"]; !ok {
			return nil, fmt.Errorf("updater/profile mismatch")
		}
	}
	return files, nil
}
func deploymentPath(name string) bool {
	if name == "release-bundle.json" {
		return true
	}
	if name == "" || name == "." || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") || strings.Contains(name, "/../") {
		return false
	}
	for _, fixed := range DeploymentRequiredFiles {
		if name == fixed {
			return true
		}
	}
	// Exact prefixes preserve the original deployment bundle allowlist.
	if name == "deploy/update.py" {
		return true
	}
	parts := strings.Split(name, "/")
	if len(parts) < 4 || parts[0] != "backend" || parts[1] != "licenses" {
		return false
	}
	allowed := map[string]bool{"LICENSE": true, "NOTICE": true, "COPYING": true, "COPYRIGHT": true, "SQLITE-LICENSE": true, "COPYRIGHT-MUSL": true, "LICENSE-GO": true, "LICENSE-MMAP-GO": true, "LICENSE-LOGO": true}
	if !allowed[parts[len(parts)-1]] {
		return false
	}
	for _, p := range parts {
		if p == "." || p == ".." || !regexp.MustCompile(`^[a-zA-Z0-9_.@+-]+$`).MatchString(p) {
			return false
		}
	}
	return true
}
