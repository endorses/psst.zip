package clibuild

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/artifact"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/notices"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

var sourceScope = []string{"client", "backend", "web", "tools/release", "LICENSE", "README.md", "Makefile", "docs/cli.md", "docs/plans/cli-client.md", "docs/plans/cli-lan-sharing.md"}
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type PackageOptions struct {
	Root, Version, Revision, Output string
	AllowDirty                      bool
}
type backendModule struct {
	Path, Version, Dir, Sum, GoModSum string
	Main                              bool
	Replace                           any
}

func sourceAllowed(name string) bool {
	if strings.HasPrefix(name, "backend/") || filepath.Base(name) == "go.work" || filepath.Base(name) == "go.work.sum" || name == "client/psst" || name == "client/psst.exe" || strings.HasSuffix(name, ".pyc") {
		return false
	}
	if strings.HasPrefix(name, "client/internal/lanassets/site/") {
		return true
	}
	for _, part := range strings.Split(name, "/") {
		switch part {
		case "node_modules", ".svelte-kit", ".svelte-kit-lan", "__pycache__", "vendor", ".build":
			return false
		}
	}
	return true
}

func SourceFiles(ctx context.Context, root string, development bool) ([]string, error) {
	runner := process.Runner{Dir: root}
	commands := [][]string{append([]string{"git", "ls-files", "-z", "--"}, sourceScope...)}
	if development {
		commands = append(commands, append([]string{"git", "ls-files", "--others", "--exclude-standard", "-z", "--"}, sourceScope...))
	}
	selected := map[string]bool{}
	for _, command := range commands {
		raw, err := runner.Output(ctx, 10*time.Second, command...)
		if err != nil {
			return nil, err
		}
		for _, name := range strings.Split(string(raw), "\x00") {
			if name != "" && sourceAllowed(name) {
				selected[name] = true
			}
		}
	}
	paths, err := walkFiles(filepath.Join(root, "client/internal/lanassets/site"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		selected[filepath.ToSlash(rel)] = true
	}
	var result []string
	for name := range selected {
		if !artifact.ValidPath(name) {
			return nil, fmt.Errorf("unsafe source input")
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("source input must be a regular file")
		}
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func Package(ctx context.Context, opts PackageOptions) error {
	if !stableVersion.MatchString(opts.Version) || !commitPattern.MatchString(opts.Revision) {
		return fmt.Errorf("use stable X.Y.Z and full lowercase source commit")
	}
	git := process.Runner{Dir: opts.Root}
	client := process.Runner{Dir: filepath.Join(opts.Root, "client"), Env: map[string]string{"GOWORK": "off", "GOFLAGS": "-mod=readonly"}}
	read := func(args ...string) (string, error) {
		raw, err := git.Output(ctx, 10*time.Second, args...)
		return strings.TrimSpace(string(raw)), err
	}
	head, err := read("git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != opts.Revision {
		return fmt.Errorf("release revision must equal HEAD")
	}
	status, err := read(append([]string{"git", "status", "--porcelain", "--untracked-files=all", "--"}, sourceScope...)...)
	if err != nil {
		return err
	}
	dirty := status != ""
	if dirty && !opts.AllowDirty {
		return fmt.Errorf("CLI source is dirty; commit first or use development-only --allow-dirty")
	}
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return err
	}
	for _, dir := range []string{"client", "backend", "web", "tools/release"} {
		rel, err := filepath.Rel(filepath.Join(opts.Root, dir), output)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("release output must be outside source directories")
		}
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("release output must be empty")
	}
	epochRaw, err := read("git", "show", "-s", "--format=%ct", head)
	if err != nil {
		return err
	}
	epoch, err := strconv.ParseInt(epochRaw, 10, 64)
	if err != nil {
		return err
	}
	// Packaging always uses the pinned module; the checked-in workspace is only
	// a development convenience. Restore the caller environment on completion.
	previous, had := os.LookupEnv("GOWORK")
	if err := os.Setenv("GOWORK", "off"); err != nil {
		return err
	}
	defer func() {
		if had {
			_ = os.Setenv("GOWORK", previous)
		} else {
			_ = os.Unsetenv("GOWORK")
		}
	}()
	assets, err := Prepare(ctx, AssetsOptions{Root: opts.Root, ReleaseVersion: opts.Version})
	if err != nil {
		return err
	}
	raw, err := client.Output(ctx, 90*time.Second, "go", "list", "-m", "-json", notices.BackendModule)
	if err != nil {
		return err
	}
	var backend backendModule
	// Go command metadata has additional version-specific fields; the external
	// release contract is decoded strictly in the contract package instead.
	if err := json.Unmarshal(raw, &backend); err != nil {
		return err
	}
	if backend.Path != notices.BackendModule || backend.Main || backend.Replace != nil || backend.Version == "" || backend.Sum == "" || backend.Dir == "" {
		return fmt.Errorf("release requires an immutable pinned backend module")
	}
	temporary, err := os.MkdirTemp("", "psst-cli-package-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	noticeDir := filepath.Join(temporary, "notices")
	inventory, err := notices.ExportPrepared(opts.Root, noticeDir)
	if err != nil {
		return err
	}
	shared := map[string]artifact.File{"LICENSE": {Source: filepath.Join(opts.Root, "LICENSE"), Mode: 0o644}}
	paths, err := walkFiles(noticeDir)
	if err != nil {
		return err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(noticeDir, path)
		shared["licenses/"+filepath.ToSlash(rel)] = artifact.File{Source: path, Mode: 0o644}
	}
	install := filepath.Join(temporary, "INSTALL.txt")
	if err := os.WriteFile(install, []byte(fmt.Sprintf("psst.zip CLI %s\nSource revision: %s\n\nPlace psst (psst.exe on Windows) on PATH. Verify SHA256SUMS first.\nRun psst --help. LAN: psst serve send FILE --qr (no account required).\nHosted: psst auth login --server https://your-server --username your-user.\nPrivate keys stay on this client; pairing does not sync keys.\nSee docs/cli.md in the source archive. Cross-compilation is not a runtime pass.\n", opts.Version, head)), 0o644); err != nil {
		return err
	}
	var commands []any
	for _, target := range notices.Targets {
		system, arch, _ := strings.Cut(target, "/")
		name := "psst"
		if system == "windows" {
			name += ".exe"
		}
		binary := filepath.Join(temporary, system+"-"+arch+"-"+name)
		flags := "-s -w -X main.version=" + opts.Version + " -X main.revision=" + head
		args := []string{"go", "build", "-trimpath", "-buildvcs=false", "-ldflags", flags, "-o", binary, "./cmd/psst"}
		if _, err := client.With(map[string]string{"CGO_ENABLED": "0", "GOOS": system, "GOARCH": arch}).Output(ctx, 2*time.Minute, args...); err != nil {
			return err
		}
		normalized := append([]string(nil), args...)
		normalized[len(normalized)-2] = name
		commands = append(commands, map[string]any{"target": target, "command": normalized})
		members := map[string]artifact.File{}
		for path, file := range shared {
			members[path] = file
		}
		members[name] = artifact.File{Source: binary, Mode: 0o755}
		members["INSTALL.txt"] = artifact.File{Source: install, Mode: 0o644}
		prefix := "psst-" + opts.Version + "-" + system + "-" + arch
		extension := ".tar.gz"
		if system == "windows" {
			extension = ".zip"
		}
		if err := artifact.Archive(filepath.Join(output, prefix+extension), prefix, members, epoch); err != nil {
			return err
		}
	}
	source := filepath.Join(temporary, "source")
	paths, err = SourceFiles(ctx, opts.Root, opts.AllowDirty)
	if err != nil {
		return err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(opts.Root, path)
		if err := copyFile(path, filepath.Join(source, rel)); err != nil {
			return err
		}
	}
	if err := copyTree(backend.Dir, filepath.Join(source, "backend")); err != nil {
		return err
	}
	if _, err := client.Output(ctx, 90*time.Second, "go", "mod", "vendor", "-o", filepath.Join(source, "client/vendor")); err != nil {
		return err
	}
	commands = append(commands, map[string]any{"command": []string{"go", "mod", "vendor", "-o", "source/client/vendor"}})
	buildText := fmt.Sprintf("This archive includes CLI, immutable backend, browser preferred source/assets/licenses, development-tool source and vendored Go dependencies.\nInstall the Go version in client/go.mod. Offline build (no Node, Python or network):\n\ncd client\nCGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -mod=vendor -trimpath -buildvcs=false -ldflags '-s -w -X main.version=%s -X main.revision=%s' ./cmd/psst\n\nRebuild browser preferred source with the Node version in web/package.json:\ncd web && npm ci --ignore-scripts && npm run build:lan && cd ..\ntool=$(sh tools/release/build.sh)\n\"$tool\" assets\n\nCross-compilation is not a platform runtime pass.\n", opts.Version, head)
	if err := os.WriteFile(filepath.Join(source, "BUILD.txt"), []byte(buildText), 0o644); err != nil {
		return err
	}
	goVersion, err := client.Output(ctx, 10*time.Second, "go", "env", "GOVERSION")
	if err != nil {
		return err
	}
	sourceHashes := map[string]string{}
	paths, err = walkFiles(source)
	if err != nil {
		return err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(source, path)
		hash, err := artifact.HashFile(path)
		if err != nil {
			return err
		}
		sourceHashes[filepath.ToSlash(rel)] = hash
	}
	record := map[string]any{"schema_version": 1, "version": opts.Version, "tag": "client/v" + opts.Version, "revision": head, "development_only": opts.AllowDirty, "source_dirty": dirty, "source_date_epoch": epoch, "go_version": strings.TrimSpace(string(goVersion)), "cgo_enabled": false, "targets": notices.Targets, "commands": commands, "notice_graph_commands": inventory.Commands, "backend_module": map[string]string{"Path": backend.Path, "Version": backend.Version, "Sum": backend.Sum, "GoModSum": backend.GoModSum}, "embedded_browser": assets, "source_files": sourceHashes}
	data, err := contract.JSON(record)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "SOURCE.json"), data, 0o644); err != nil {
		return err
	}
	members := map[string]artifact.File{}
	paths, err = walkFiles(source)
	if err != nil {
		return err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(source, path)
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") {
			mode = 0o755
		}
		members[filepath.ToSlash(rel)] = artifact.File{Source: path, Mode: mode}
	}
	prefix := "psst-" + opts.Version + "-source"
	if err := artifact.Archive(filepath.Join(output, prefix+".tar.gz"), prefix, members, epoch); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "cli-release.json"), data, 0o644); err != nil {
		return err
	}
	entries, err = os.ReadDir(output)
	if err != nil {
		return err
	}
	var sums strings.Builder
	for _, entry := range entries {
		hash, err := artifact.HashFile(filepath.Join(output, entry.Name()))
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%s  %s\n", hash, entry.Name())
	}
	return os.WriteFile(filepath.Join(output, "SHA256SUMS"), []byte(sums.String()), 0o644)
}

func copyFile(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source copy requires regular file")
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o644)
}
