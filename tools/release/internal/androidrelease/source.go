// Package androidrelease owns Android delivery contracts and thin official SDK
// adapters. It is development tooling, never an ordinary Gradle prerequisite.
package androidrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

const Package = "zip.psst.android"
const maxAPK int64 = 1 << 30
const maxMember uint64 = 256 << 20

var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var positivePattern = regexp.MustCompile(`^[1-9][0-9]*$`)

type Version struct {
	Name string `json:"versionName"`
	Code int    `json:"versionCode"`
}
type Source struct {
	Schema   int    `json:"schema_version"`
	Tag      string `json:"tag"`
	Revision string `json:"sourceRevision"`
	Version
}
type Tool struct {
	Root   string
	Runner process.Runner
}

func New(root string) Tool {
	absolute, err := filepath.Abs(root)
	if err == nil {
		root = absolute
	}
	return Tool{Root: root, Runner: process.Runner{Dir: root}}
}
func require(ok bool, message string) error {
	if !ok {
		return errors.New(message)
	}
	return nil
}
func safeRead(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > limit {
		return nil, errors.New("input is unavailable, not regular, or exceeds bounds")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("input could not be opened")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, errors.New("input changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit || int64(len(data)) != info.Size() {
		return nil, errors.New("input could not be read within bounds")
	}
	return data, nil
}

func writeJSON(path string, value any) error {
	data, err := contract.JSON(value)
	if err != nil {
		return errors.New("invalid JSON output")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return errors.New("output directory could not be created")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return errors.New("output already exists or could not be created")
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.New("output could not be written")
	}
	return nil
}
func VersionMetadata(path string) (Version, error) {
	data, err := safeRead(path, 65536)
	if err != nil {
		return Version{}, err
	}
	values := map[string]string{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if _, seen := values[name]; !ok || seen {
			return Version{}, errors.New("invalid version properties")
		}
		values[name] = strings.TrimSpace(value)
	}
	if len(values) != 2 || !versionPattern.MatchString(values["versionName"]) || !positivePattern.MatchString(values["versionCode"]) {
		return Version{}, errors.New("invalid Android release version properties")
	}
	code, err := strconv.Atoi(values["versionCode"])
	if err != nil || code < 1 || code > 2100000000 {
		return Version{}, errors.New("Android version code exceeds supported range")
	}
	return Version{values["versionName"], code}, nil
}
func (t Tool) command(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	operation := "release-command"
	signing := false
	if len(args) > 0 {
		switch filepath.Base(args[0]) {
		case "git":
			operation = "source-git"
		case "zipalign":
			operation = "apk-align"
		case "aapt2":
			operation = "apk-manifest"
		case "apksigner":
			if len(args) > 1 && args[1] == "sign" {
				operation = "apk-sign"
				signing = true
			} else {
				operation = "apk-verify"
			}
		}
	}
	result, err := t.Runner.Result(ctx, timeout, args...)
	if err == nil {
		return string(result.Stdout), nil
	}
	category := failureCategory(result.Stderr, signing)
	var failure *process.Error
	if errors.As(err, &failure) {
		if failure.Permission {
			category = "permission-or-disk"
		}
		switch failure.Kind {
		case "cancelled":
			return "", fmt.Errorf("Android release command cancelled or timed out: %s", operation)
		case "exit":
			return "", fmt.Errorf("Android release command failed: %s (exit %d) [category=%s]", operation, failure.ExitCode, category)
		}
	}
	return "", fmt.Errorf("Android release command could not complete: %s [category=%s]", operation, category)
}
func failureCategory(stderr []byte, signing bool) string {
	if len(stderr) > 65536 {
		stderr = stderr[:65536]
	}
	value := strings.ToLower(string(stderr))
	candidates := []struct {
		name    string
		markers []string
	}{{"permission-or-disk", []string{"permission denied", "operation not permitted", "no space left", "disk quota exceeded", "read-only file system"}}}
	if signing {
		candidates = append(candidates, []struct {
			name    string
			markers []string
		}{
			{"java-provider-or-algorithm", []string{"nosuchalgorithmexception", "nosuchproviderexception", "providernotfoundexception", "providerexception", "unsupportedclassversionerror", "noclassdeffounderror", "classnotfoundexception", "exceptionininitializererror", "could not create the java virtual machine"}},
			{"store-password-or-format", []string{"keystore password was incorrect", "keystore was tampered with", "invalid keystore format", "unrecognized keystore format", "derinputstream", "toderinputstream", "failed to load keystore", "keystore integrity check failed"}},
			{"key-password", []string{"unrecoverablekeyexception", "cannot recover key", "key password was incorrect", "failed to decrypt key"}},
			{"alias-or-private-key", []string{"does not contain key", "does not contain a key", "not a key entry", "not a private key", "no key found", "no certificate chain found", "no such alias", "failed to obtain key with alias"}},
			{"apk-policy", []string{"debuggable", "min-sdk-version", "min sdk version", "malformed apk", "not an apk", "androidmanifest.xml", "zip format error", "apk format error", "unsupported option", "unrecognized option"}},
		}...)
	}
	for _, candidate := range candidates {
		for _, marker := range candidate.markers {
			if strings.Contains(value, marker) {
				return candidate.name
			}
		}
	}
	return "unclassified"
}
func (t Tool) git(ctx context.Context, args ...string) (string, error) {
	return t.command(ctx, 15*time.Second, append([]string{"git"}, args...)...)
}
func (t Tool) ValidateSource(ctx context.Context, tag, revision, mainRef, versionFile string) (Source, error) {
	version, err := VersionMetadata(versionFile)
	if err != nil {
		return Source{}, err
	}
	if !revisionPattern.MatchString(revision) || tag != "android-v"+version.Name {
		return Source{}, errors.New("Android tag/source/version disagree")
	}
	resolved, err := t.git(ctx, "rev-parse", "--verify", "refs/tags/"+tag+"^{commit}")
	if err != nil || strings.TrimSpace(resolved) != revision {
		return Source{}, errors.New("Android tag does not identify selected source")
	}
	head, err := t.git(ctx, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) != revision {
		return Source{}, errors.New("checkout differs from selected Android source")
	}
	if _, err = t.git(ctx, "merge-base", "--is-ancestor", revision, mainRef); err != nil {
		return Source{}, errors.New("Android source is outside protected main ancestry")
	}
	dirty, err := t.git(ctx, "status", "--porcelain", "--untracked-files=no")
	if err != nil || dirty != "" {
		return Source{}, errors.New("tracked source is modified")
	}
	untracked, err := t.git(ctx, "ls-files", "--others", "--exclude-standard", "--", "android", "shared")
	if err != nil || untracked != "" {
		return Source{}, errors.New("untracked mobile source cannot receive a release identity")
	}
	text, err := t.git(ctx, "show", revision+":android/release-version.properties")
	data, readErr := safeRead(versionFile, 65536)
	if err != nil || readErr != nil || text != string(data) {
		return Source{}, errors.New("version file differs from selected source")
	}
	return Source{1, tag, revision, version}, nil
}

type ExistingRelease struct {
	Tag  string `json:"tag_name"`
	Body string `json:"body"`
}

func CheckPublication(data []byte, tag string, version Version) error {
	var records []ExistingRelease
	// GitHub returns other fields; strictly validate JSON syntax/duplicate keys,
	// then decode only the documented tag/body fields.
	var raw []map[string]json.RawMessage
	if err := contract.Decode(data, 8<<20, &raw); err != nil || raw == nil {
		return errors.New("invalid Android release listing")
	}
	for _, record := range raw {
		var item ExistingRelease
		if value, ok := record["tag_name"]; ok {
			if err := json.Unmarshal(value, &item.Tag); err != nil {
				return errors.New("invalid release tag")
			}
		}
		if value, ok := record["body"]; ok {
			if string(value) != "null" {
				if err := json.Unmarshal(value, &item.Body); err != nil {
					return errors.New("invalid release body")
				}
			}
		}
		records = append(records, item)
	}
	if tag != "android-v"+version.Name {
		return errors.New("Android tag and version disagree")
	}
	pattern := regexp.MustCompile(`(?m)^Android versionCode: ([0-9]+)$`)
	for _, record := range records {
		if record.Tag == tag {
			return errors.New("Android release already exists; refusing replacement")
		}
		if !strings.HasPrefix(record.Tag, "android-v") {
			continue
		}
		matches := pattern.FindAllStringSubmatch(record.Body, -1)
		if len(matches) != 1 {
			return errors.New("existing Android release lacks trustworthy version code")
		}
		code, err := strconv.Atoi(matches[0][1])
		if err != nil || code >= version.Code {
			return errors.New("Android version code is reused or decreases")
		}
	}
	return nil
}
