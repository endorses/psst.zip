package containerbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
)

// DefaultBases returns independent current stable official tags. Resolving these
// tags never authorizes their use until immutable bytes and both children agree.
func DefaultBases() map[string]string {
	return map[string]string{
		"golang":   "docker.io/library/golang:1.27.3-alpine3.24",
		"alpine":   "docker.io/library/alpine:3.24.2",
		"node":     "docker.io/library/node:26.12.1-alpine3.24",
		"caddy":    "docker.io/library/caddy:2.11.7-alpine",
		"buildkit": "docker.io/moby/buildkit:v0.34.1",
	}
}

var baseNames = []string{"golang", "alpine", "node", "caddy", "buildkit"}

// Validate enforces the existing eight-field v1 candidate contract.
func (c Candidate) Validate() error {
	if c.SchemaVersion != 1 || c.Kind != "release-candidate" || !c.CandidateOnly || !contract.ReleaseVersion.MatchString(c.Version) || !commitRE.MatchString(c.SourceCommit) || !reflect.DeepEqual(c.Platforms, []string{"linux/amd64", "linux/arm64"}) || len(c.BaseImages) != len(baseNames) || len(c.BasePlatformDigests) != len(baseNames) {
		return fmt.Errorf("invalid read-only container candidate schema or identity")
	}
	defaults := DefaultBases()
	for _, name := range baseNames {
		prefix := defaults[name][:strings.LastIndex(defaults[name], ":")] + "@"
		index := strings.TrimPrefix(c.BaseImages[name], prefix)
		pair := c.BasePlatformDigests[name]
		if index == c.BaseImages[name] || !digestRE.MatchString(index) || len(pair) != 2 || !digestRE.MatchString(pair["linux/amd64"]) || !digestRE.MatchString(pair["linux/arm64"]) || pair["linux/amd64"] == pair["linux/arm64"] || index == pair["linux/amd64"] || index == pair["linux/arm64"] {
			return fmt.Errorf("candidate has substituted official base repository or invalid platform digest pair")
		}
	}
	return nil
}

type baseDescriptor struct {
	MediaType string          `json:"mediaType"`
	Digest    string          `json:"digest"`
	Size      int64           `json:"size"`
	URLs      []string        `json:"urls"`
	Data      json.RawMessage `json:"data"`
	Platform  struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	} `json:"platform"`
}

type baseDocument struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Digest        string            `json:"digest"`
	Manifests     []json.RawMessage `json:"manifests"`
}

func decodeBase(raw []byte) (baseDocument, error) {
	var checked json.RawMessage
	if err := contract.Decode(raw, maxJSON, &checked); err != nil {
		return baseDocument{}, err
	}
	var document baseDocument
	if err := json.Unmarshal(checked, &document); err != nil {
		return document, fmt.Errorf("invalid official base index fields")
	}
	if document.SchemaVersion != 2 || document.MediaType != oci.IndexMediaType && document.MediaType != "application/vnd.docker.distribution.manifest.list.v2+json" || len(document.Manifests) == 0 || len(document.Manifests) > 256 {
		return document, fmt.Errorf("official base must be a bounded multi-platform index")
	}
	return document, nil
}

func basePlatforms(document baseDocument, index string) (map[string]string, error) {
	if !digestRE.MatchString(index) {
		return nil, fmt.Errorf("invalid official base index digest")
	}
	pair := map[string]string{}
	for _, raw := range document.Manifests {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("official base descriptor must be an object")
		}
		if rawPlatform, present := fields["platform"]; present {
			var platform map[string]json.RawMessage
			if err := json.Unmarshal(rawPlatform, &platform); err != nil || platform == nil {
				return nil, fmt.Errorf("official base platform must be an object")
			}
		}
		var item baseDescriptor
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("invalid official base descriptor fields")
		}
		platform := item.Platform.OS + "/" + item.Platform.Architecture
		if platform != "linux/amd64" && platform != "linux/arm64" {
			continue // Official indexes may cover other architectures/attestations.
		}
		variant := item.Platform.Variant
		if pair[platform] != "" || variant != "" && (platform != "linux/arm64" || variant != "v8") || item.MediaType != oci.ManifestMediaType && item.MediaType != "application/vnd.docker.distribution.manifest.v2+json" || item.Size <= 0 || !digestRE.MatchString(item.Digest) || item.Digest == index || len(item.URLs) != 0 || len(item.Data) != 0 {
			return nil, fmt.Errorf("invalid or duplicate official base platform descriptor")
		}
		pair[platform] = item.Digest
	}
	if len(pair) != 2 || pair["linux/amd64"] == pair["linux/arm64"] {
		return nil, fmt.Errorf("official base index must cover both distinct release architectures")
	}
	return pair, nil
}

// ResolveBases resolves official tags, then verifies raw immutable index bytes.
// A tag mutation between requests cannot change the recorded build input graph.
func (b Builder) ResolveBases(ctx context.Context, version, commit string) (Candidate, error) {
	if !contract.ReleaseVersion.MatchString(version) || !commitRE.MatchString(commit) {
		return Candidate{}, fmt.Errorf("invalid candidate source/version")
	}
	candidate := Candidate{SchemaVersion: 1, Kind: "release-candidate", CandidateOnly: true, Version: version, SourceCommit: commit, Platforms: []string{"linux/amd64", "linux/arm64"}, BaseImages: map[string]string{}, BasePlatformDigests: map[string]map[string]string{}}
	defaults := DefaultBases()
	for _, name := range baseNames {
		tagged := defaults[name]
		summaryRaw, err := b.output(ctx, "docker", "buildx", "imagetools", "inspect", tagged, "--format", "{{json .Manifest}}")
		if err != nil {
			return Candidate{}, err
		}
		summary, err := decodeBase(summaryRaw)
		if err != nil {
			return Candidate{}, err
		}
		pair, err := basePlatforms(summary, summary.Digest)
		if err != nil {
			return Candidate{}, err
		}
		pinned := tagged[:strings.LastIndex(tagged, ":")] + "@" + summary.Digest
		raw, err := b.output(ctx, "docker", "buildx", "imagetools", "inspect", pinned, "--raw")
		if err != nil {
			return Candidate{}, err
		}
		actual, err := decodeBase(raw)
		if err != nil {
			return Candidate{}, err
		}
		hash := sha256.Sum256(raw)
		digest := "sha256:" + hex.EncodeToString(hash[:])
		actualPair, err := basePlatforms(actual, digest)
		if err != nil || digest != summary.Digest || !reflect.DeepEqual(actualPair, pair) {
			return Candidate{}, fmt.Errorf("immutable official base index bytes/platform graph differ")
		}
		candidate.BaseImages[name], candidate.BasePlatformDigests[name] = pinned, pair
	}
	return candidate, candidate.Validate()
}
