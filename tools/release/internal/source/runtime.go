package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

type Recipe struct {
	Repository       string            `json:"repository"`
	Commit           string            `json:"commit"`
	Files            []File            `json:"files"`
	DeclaredPackages []string          `json:"declared_packages"`
	SourceSHA512     map[string]string `json:"source_sha512"`
}
type RuntimeGraph struct {
	Component           string          `json:"component"`
	Platform            string          `json:"platform"`
	Config              string          `json:"config"`
	RootFS              []string        `json:"rootfs"`
	RuntimeConfigSHA256 string          `json:"runtime_config_sha256"`
	Binary              File            `json:"binary"`
	GoVersion           string          `json:"go_version"`
	Packages            []APK           `json:"packages"`
	Layers              []LayerDatabase `json:"layers"`
	SourceIDs           []string        `json:"source_ids"`
	Notices             File            `json:"notices"`
	WebRelease          *File           `json:"web_release,omitempty"`
}

func runtimeGraph(component string, image SavedImage, source Context) (RuntimeGraph, error) {
	configuration, err := canonicalConfig(image.RuntimeConfig)
	if err != nil {
		return RuntimeGraph{}, err
	}
	result := RuntimeGraph{Component: component, Platform: "linux/" + image.Architecture, Config: image.Config, RootFS: image.RootFS, RuntimeConfigSHA256: hash(configuration), Packages: image.Packages, Layers: image.Databases, SourceIDs: []string{}}
	if component != "backend" && component != "web" || image.Labels["org.opencontainers.image.version"] != source.Version || image.Labels["org.opencontainers.image.revision"] != source.Commit || image.Labels["org.opencontainers.image.source"] != "https://github.com/"+source.Repository {
		return result, fmt.Errorf("runtime image source labels differ")
	}
	binary := "app/server"
	if component == "web" {
		binary = "usr/bin/caddy"
	}
	raw := image.Files[binary]
	if len(raw) == 0 {
		return result, fmt.Errorf("runtime executable missing")
	}
	build, err := buildinfo.Read(bytes.NewReader(raw))
	if err != nil || !goVersionRE.MatchString(build.GoVersion) {
		return result, fmt.Errorf("runtime executable Go version unavailable")
	}
	result.GoVersion = build.GoVersion
	result.Binary = fact(binary, raw)
	return result, nil
}

func canonicalConfig(raw []byte) ([]byte, error) {
	var value map[string]any
	if err := contract.Decode(raw, MetadataLimit, &value); err != nil {
		return nil, err
	}
	// Docker inspection materializes empty defaults that an inherited OCI
	// config may omit. Compare their runtime meaning while retaining every
	// nondefault value and all nested labels/env/healthcheck configuration.
	for key, field := range value {
		empty := field == nil
		switch v := field.(type) {
		case string:
			empty = v == ""
		case bool:
			empty = !v
		case []any:
			empty = len(v) == 0
		case map[string]any:
			empty = len(v) == 0
		}
		if empty {
			delete(value, key)
		}
	}
	return contract.JSON(value)
}

const sourcePackageScript = `set -e
mkdir -p /work/recipe /work/user/.abuild /work/distfiles
cp -a /input/. /work/recipe/
cd /work/recipe
. ./APKBUILD
printf '%s\0%s\0%s\0%s\0' "$pkgname" "$pkgver-r$pkgrel" "${subpackages:-}" "${sha512sums:-}" > /output/recipe-metadata
abuild -C /work/recipe -s /work/distfiles -P /output fetch srcpkg
`

func collectAPKOriginal(ctx context.Context, p APK, helper, mirror string, recipe map[string][]byte, execute func(context.Context, time.Duration, ...string) ([]byte, error)) ([]byte, []string, map[string]string, error) {
	if !regexp.MustCompile(`^https://distfiles\.alpinelinux\.org/distfiles/v[1-9][0-9]*\.[0-9]+$`).MatchString(mirror) || !digestRE.MatchString(helper) {
		return nil, nil, nil, fmt.Errorf("invalid pinned runtime helper or source mirror")
	}
	private, err := os.MkdirTemp("", "psst-apk-source-")
	if err != nil {
		return nil, nil, nil, err
	}
	defer os.RemoveAll(private)
	input, output := filepath.Join(private, "recipe"), filepath.Join(private, "output")
	if err = os.Mkdir(input, 0700); err != nil {
		return nil, nil, nil, err
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return nil, nil, nil, err
	}
	for name, body := range recipe {
		local := filepath.Join(input, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(local), 0700); err != nil {
			return nil, nil, nil, err
		}
		if err = os.WriteFile(local, body, 0600); err != nil {
			return nil, nil, nil, err
		}
	}
	name := fmt.Sprintf("psst-apk-source-%d-%d", os.Getpid(), time.Now().UnixNano())
	uid, gid := os.Getuid(), os.Getgid()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		execute(cleanup, 30*time.Second, "docker", "rm", "-f", name)
	}()
	_, err = execute(ctx, 15*time.Minute, "docker", "run", "--rm", "--name", name, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", fmt.Sprintf("%d:%d", uid, gid), "--pids-limit", "128", "--memory", "1g", "--cpus", "2", "--tmpfs", fmt.Sprintf("/work:rw,size=2g,uid=%d,gid=%d,mode=0700", uid, gid), "--tmpfs", fmt.Sprintf("/tmp:rw,size=128m,uid=%d,gid=%d,mode=1777", uid, gid), "--env", "HOME=/work/user", "--env", "ABUILD_USERDIR=/work/user/.abuild", "--env", "DISTFILES_MIRROR="+mirror, "--mount", "type=bind,src="+input+",dst=/input,readonly", "--mount", "type=bind,src="+output+",dst=/output", "--entrypoint", "/bin/sh", helper, "-c", sourcePackageScript)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("source package acquisition failed for %s %s at %s: %w", p.Origin, p.Version, p.AportsCommit, err)
	}
	metadata, err := readRegular(filepath.Join(output, "recipe-metadata"), 8<<20)
	if err != nil {
		return nil, nil, nil, err
	}
	fields := strings.Split(string(metadata), "\x00")
	if len(fields) != 5 || fields[4] != "" || fields[0] != p.Origin || fields[1] != p.Version {
		return nil, nil, nil, fmt.Errorf("source recipe identity differs from retained package")
	}
	declared := map[string]bool{fields[0]: true}
	for _, field := range strings.Fields(fields[2]) {
		name := strings.SplitN(field, ":", 2)[0]
		if !apkNameRE.MatchString(name) {
			return nil, nil, nil, fmt.Errorf("unsafe declared source subpackage")
		}
		declared[name] = true
	}
	if !declared[p.Name] {
		return nil, nil, nil, fmt.Errorf("retained package not declared by source recipe")
	}
	var packages []string
	for name := range declared {
		packages = append(packages, name)
	}
	sort.Strings(packages)
	sums, err := sourceChecksums(fields[3])
	if err != nil {
		return nil, nil, nil, err
	}
	var archives []string
	err = filepath.WalkDir(output, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("linked runtime collector output")
		}
		relative, e := filepath.Rel(output, name)
		if e != nil {
			return e
		}
		if entry.Type().IsRegular() && strings.HasPrefix(filepath.ToSlash(relative), "src/") && strings.HasSuffix(name, ".tar.gz") {
			archives = append(archives, name)
		}
		return nil
	})
	if err != nil || len(archives) != 1 {
		return nil, nil, nil, fmt.Errorf("missing or ambiguous source package")
	}
	raw, err := readRegular(archives[0], OriginalLimit)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = verifySourcePackage(raw, sums, recipe); err != nil {
		return nil, nil, nil, err
	}
	return raw, packages, sums, nil
}
func sourceChecksums(text string) (map[string]string, error) {
	result := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || len(fields[0]) != 128 || strings.ToLower(fields[0]) != fields[0] || !safePath(fields[1]) || path.Base(fields[1]) != fields[1] {
			return nil, fmt.Errorf("unsupported original source checksum table")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, fmt.Errorf("invalid source SHA512 checksum")
		}
		if _, ok := result[fields[1]]; ok {
			return nil, fmt.Errorf("duplicate source checksum input")
		}
		result[fields[1]] = fields[0]
	}
	return result, nil
}
func verifySourcePackage(raw []byte, sums map[string]string, recipe map[string][]byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: (2 << 30) + 1}
	reader := tar.NewReader(limited)
	seen := map[string]bool{}
	basenames := map[string]bool{}
	checked := map[string]bool{}
	recipeChecked := false
	for count := 0; ; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safePath(name) || seen[name] || count >= 100000 || header.Size < 0 || header.Size > OriginalLimit {
			return fmt.Errorf("unsafe source package member")
		}
		seen[name] = true
		if header.Typeflag != tar.TypeReg {
			continue
		}
		base := path.Base(name)
		if basenames[base] {
			return fmt.Errorf("ambiguous source package checksum filename")
		}
		basenames[base] = true
		if expected, ok := sums[base]; ok {
			digest := sha512.New()
			if _, err = io.Copy(digest, reader); err != nil {
				return err
			}
			if hex.EncodeToString(digest.Sum(nil)) != expected {
				return fmt.Errorf("original source differs from exact APKBUILD SHA512")
			}
			checked[base] = true
		} else if name == "recipe/APKBUILD" {
			body, err := io.ReadAll(io.LimitReader(reader, (8<<20)+1))
			if err != nil || !bytes.Equal(body, recipe["APKBUILD"]) {
				return fmt.Errorf("source package recipe differs from retained original")
			}
			recipeChecked = true
		}
	}
	if _, err = io.Copy(io.Discard, limited); err != nil || limited.N <= 0 {
		return fmt.Errorf("source package expansion exceeds bounds")
	}
	if len(checked) != len(sums) || !recipeChecked {
		return fmt.Errorf("source package lacks original checksum inputs or recipe")
	}
	return nil
}
func runtimeOriginSource(ctx context.Context, p APK, raw []byte, repository string, recipe map[string][]byte, declared []string, sums map[string]string) (Source, map[string][]byte, error) {
	id := "apk-" + strings.TrimPrefix(hash([]byte(apkOrigin(p))), "sha256:")
	item := Source{ID: id, Ecosystem: "apk", Name: p.Origin, Version: p.Version, Origin: p.Origin, URL: "https://raw.githubusercontent.com/alpinelinux/aports/" + p.AportsCommit + "/" + repository + "/" + p.Origin + "/APKBUILD", Original: fact("apk/"+id+".tar.gz", raw), LicensePaths: []string{}, BuildInputPaths: []string{}, PreferredSourcePaths: []string{"recipe/APKBUILD"}, Associations: map[string]string{}, ObservedInputs: map[string]File{}, Recipe: &Recipe{Repository: repository, Commit: p.AportsCommit, Files: []File{}, DeclaredPackages: declared, SourceSHA512: sums}}
	legal, err := sourceLegal(ctx, raw, item.Original.Path)
	if err != nil {
		return item, nil, err
	}
	for name, digest := range legal.Fixtures {
		if p.Origin != "busybox" || fixtureHashes[name] != digest {
			return item, nil, fmt.Errorf("unreviewed malformed source fixture")
		}
	}
	files := map[string][]byte{item.Original.Path: raw}
	var names []string
	for name := range recipe {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := recipe[name]
		target := "recipes/" + id + "/" + name
		files[target] = body
		f := fact(target, body)
		item.Recipe.Files = append(item.Recipe.Files, f)
		item.BuildInputPaths = append(item.BuildInputPaths, target)
		item.ObservedInputs[target] = f
		if legalNameRE.MatchString(path.Base(name)) || bytes.Contains(bytes.ToLower(body), []byte("copyright")) {
			legal.Documents["recipe/"+name] = body
		}
	}
	paths, facts, documents := retainedDocuments("notices/"+id, legal.Documents)
	item.LicensePaths = paths
	item.NoticeFiles = facts
	for name, body := range documents {
		files[name] = body
	}
	return item, files, nil
}
