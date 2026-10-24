// Package clibuild prepares the CLI's matching browser assets and release inputs.
package clibuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/notices"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

type Metadata struct {
	Product           string            `json:"product"`
	Revision          string            `json:"revision"`
	AssetDigest       string            `json:"asset_digest"`
	Source            string            `json:"source"`
	SourceArchive     string            `json:"source_archive"`
	SourceDirty       bool              `json:"source_dirty"`
	InputDigest       string            `json:"input_digest"`
	WebInputDigest    string            `json:"web_input_digest"`
	NoticeInputDigest string            `json:"notice_input_digest"`
	Files             map[string]string `json:"files"`
}

type AssetsOptions struct {
	Root, ReleaseVersion string
	Check                bool
}

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var stableWebVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func walkFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source contains a symbolic link: %s", path)
		}
		if !entry.IsDir() {
			if !entry.Type().IsRegular() {
				return fmt.Errorf("unsupported source file: %s", path)
			}
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func inputDigest(root string, paths []string) (string, error) {
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(filepath.ToSlash(relative) + "\x00"))
		sum := sha256.Sum256(data)
		_, _ = h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func WebDigest(root string) (string, error) {
	web := filepath.Join(root, "web")
	var paths []string
	for _, name := range []string{"package.json", "package-lock.json", "svelte.config.js", "vite.config.ts"} {
		paths = append(paths, filepath.Join(web, name))
	}
	for _, dir := range []string{"src", "static", "scripts"} {
		selected, err := walkFiles(filepath.Join(web, dir))
		if err != nil {
			return "", err
		}
		paths = append(paths, selected...)
	}
	return inputDigest(root, paths)
}

func workspace(root string) bool {
	_, err := os.Stat(filepath.Join(root, "client/go.work"))
	return os.Getenv("GOWORK") != "off" && err == nil
}

func NoticeDigest(ctx context.Context, root string) (string, error) {
	raw, err := (process.Runner{Dir: filepath.Join(root, "client")}).Output(ctx, 30*time.Second, "go", "env", "-json", "GOVERSION", "GOROOT")
	if err != nil {
		return "", err
	}
	var info struct{ GOVERSION, GOROOT string }
	if err := json.Unmarshal(raw, &info); err != nil {
		return "", err
	}
	seed, err := toolchainDigest(info.GOROOT, info.GOVERSION, workspace(root))
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte(seed))
	var paths []string
	directories := []string{"client", "tools/release/internal/notices", "tools/release/internal/contract"}
	if workspace(root) {
		directories = append(directories, "backend")
	}
	for _, dir := range directories {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "vendor" || entry.Name() == ".build" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(path)
			if (ext != ".go" && ext != ".mod" && ext != ".sum") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("Go input is a symbolic link")
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	if workspace(root) {
		for _, name := range []string{"go.work", "go.work.sum"} {
			path := filepath.Join(root, "client", name)
			if _, err := os.Stat(path); err == nil {
				paths = append(paths, path)
			}
		}
	}
	digest, err := inputDigest(root, paths)
	if err != nil {
		return "", err
	}
	_, _ = h.Write([]byte(digest))
	return hex.EncodeToString(h.Sum(nil)), nil
}

func toolchainDigest(goRoot, version string, localWorkspace bool) (string, error) {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "tooling-go-v2\x00%t\x00%s\x00", localWorkspace, version)
	entries, err := os.ReadDir(goRoot)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		lower := strings.ToLower(entry.Name())
		if !entry.IsDir() && (strings.HasPrefix(lower, "license") || strings.HasPrefix(lower, "copying") || strings.HasPrefix(lower, "notice") || strings.HasPrefix(lower, "patents")) {
			data, err := os.ReadFile(filepath.Join(goRoot, entry.Name()))
			if err != nil {
				return "", err
			}
			_, _ = h.Write([]byte(entry.Name() + "\x00"))
			_, _ = h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type npmLock struct {
	Packages map[string]map[string]any `json:"packages"`
}

func platformMatches(value any, current string) bool {
	list, ok := value.([]any)
	if !ok {
		return true
	}
	positive := false
	matched := false
	for _, item := range list {
		v, ok := item.(string)
		if !ok {
			return false
		}
		if v == "!"+current {
			return false
		}
		if !strings.HasPrefix(v, "!") {
			positive = true
			if v == current {
				matched = true
			}
		}
	}
	return !positive || matched
}

func NPMCurrent(web, system, cpu, libc string) bool {
	var expected, installed npmLock
	if readJSON(filepath.Join(web, "package-lock.json"), &expected) != nil || readJSON(filepath.Join(web, "node_modules/.package-lock.json"), &installed) != nil || expected.Packages == nil || installed.Packages == nil {
		return false
	}
	for name, data := range installed.Packages {
		if !reflect.DeepEqual(expected.Packages[name], data) {
			return false
		}
	}
	for name, data := range expected.Packages {
		if name == "" {
			continue
		}
		if _, ok := installed.Packages[name]; ok {
			continue
		}
		if data["optional"] != true || (platformMatches(data["os"], system) && platformMatches(data["cpu"], cpu) && platformMatches(data["libc"], libc)) {
			return false
		}
	}
	return true
}

func assetInventory(site string) (map[string]string, error) {
	files := map[string]string{}
	if _, err := os.Stat(site); os.IsNotExist(err) {
		return files, nil
	}
	paths, err := walkFiles(site)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(site, path)
		name := filepath.ToSlash(rel)
		if name == "licenses/SOURCE.json" || filepath.Base(path) == "README.txt" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		files[name] = notices.Hash(data)
	}
	return files, nil
}

func copyTree(from, to string) error {
	paths, err := walkFiles(from)
	if err != nil {
		return err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(from, path)
		dest := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(dest); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("destination is not a regular file")
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func browserFiles(files map[string]string) map[string]string {
	result := map[string]string{}
	for name, hash := range files {
		if !strings.HasPrefix(name, "licenses/backend/") {
			result[name] = hash
		}
	}
	return result
}

// Prepare builds the browser only when its own inputs changed and collects the
// six-target notice graph only when its independently hashed inputs changed.
func Prepare(ctx context.Context, opts AssetsOptions) (Metadata, error) {
	var result Metadata
	webDigest, err := WebDigest(opts.Root)
	if err != nil {
		return result, err
	}
	goDigest, err := NoticeDigest(ctx, opts.Root)
	if err != nil {
		return result, err
	}
	inputs := notices.Hash([]byte(webDigest + goDigest))
	web := filepath.Join(opts.Root, "web")
	site := filepath.Join(opts.Root, "client/internal/lanassets/site")
	metadataPath := filepath.Join(site, "licenses/SOURCE.json")
	var previous Metadata
	_ = readJSON(metadataPath, &previous)
	files, err := assetInventory(site)
	if err != nil {
		return result, err
	}
	fresh := previous.InputDigest == inputs && reflect.DeepEqual(files, previous.Files) && files["index.html"] != ""
	noticeFresh := previous.NoticeInputDigest == goDigest && reflect.DeepEqual(noticeFiles(files), noticeFiles(previous.Files)) && files["licenses/backend/dependency-inventory.json"] != ""
	if opts.Check && !fresh {
		return result, fmt.Errorf("embedded assets are stale; run make build")
	}
	browserFresh := previous.WebInputDigest == webDigest && files["index.html"] != "" && reflect.DeepEqual(browserFiles(files), browserFiles(previous.Files))
	runner := process.Runner{Dir: web}
	if !fresh && !browserFresh {
		system := runtime.GOOS
		if system == "windows" {
			system = "win32"
		}
		cpu := runtime.GOARCH
		if cpu == "amd64" {
			cpu = "x64"
		}
		libc := "glibc"
		if _, err := os.Stat("/etc/alpine-release"); err == nil {
			libc = "musl"
		}
		if !NPMCurrent(web, system, cpu, libc) {
			if _, err := runner.Output(ctx, 5*time.Minute, "npm", "ci", "--ignore-scripts"); err != nil {
				return result, err
			}
		}
		version := os.Getenv("VERSION")
		if !stableWebVersion.MatchString(version) {
			version = "dev"
		}
		if _, err := runner.With(map[string]string{"VERSION": version}).Output(ctx, 2*time.Minute, "npm", "run", "build:lan"); err != nil {
			return result, err
		}
		built := filepath.Join(web, "build-lan")
		if _, err := os.Stat(filepath.Join(built, "index.html")); err != nil {
			return result, fmt.Errorf("browser build did not produce index.html")
		}
		var savedNotices string
		if noticeFresh {
			savedNotices, err = os.MkdirTemp("", "psst-retained-notices-")
			if err != nil {
				return result, err
			}
			defer os.RemoveAll(savedNotices)
			if err := copyTree(filepath.Join(site, "licenses/backend"), savedNotices); err != nil {
				return result, err
			}
		}
		if err := os.MkdirAll(site, 0o755); err != nil {
			return result, err
		}
		entries, err := os.ReadDir(site)
		if err != nil {
			return result, err
		}
		for _, entry := range entries {
			if entry.Name() != "README.txt" {
				if err := os.RemoveAll(filepath.Join(site, entry.Name())); err != nil {
					return result, err
				}
			}
		}
		if err := copyTree(built, site); err != nil {
			return result, err
		}
		if savedNotices != "" {
			if err := copyTree(savedNotices, filepath.Join(site, "licenses/backend")); err != nil {
				return result, err
			}
		}
	}
	// Browser-only edits do not need another Go graph resolution. Reuse the
	// authenticated inventory already embedded when its own source hash matches.
	if !fresh && !noticeFresh {
		temporary, err := os.MkdirTemp("", "psst-go-notices-")
		if err != nil {
			return result, err
		}
		defer os.RemoveAll(temporary)
		if _, err := notices.Collect(ctx, notices.Options{Root: opts.Root, Output: filepath.Join(temporary, "notices"), Workspace: workspace(opts.Root)}); err != nil {
			return result, err
		}
		target := filepath.Join(site, "licenses/backend")
		if err := os.RemoveAll(target); err != nil {
			return result, err
		}
		if err := copyTree(filepath.Join(temporary, "notices"), target); err != nil {
			return result, err
		}
	}
	files, err = assetInventory(site)
	if err != nil {
		return result, err
	}
	revision := "unknown"
	source := ""
	dirty := true
	git := process.Runner{Dir: opts.Root}
	if raw, err := git.Output(ctx, 10*time.Second, "git", "rev-parse", "HEAD"); err == nil {
		revision = strings.TrimSpace(string(raw))
		raw, err = git.Output(ctx, 10*time.Second, "git", "status", "--porcelain", "--untracked-files=all", "--", "client", "backend", "web", "tools/release")
		if err != nil {
			return result, err
		}
		dirty = len(raw) != 0
		refs, err := git.Output(ctx, 10*time.Second, "git", "for-each-ref", "--format=%(refname)", "--contains", revision, "refs/remotes/origin")
		if err != nil {
			return result, err
		}
		if !dirty && len(bytesTrim(refs)) != 0 {
			source = "https://github.com/endorses/psst.zip/tree/" + revision
		}
	} else {
		revision = previous.Revision
		dirty = previous.SourceDirty
		source = previous.Source
	}
	if opts.ReleaseVersion != "" && !stableVersion.MatchString(opts.ReleaseVersion) {
		return result, fmt.Errorf("release version requires stable X.Y.Z")
	}
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		_, _ = fmt.Fprintf(h, "%s\x00%s\n", name, files[name])
	}
	result = Metadata{Product: "psst.zip", Revision: revision, AssetDigest: hex.EncodeToString(h.Sum(nil)), Source: source, SourceDirty: dirty, InputDigest: inputs, WebInputDigest: webDigest, NoticeInputDigest: goDigest, Files: files}
	if opts.ReleaseVersion != "" && !dirty && source != "" {
		result.SourceArchive = "https://github.com/endorses/psst.zip/releases/download/client%2Fv" + opts.ReleaseVersion + "/psst-" + opts.ReleaseVersion + "-source.tar.gz"
	}
	if !opts.Check {
		data, err := contract.JSON(result)
		if err != nil {
			return result, err
		}
		if err := os.MkdirAll(filepath.Dir(metadataPath), 0o755); err != nil {
			return result, err
		}
		if err := os.WriteFile(metadataPath, data, 0o644); err != nil {
			return result, err
		}
	}
	return result, nil
}

func noticeFiles(files map[string]string) map[string]string {
	result := map[string]string{}
	for name, hash := range files {
		if strings.HasPrefix(name, "licenses/backend/") {
			result[name] = hash
		}
	}
	return result
}
func bytesTrim(data []byte) []byte { return []byte(strings.TrimSpace(string(data))) }
