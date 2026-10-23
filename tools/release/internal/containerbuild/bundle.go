package containerbuild

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// BundleOptions selects a new v1 bundle. Readiness must be selected by the gated
// caller; a tracked updater alone does not establish publication authorization.
type BundleOptions struct {
	Output, Version, Profile, ExpectedCommit string
}

// Bundle records the newly created artifact, not a mutable published release.
type Bundle struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	SourceCommit string `json:"source_commit"`
	Size         int64  `json:"size"`
}

var licensePartRE = regexp.MustCompile(`^[a-zA-Z0-9_.@+-]+$`)

// AllowedBundlePath is the immutable v1 compatibility allowlist. Metadata is
// generated separately, never accepted from an operator's working tree.
func AllowedBundlePath(name string) bool {
	for _, required := range contract.DeploymentRequiredFiles {
		if name == required {
			return true
		}
	}
	if name == "deploy/update.py" {
		return true
	}
	parts := strings.Split(name, "/")
	if len(parts) < 4 || parts[0] != "backend" || parts[1] != "licenses" || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	switch parts[len(parts)-1] {
	case "LICENSE", "NOTICE", "COPYING", "COPYRIGHT", "SQLITE-LICENSE", "COPYRIGHT-MUSL", "LICENSE-GO", "LICENSE-MMAP-GO", "LICENSE-LOGO":
	default:
		return false
	}
	for _, part := range parts {
		if part == "." || part == ".." || !licensePartRE.MatchString(part) {
			return false
		}
	}
	return true
}

type boundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("deployment bundle exceeds its byte bound")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// BuildBundle reads committed, clean, regular allowlisted inputs and exclusively
// creates a deterministic v1 gzip/USTAR artifact. Both gzip bytes and the entire
// expanded tar, including headers/padding, are bounded to 16 MiB.
func (b Builder) BuildBundle(ctx context.Context, opts BundleOptions) (_ Bundle, err error) {
	if opts.Profile == "" {
		opts.Profile = "artifact-foundation"
	}
	if !contract.ReleaseVersion.MatchString(opts.Version) || opts.Output == "" || opts.Profile != "artifact-foundation" && opts.Profile != "deployment-ready" || opts.ExpectedCommit != "" && !commitRE.MatchString(opts.ExpectedCommit) {
		return Bundle{}, fmt.Errorf("invalid deployment bundle version, profile or source")
	}
	commit, err := b.commit(ctx, "HEAD")
	if err != nil || opts.ExpectedCommit != "" && commit != opts.ExpectedCommit {
		return Bundle{}, fmt.Errorf("bundle checkout differs from the selected source")
	}
	stamp, err := b.git(ctx, "show", "-s", "--format=%ct", commit)
	if err != nil {
		return Bundle{}, err
	}
	epoch, err := strconv.ParseInt(strings.TrimSpace(string(stamp)), 10, 64)
	if err != nil || epoch < 0 {
		return Bundle{}, fmt.Errorf("invalid committed bundle timestamp")
	}
	tree, err := b.git(ctx, "ls-tree", "-rz", "--full-tree", commit)
	if err != nil {
		return Bundle{}, err
	}
	root, err := os.OpenRoot(b.Root)
	if err != nil {
		return Bundle{}, err
	}
	defer root.Close()
	files := map[string][]byte{}
	var total int64
	for _, entry := range bytes.Split(tree, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		attributes, rawName, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok || !utf8.Valid(rawName) {
			return Bundle{}, fmt.Errorf("invalid committed release tree")
		}
		name := string(rawName)
		if !AllowedBundlePath(name) {
			continue
		}
		parts := strings.Fields(string(attributes))
		if len(parts) != 3 || parts[0] != "100644" && parts[0] != "100755" || parts[1] != "blob" || !commitRE.MatchString(parts[2]) {
			return Bundle{}, fmt.Errorf("non-regular committed release input")
		}
		info, err := root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() > contract.MaxDeploymentBundleBytes-total {
			return Bundle{}, fmt.Errorf("missing, unsafe or oversized release input")
		}
		content, err := b.git(ctx, "cat-file", "blob", parts[2])
		if err != nil || int64(len(content)) > contract.MaxDeploymentBundleBytes-total {
			return Bundle{}, fmt.Errorf("committed release payload exceeds bounds")
		}
		file, err := root.Open(name)
		if err != nil {
			return Bundle{}, fmt.Errorf("cannot open regular release input")
		}
		opened, statErr := file.Stat()
		working, readErr := io.ReadAll(io.LimitReader(file, contract.MaxDeploymentBundleBytes-total+1))
		closeErr := file.Close()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || readErr != nil || closeErr != nil || !bytes.Equal(content, working) {
			return Bundle{}, fmt.Errorf("unsafe or uncommitted release input")
		}
		files[name] = content
		total += int64(len(content))
	}
	for _, required := range contract.DeploymentRequiredFiles {
		if _, ok := files[required]; !ok {
			return Bundle{}, fmt.Errorf("missing tracked required deployment input")
		}
	}
	if opts.Profile == "deployment-ready" {
		if _, ok := files["deploy/update.py"]; !ok {
			return Bundle{}, fmt.Errorf("deployment-ready bundle lacks its tracked compatibility updater")
		}
	}
	candidates, err := b.git(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return Bundle{}, err
	}
	for _, rawName := range bytes.Split(candidates, []byte{0}) {
		if len(rawName) == 0 {
			continue
		}
		if !utf8.Valid(rawName) {
			return Bundle{}, fmt.Errorf("invalid release input filename")
		}
		name := string(rawName)
		if _, ok := files[name]; AllowedBundlePath(name) && !ok {
			return Bundle{}, fmt.Errorf("untracked or newly staged release input")
		}
	}
	if _, err := root.Lstat("deploy/update.py"); err == nil {
		if _, ok := files["deploy/update.py"]; !ok {
			return Bundle{}, fmt.Errorf("updater exists outside the tracked release inputs")
		}
	} else if !os.IsNotExist(err) {
		return Bundle{}, fmt.Errorf("cannot establish compatibility updater presence")
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, cached := range []bool{true, false} {
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only"}
		if cached {
			args = append(args, "--cached")
		}
		args = append(args, commit, "--")
		raw, err := b.git(ctx, append(args, names...)...)
		if err != nil || len(raw) != 0 {
			return Bundle{}, fmt.Errorf("staged or dirty committed release inputs")
		}
	}
	metadata, err := contract.JSON(map[string]any{"schema_version": 1, "version": opts.Version, "source_commit": commit, "payload_profile": opts.Profile})
	if err != nil {
		return Bundle{}, err
	}
	files["release-bundle.json"] = metadata
	names = append(names, "release-bundle.json")
	sort.Strings(names)
	var compressed bytes.Buffer
	zipLimit := &boundedWriter{writer: &compressed, remaining: contract.MaxDeploymentBundleBytes}
	zipped := gzip.NewWriter(zipLimit)
	zipped.Header.ModTime = time.Unix(epoch, 0).UTC()
	zipped.Header.OS = 255
	expandedLimit := &boundedWriter{writer: zipped, remaining: contract.MaxDeploymentBundleBytes}
	archive := tar.NewWriter(expandedLimit)
	for _, name := range names {
		content := files[name]
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), ModTime: time.Unix(epoch, 0).UTC(), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			_ = zipped.Close()
			return Bundle{}, fmt.Errorf("write bounded USTAR deployment header: %w", err)
		}
		if _, err := archive.Write(content); err != nil {
			_ = zipped.Close()
			return Bundle{}, err
		}
	}
	if err := archive.Close(); err != nil {
		_ = zipped.Close()
		return Bundle{}, err
	}
	if err := zipped.Close(); err != nil {
		return Bundle{}, err
	}
	if current, err := b.commit(ctx, "HEAD"); err != nil || current != commit {
		return Bundle{}, fmt.Errorf("bundle source HEAD changed during preparation")
	}
	if err := os.MkdirAll(opts.Output, 0o700); err != nil {
		return Bundle{}, err
	}
	name := "psst.zip-deployment-" + opts.Version + ".tar.gz"
	destination := filepath.Join(opts.Output, name)
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Bundle{}, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	_, writeErr := file.Write(compressed.Bytes())
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return Bundle{}, fmt.Errorf("cannot persist deployment bundle")
	}
	hash := sha256.Sum256(compressed.Bytes())
	checksum := hex.EncodeToString(hash[:])
	manifest := contract.ManifestV1{SchemaVersion: 1, Version: opts.Version, PayloadProfile: opts.Profile, Source: contract.SourceV1{Commit: commit}, Bundle: contract.BundleV1{Name: name, SHA256: checksum}}
	if _, err := contract.ReadBundleV1(destination, manifest); err != nil {
		return Bundle{}, fmt.Errorf("new bundle violates the existing v1 reader: %w", err)
	}
	return Bundle{Path: destination, SHA256: checksum, SourceCommit: commit, Size: int64(compressed.Len())}, nil
}
