// Package publication holds the one cross-job container release result.
package publication

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const Limit = 16 << 20

var baseChecks = []string{"corresponding-source", "final-image-scanners", "final-image-smoke", "runtime-notices", "source-ci", "source-scanners"}
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var repositoryPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9._-]*$`)

type Input struct {
	SHA256 string `json:"sha256"`
}
type Platform struct {
	Execution string   `json:"execution"`
	Checks    []string `json:"checks"`
}

// Result carries each substantive check once. Raw scanner and replay records are
// inputs referenced by digest; there are no separately signed gate envelopes.
type Result struct {
	SchemaVersion int                        `json:"schema_version"`
	Kind          string                     `json:"kind"`
	Repository    string                     `json:"repository"`
	Version       string                     `json:"version"`
	Commit        string                     `json:"commit"`
	RunID         int64                      `json:"run_id"`
	Attempt       int64                      `json:"attempt"`
	Binding       string                     `json:"binding_digest"`
	Subjects      map[string]string          `json:"subjects"`
	Checks        map[string]json.RawMessage `json:"checks"`
	Inputs        map[string]Input           `json:"inputs"`
	Platforms     map[string]Platform        `json:"platforms"`
}

func read(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > Limit {
		return nil, fmt.Errorf("release result input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("release result input changed before reading")
	}
	data, err := io.ReadAll(io.LimitReader(file, Limit+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if len(data) > Limit || int64(len(data)) != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, fmt.Errorf("release result input changed while reading")
	}
	return data, nil
}
func digest(data []byte) string {
	value := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(value[:])
}
func Decode(data []byte) (Result, error) {
	var r Result
	if err := contract.Decode(data, Limit, &r); err != nil {
		return r, err
	}
	if r.SchemaVersion != 1 || r.Kind != "container-release-result" || !repositoryPattern.MatchString(r.Repository) || !versionPattern.MatchString(r.Version) || !commitPattern.MatchString(r.Commit) || r.RunID < 1 || r.Attempt < 1 || !digestPattern.MatchString(r.Binding) || len(r.Subjects) < 8 || len(r.Subjects) > 128 {
		return r, fmt.Errorf("invalid release result identity")
	}
	required := map[string]bool{"manifest": false, "bundle": false, "backend-index": false, "backend-amd64": false, "backend-arm64": false, "web-index": false, "web-amd64": false, "web-arm64": false}
	for name, subject := range r.Subjects {
		at := len(subject) - len("sha256:") - 64 - 1
		if name == "" || len(subject) > 512 || at < 1 || subject[at] != '@' || !digestPattern.MatchString(subject[at+1:]) || !(len(subject) > 5 && subject[:5] == "file:" || len(subject) > 6 && subject[:6] == "oci://") {
			return r, fmt.Errorf("invalid release result subject")
		}
		if _, ok := required[name]; ok {
			required[name] = true
		}
	}
	for _, found := range required {
		if !found {
			return r, fmt.Errorf("missing required release subject")
		}
	}
	bindingBytes, err := contract.JSON(map[string]any{"repository": r.Repository, "ref": "refs/tags/" + r.Version, "commit": r.Commit, "workflow": ".github/workflows/release.yml", "subjects": r.Subjects})
	if err != nil || digest(bindingBytes) != r.Binding {
		return r, fmt.Errorf("result binding does not match source subjects")
	}
	names := append([]string{}, baseChecks...)
	if _, ok := r.Checks["upgrade-recovery"]; ok {
		names = append(names, "upgrade-recovery")
	}
	if len(r.Checks) != len(names) {
		return r, fmt.Errorf("incomplete release result check set")
	}
	for _, name := range names {
		v, ok := r.Checks[name]
		if !ok || len(v) < 2 || v[0] != '{' {
			return r, fmt.Errorf("invalid release result check %s", name)
		}
	}
	if len(r.Platforms) != 2 || len(r.Inputs) < 1 || len(r.Inputs) > 64 {
		return r, fmt.Errorf("incomplete platform/input inventory")
	}
	expectedChecks := []string{"final-image-smoke"}
	if _, ok := r.Checks["upgrade-recovery"]; ok {
		expectedChecks = append(expectedChecks, "upgrade-recovery")
		if _, ok := r.Inputs["source-result"]; !ok {
			return r, fmt.Errorf("missing prior authenticated source result")
		}
	}
	if _, ok := r.Inputs["corresponding-source-replay"]; !ok {
		return r, fmt.Errorf("missing raw source replay reference")
	}
	var ci struct {
		RunID   int64 `json:"run_id"`
		Attempt int64 `json:"run_attempt"`
	}
	if err = json.Unmarshal(r.Checks["source-ci"], &ci); err != nil || ci.RunID != r.RunID || ci.Attempt != r.Attempt {
		return r, fmt.Errorf("source CI belongs to another run attempt")
	}
	for _, check := range expectedChecks {
		var details struct {
			Execution map[string]string `json:"execution"`
		}
		if err = json.Unmarshal(r.Checks[check], &details); err != nil {
			return r, err
		}
		if len(details.Execution) != 2 || details.Execution["linux/amd64"] != "native" || details.Execution["linux/arm64"] != "native" {
			return r, fmt.Errorf("release result requires both native platforms")
		}
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		p, ok := r.Platforms[platform]
		if !ok || p.Execution != "native" || !slices.Equal(p.Checks, expectedChecks) {
			return r, fmt.Errorf("release result requires both native platforms")
		}
	}
	for name, input := range r.Inputs {
		if name == "" || !digestPattern.MatchString(input.SHA256) {
			return r, fmt.Errorf("invalid release result input digest")
		}
	}
	return r, nil
}
