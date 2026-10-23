// Package containerbuild prepares read-only container candidates and v1 bundles.
// Candidate records never establish CI, provenance or publication authorization.
package containerbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

const maxJSON = 16 << 20

var (
	commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Executor is implemented by process.Runner and bounded test adapters.
type Executor interface {
	Output(context.Context, time.Duration, ...string) ([]byte, error)
}

// Builder carries the source checkout and official local command adapter.
type Builder struct {
	Root   string
	Runner Executor
}

// New selects the standard bounded process runner for a source checkout.
func New(root string) Builder { return Builder{Root: root, Runner: process.Runner{Dir: root}} }

func (b Builder) output(ctx context.Context, args ...string) ([]byte, error) {
	if b.Runner == nil {
		return nil, fmt.Errorf("missing container candidate command adapter")
	}
	raw, err := b.Runner.Output(ctx, 3*time.Minute, args...)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxJSON {
		return nil, fmt.Errorf("container candidate command output exceeds bounds")
	}
	return raw, nil
}

func (b Builder) git(ctx context.Context, args ...string) ([]byte, error) {
	return b.output(ctx, append([]string{"git", "--no-pager", "-c", "core.fsmonitor=false", "-C", b.Root}, args...)...)
}

func (b Builder) commit(ctx context.Context, ref string) (string, error) {
	raw, err := b.git(ctx, "rev-parse", "--verify", ref+"^{commit}")
	value := strings.TrimSpace(string(raw))
	if err != nil || !commitRE.MatchString(value) {
		return "", fmt.Errorf("cannot resolve exact candidate source commit")
	}
	return value, nil
}

// SourceIdentity keeps planned dispatches distinct from version-tag candidates.
type SourceIdentity struct {
	Version                string `json:"version"`
	Commit                 string `json:"commit"`
	SourceKind             string `json:"source_kind"`
	CandidateOnly          bool   `json:"candidate_only"`
	PlannedCandidateOnly   bool   `json:"planned_candidate_only"`
	TaggedSourceCIVerified bool   `json:"tagged_source_ci_verified"`
	PublicationAuthorized  bool   `json:"publication_authorized"`
}

// ValidateTag resolves lightweight/annotated tag and event objects to the same
// full commit and requires that commit to be reachable from origin/main.
func (b Builder) ValidateTag(ctx context.Context, ref, eventSHA string) (SourceIdentity, error) {
	version := strings.TrimPrefix(ref, "refs/tags/")
	if ref == version || !contract.ReleaseVersion.MatchString(version) || !commitRE.MatchString(eventSHA) {
		return SourceIdentity{}, fmt.Errorf("candidate requires an exact stable version tag and event SHA")
	}
	commit, err := b.commit(ctx, ref)
	if err != nil {
		return SourceIdentity{}, err
	}
	event, err := b.commit(ctx, eventSHA)
	if err != nil || event != commit {
		return SourceIdentity{}, fmt.Errorf("event SHA and version tag differ")
	}
	if _, err := b.git(ctx, "merge-base", "--is-ancestor", commit, "refs/remotes/origin/main"); err != nil {
		return SourceIdentity{}, fmt.Errorf("tagged commit is not reachable from origin/main")
	}
	return SourceIdentity{Version: version, Commit: commit, SourceKind: "version-tag", CandidateOnly: true}, nil
}

// ValidateSource checks an unused planned version at exact main-dispatch HEAD.
// It does not create a tag or claim tagged CI/signing/publication readiness.
func (b Builder) ValidateSource(ctx context.Context, ref, eventSHA, version string) (SourceIdentity, error) {
	if ref != "refs/heads/main" || !contract.ReleaseVersion.MatchString(version) || !commitRE.MatchString(eventSHA) {
		return SourceIdentity{}, fmt.Errorf("planned candidates require exact main dispatch and an unused stable version")
	}
	head, err := b.commit(ctx, "HEAD")
	if err != nil || head != eventSHA {
		return SourceIdentity{}, fmt.Errorf("dispatch source SHA and checked-out HEAD differ")
	}
	resolved, err := b.commit(ctx, eventSHA)
	if err != nil || resolved != head {
		return SourceIdentity{}, fmt.Errorf("dispatch SHA does not resolve to HEAD")
	}
	if _, err := b.git(ctx, "merge-base", "--is-ancestor", head, "refs/remotes/origin/main"); err != nil {
		return SourceIdentity{}, fmt.Errorf("dispatch source is not reachable from origin/main")
	}
	_, err = b.git(ctx, "show-ref", "--verify", "--quiet", "refs/tags/"+version)
	if err == nil {
		return SourceIdentity{}, fmt.Errorf("planned version already has a tag")
	}
	var command *process.Error
	if !errors.As(err, &command) || command.Kind != "exit" || command.ExitCode != 1 {
		return SourceIdentity{}, fmt.Errorf("cannot establish planned version tag absence")
	}
	return SourceIdentity{Version: version, Commit: head, SourceKind: "planned-main-dispatch", CandidateOnly: true, PlannedCandidateOnly: true}, nil
}

// Candidate is the existing v1 read-only base-resolution contract. Source kind
// belongs to SourceIdentity/workflow context, not implicit readiness flags.
type Candidate struct {
	SchemaVersion       int                          `json:"schema_version"`
	Kind                string                       `json:"kind"`
	CandidateOnly       bool                         `json:"candidate_only"`
	Version             string                       `json:"version"`
	SourceCommit        string                       `json:"source_commit"`
	Platforms           []string                     `json:"platforms"`
	BaseImages          map[string]string            `json:"base_images"`
	BasePlatformDigests map[string]map[string]string `json:"base_platform_digests"`
}

// DecodeCandidate rejects unknown fields, duplicate keys and invalid base graphs.
func DecodeCandidate(raw []byte) (Candidate, error) {
	var candidate Candidate
	if err := contract.Decode(raw, maxJSON, &candidate); err != nil {
		return candidate, err
	}
	return candidate, candidate.Validate()
}

// BuildRecord records actual native execution, original Buildx metadata and save
// bytes. Image config and exporter digest may be equal for a --load exporter.
type BuildRecord struct {
	Candidate
	CheckedPlatform string                     `json:"checked_platform"`
	NativeExecution bool                       `json:"native_execution"`
	ToolchainOutput map[string]string          `json:"toolchain_output"`
	BuildMetadata   map[string]json.RawMessage `json:"build_metadata"`
	ImageArchive    ArchiveRecord              `json:"image_archive"`
	Limitations     []string                   `json:"limitations"`
}

// ArchiveRecord identifies the original paired image-save artifact.
type ArchiveRecord struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// RecordBuild binds original image-save bytes and metadata to native tool output.
func (b Builder) RecordBuild(ctx context.Context, candidate Candidate, platform string, metadata map[string]json.RawMessage, archive string) (BuildRecord, error) {
	if err := candidate.Validate(); err != nil {
		return BuildRecord{}, err
	}
	if platform != "linux/amd64" && platform != "linux/arm64" || len(metadata) != 2 {
		return BuildRecord{}, fmt.Errorf("invalid native build platform or metadata pair")
	}
	for _, component := range []string{"backend", "web"} {
		var value map[string]json.RawMessage
		if err := contract.Decode(metadata[component], maxJSON, &value); err != nil || value == nil {
			return BuildRecord{}, fmt.Errorf("invalid paired Buildx metadata")
		}
		for _, key := range []string{"containerimage.config.digest", "containerimage.digest"} {
			var identity string
			if err := json.Unmarshal(value[key], &identity); err != nil || !digestRE.MatchString(identity) {
				return BuildRecord{}, fmt.Errorf("missing actual Buildx image/config identity")
			}
		}
	}
	info, err := os.Lstat(archive)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 8<<30 {
		return BuildRecord{}, fmt.Errorf("candidate save must be a bounded regular file")
	}
	file, err := os.Open(archive)
	if err != nil {
		return BuildRecord{}, err
	}
	before, err := file.Stat()
	if err != nil || !os.SameFile(info, before) {
		_ = file.Close()
		return BuildRecord{}, fmt.Errorf("candidate save changed before hashing")
	}
	hash := sha256.New()
	count, readErr := io.Copy(hash, io.LimitReader(file, info.Size()+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil || count != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return BuildRecord{}, fmt.Errorf("candidate save changed during hashing")
	}
	native, err := b.output(ctx, "docker", "info", "--format", "{{.Architecture}}")
	if err != nil {
		return BuildRecord{}, err
	}
	arch := strings.TrimSpace(string(native))
	if normalized := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[arch]; normalized != "" {
		arch = normalized
	}
	if platform != "linux/"+arch {
		return BuildRecord{}, fmt.Errorf("candidate checks require a native Docker host")
	}
	commands := map[string][]string{
		"go":      {"docker", "run", "--rm", "--network", "none", "--platform", platform, candidate.BaseImages["golang"], "go", "version"},
		"node":    {"docker", "run", "--rm", "--network", "none", "--platform", platform, candidate.BaseImages["node"], "node", "--version"},
		"docker":  {"docker", "version", "--format", "{{.Server.Version}}"},
		"compose": {"docker", "compose", "version", "--short"},
		"buildx":  {"docker", "buildx", "version"},
		"builder": {"docker", "buildx", "inspect"},
	}
	outputs := map[string]string{}
	for _, name := range []string{"go", "node", "docker", "compose", "buildx", "builder"} {
		raw, err := b.output(ctx, commands[name]...)
		value := strings.TrimSpace(string(raw))
		if err != nil || value == "" || len(value) > 8192 || !utf8.Valid(raw) {
			return BuildRecord{}, fmt.Errorf("missing, failed or oversized actual toolchain output")
		}
		outputs[name] = value
	}
	return BuildRecord{Candidate: candidate, CheckedPlatform: platform, NativeExecution: true, ToolchainOutput: outputs, BuildMetadata: metadata, ImageArchive: ArchiveRecord{filepath.Base(archive), hex.EncodeToString(hash.Sum(nil)), info.Size()}, Limitations: []string{"No registry publication or authenticated provenance.", "No vulnerability approval, complete runtime-license review, updater or production deployment readiness."}}, nil
}
