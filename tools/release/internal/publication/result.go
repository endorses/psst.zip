// Package publication holds the one cross-job container release result.
package publication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

type report struct {
	SchemaVersion int             `json:"schema_version"`
	Gate          string          `json:"gate"`
	Binding       string          `json:"binding_digest"`
	Passed        bool            `json:"passed"`
	Details       json.RawMessage `json:"details"`
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
func readReport(path, gate, binding string) (report, error) {
	data, err := read(path)
	if err != nil {
		return report{}, err
	}
	var r report
	if err = contract.Decode(data, Limit, &r); err != nil {
		return r, err
	}
	if r.SchemaVersion != 1 || r.Gate != gate || !r.Passed || r.Binding != binding || len(r.Details) < 2 || r.Details[0] != '{' {
		return r, fmt.Errorf("incomplete or mismatched release check %s", gate)
	}
	return r, nil
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

type Options struct {
	SourceDir, ChecksDir, Prepared, Previous, Recovery, Output, Repository, Version, Commit string
	RunID, Attempt                                                                          int64
}

func Create(o Options) error {
	var result Result
	var remove []string
	if o.Previous != "" {
		raw, err := read(o.Previous)
		if err != nil {
			return err
		}
		result, err = Decode(raw)
		if err != nil {
			return err
		}
		if _, ok := result.Checks["upgrade-recovery"]; ok {
			return fmt.Errorf("completed release result cannot be extended")
		}
		if result.Repository != o.Repository || result.Version != o.Version || result.Commit != o.Commit || result.RunID != o.RunID || result.Attempt != o.Attempt {
			return fmt.Errorf("release result extension changes source or invocation")
		}
		r, err := readReport(o.Recovery, "upgrade-recovery", result.Binding)
		if err != nil {
			return err
		}
		result.Checks[r.Gate] = r.Details
		result.Inputs["source-result"] = Input{digest(raw)}
		remove = append(remove, o.Recovery)
	} else {
		// The preparation producer already checked all source/image bytes. Decode its
		// bounded record strictly before binding that same subject set to the result.
		data, err := read(filepath.Join(o.Prepared, "release-inputs.json"))
		if err != nil {
			return err
		}
		var record map[string]json.RawMessage
		if err = contract.Decode(data, Limit, &record); err != nil {
			return err
		}
		allowed := []string{"schema_version", "kind", "source_kind", "tagged_source_ci_gate_verified", "signer_identity_verified", "repository", "version", "source_commit", "binding_sha256", "assets", "subjects", "dependency_replays", "upstream_replay", "publication_authorized", "measurement_authentication_required"}
		if len(record) != len(allowed) {
			return fmt.Errorf("prepared release input field set differs")
		}
		for _, key := range allowed {
			if _, ok := record[key]; !ok {
				return fmt.Errorf("prepared release input field set differs")
			}
		}
		for key, expected := range map[string]string{"kind": "prepared-release-inputs", "source_kind": "version-tag"} {
			var actual string
			if json.Unmarshal(record[key], &actual) != nil || actual != expected {
				return fmt.Errorf("prepared input is not a tagged release")
			}
		}
		for key, expected := range map[string]bool{"tagged_source_ci_gate_verified": false, "signer_identity_verified": false, "publication_authorized": false, "measurement_authentication_required": true} {
			var actual bool
			if json.Unmarshal(record[key], &actual) != nil || actual != expected {
				return fmt.Errorf("prepared input changed its authorization boundary")
			}
		}
		var schema int
		if json.Unmarshal(record["schema_version"], &schema) != nil || schema != 1 {
			return fmt.Errorf("unsupported prepared release schema")
		}
		var binding string
		var subjects map[string]string
		if err = json.Unmarshal(record["binding_sha256"], &binding); err != nil {
			return err
		}
		if err = json.Unmarshal(record["subjects"], &subjects); err != nil {
			return err
		}
		for key, expected := range map[string]string{"repository": o.Repository, "version": o.Version, "source_commit": o.Commit} {
			var actual string
			if json.Unmarshal(record[key], &actual) != nil || actual != expected {
				return fmt.Errorf("prepared release identity differs")
			}
		}
		result = Result{SchemaVersion: 1, Kind: "container-release-result", Repository: o.Repository, Version: o.Version, Commit: o.Commit, RunID: o.RunID, Attempt: o.Attempt, Binding: binding, Subjects: subjects, Checks: map[string]json.RawMessage{}, Inputs: map[string]Input{}, Platforms: map[string]Platform{}}
		for _, name := range baseChecks {
			path := filepath.Join(o.ChecksDir, name+".json")
			if name == "corresponding-source" {
				path = filepath.Join(o.SourceDir, name+".json")
			}
			r, err := readReport(path, name, binding)
			if err != nil {
				return err
			}
			result.Checks[name] = r.Details
			remove = append(remove, path)
		}
		evidence, err := read(filepath.Join(o.SourceDir, "corresponding-source-evidence.json"))
		if err != nil {
			return err
		}
		result.Inputs["corresponding-source-replay"] = Input{digest(evidence)}
	}
	var smoke struct {
		Execution map[string]string `json:"execution"`
	}
	if err := json.Unmarshal(result.Checks["final-image-smoke"], &smoke); err != nil {
		return err
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		checks := []string{"final-image-smoke"}
		if _, ok := result.Checks["upgrade-recovery"]; ok {
			checks = append(checks, "upgrade-recovery")
		}
		result.Platforms[platform] = Platform{smoke.Execution[platform], checks}
	}
	raw, err := contract.JSON(result)
	if err != nil {
		return err
	}
	if _, err = Decode(raw); err != nil {
		return err
	}
	out, err := os.OpenFile(o.Output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = out.Write(raw); err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// These are private, fully incorporated intermediate envelopes. Preserve raw
	// replay/scanner diagnostics; no consumer needs the redundant serialized gates.
	for _, path := range remove {
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func Run(ctx context.Context, args []string) error {
	_ = ctx
	flags := flag.NewFlagSet("publication-result", flag.ContinueOnError)
	var o Options
	flags.StringVar(&o.SourceDir, "source-dir", "", "source replay output directory")
	flags.StringVar(&o.ChecksDir, "checks-dir", "", "native/source check output directory")
	flags.StringVar(&o.Prepared, "prepared", "", "prepared release input directory")
	flags.StringVar(&o.Previous, "previous-result", "", "authenticated source result to extend")
	flags.StringVar(&o.Recovery, "recovery", "", "completed native recovery result")
	flags.StringVar(&o.Output, "output", "", "new result file")
	flags.StringVar(&o.Repository, "repository", "", "source repository")
	flags.StringVar(&o.Version, "version", "", "exact version tag")
	flags.StringVar(&o.Commit, "commit", "", "exact source commit")
	flags.Int64Var(&o.RunID, "run-id", 0, "GitHub workflow run")
	flags.Int64Var(&o.Attempt, "attempt", 0, "GitHub workflow attempt")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || o.Output == "" {
		return fmt.Errorf("publication-result requires flags and --output")
	}
	return Create(o)
}
