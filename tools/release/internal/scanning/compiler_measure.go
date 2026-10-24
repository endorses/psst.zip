package scanning

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
	"github.com/endorses/psst.zip/tools/release/internal/process"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

type CompilerOptions struct {
	Root, Output, Collection, Platform, Component, Archive, TestedConfig, GoImage, Cosign, SourceSHA256 string
	Source                                                                                              Source
	Advisories                                                                                          []string
	Execute                                                                                             func(context.Context, time.Duration, ...string) ([]byte, error)
	Fetch                                                                                               func(context.Context, string, int64) ([]byte, error)
}
type NativeIdentity struct {
	Source
	Platform string `json:"platform"`
}

func (n NativeIdentity) Validate() error {
	if err := (source.Context{Repository: n.Repository, Version: n.Version, Commit: n.Commit}).Validate(); err != nil {
		return err
	}
	if n.Platform != "linux/amd64" && n.Platform != "linux/arm64" {
		return fmt.Errorf("invalid native scanner platform")
	}
	return nil
}

type ImageIdentity struct {
	Archive  string `json:"archive_digest"`
	Manifest string `json:"manifest_digest"`
	Config   string `json:"config_digest"`
	Platform string `json:"platform"`
}
type Advisory struct {
	RawFile string `json:"raw_file"`
	SHA256  string `json:"sha256"`
	Origin  string `json:"origin"`
}
type CompilerBinary struct {
	SHA256      string           `json:"sha256"`
	Build       *debug.BuildInfo `json:"build_info"`
	BuildSHA256 string           `json:"build_info_sha256"`
}
type CompilerMeasurement struct {
	Schema       int                            `json:"schema_version"`
	Kind         string                         `json:"kind"`
	Source       NativeIdentity                 `json:"source"`
	Execution    string                         `json:"execution"`
	Component    string                         `json:"component"`
	Target       string                         `json:"executable_target"`
	Builder      Builder                        `json:"builder"`
	Image        ImageIdentity                  `json:"image"`
	Binary       CompilerBinary                 `json:"binary"`
	Graph        CompilerGraph                  `json:"source_graph"`
	Inputs       map[string]string              `json:"source_inputs"`
	Signatures   []source.SignatureVerification `json:"signature_verification"`
	Advisories   map[string]Advisory            `json:"advisories"`
	RawFiles     map[string]string              `json:"raw_files"`
	Status       string                         `json:"status"`
	Exit         int                            `json:"exit_code"`
	Completed    string                         `json:"completed_at"`
	Authorized   bool                           `json:"publication_authorized"`
	Dispositions bool                           `json:"finding_dispositions_authorized"`
}

func (o CompilerOptions) execute() func(context.Context, time.Duration, ...string) ([]byte, error) {
	if o.Execute != nil {
		return o.Execute
	}
	return (process.Runner{Dir: o.Root, Env: dockerEnvironment(), CleanEnv: true}).Output
}

func nativeBuilder(ctx context.Context, execute func(context.Context, time.Duration, ...string) ([]byte, error), image, platform string) (Builder, error) {
	var result Builder
	if !strings.HasPrefix(image, "docker.io/library/golang@") || !digestPattern.MatchString(strings.TrimPrefix(image, "docker.io/library/golang@")) {
		return result, fmt.Errorf("compiler builder must be an official immutable Go image")
	}
	raw, err := execute(ctx, 15*time.Second, "docker", "info", "--format", "{{json .Architecture}}")
	arch := strings.TrimPrefix(platform, "linux/")
	if err != nil || json.Unmarshal(raw, &result.Daemon) != nil || result.Daemon != arch && !(arch == "amd64" && result.Daemon == "x86_64") && !(arch == "arm64" && result.Daemon == "aarch64") {
		return result, fmt.Errorf("compiler daemon would emulate the native executable")
	}
	if _, err = execute(ctx, 3*time.Minute, "docker", "pull", "--platform", platform, image); err != nil {
		return result, err
	}
	raw, err = execute(ctx, 15*time.Second, "docker", "image", "inspect", image)
	if err != nil {
		return result, err
	}
	var images []struct {
		ID, Os, Architecture string
		RootFS               struct{ Layers []string }
	}
	if json.Unmarshal(raw, &images) != nil || len(images) != 1 || images[0].Os != "linux" || images[0].Architecture != arch || !digestPattern.MatchString(images[0].ID) || len(images[0].RootFS.Layers) == 0 {
		return result, fmt.Errorf("actual compiler builder configuration differs")
	}
	for _, diff := range images[0].RootFS.Layers {
		if !digestPattern.MatchString(diff) {
			return result, fmt.Errorf("invalid compiler builder layer identity")
		}
	}
	result.Reference, result.Config, result.Architecture, result.Layers = image, images[0].ID, arch, images[0].RootFS.Layers
	return result, nil
}

// MeasureCompiler binds one graph to the native tested OCI binary and the
// retained complete source offering. It makes no finding/publication decision.
func MeasureCompiler(ctx context.Context, o CompilerOptions) (CompilerMeasurement, error) {
	result := CompilerMeasurement{Schema: 2, Kind: "native-compiler-graph-measurement", Source: NativeIdentity{o.Source, o.Platform}, Execution: "native", Component: o.Component, Inputs: map[string]string{}, Signatures: []source.SignatureVerification{}, Advisories: map[string]Advisory{}, RawFiles: map[string]string{}}
	identity := source.Context{Repository: o.Source.Repository, Version: o.Source.Version, Commit: o.Source.Commit}
	if err := identity.Validate(); err != nil {
		return result, err
	}
	if runtime.GOOS != "linux" || o.Platform != "linux/"+runtime.GOARCH || o.Component != "backend" && o.Component != "web" || !digestPattern.MatchString(o.TestedConfig) || !digestPattern.MatchString(o.SourceSHA256) {
		return result, fmt.Errorf("compiler measurement requires exact native source/image inputs")
	}
	if _, err := os.Lstat(o.Output); !os.IsNotExist(err) {
		return result, fmt.Errorf("compiler output must be a new private directory")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	// The shared reader replays original sources/recipes/legal policy once; the
	// source offering itself is hash-bound before and after compiler execution.
	inventory, files, err := source.ReplayRuntimeRetained(ctx, source.RuntimeOptions{Root: o.Root, Collection: o.Collection, Platform: o.Platform, Source: identity})
	if err != nil {
		return result, err
	}
	asset, err := source.RuntimeAsset(o.Collection, identity, o.Platform)
	if err != nil || asset.SHA256 != o.SourceSHA256 {
		return result, fmt.Errorf("compiler source offering differs from exact native input")
	}
	result.Inputs["runtime_source_asset_sha256"] = asset.SHA256
	image, metadata, err := source.InspectOCI(o.Archive, oci.Options{Repository: o.Source.Repository, Version: o.Source.Version, Commit: o.Source.Commit, Component: o.Component, Platform: o.Platform, TestedConfig: o.TestedConfig})
	if err != nil {
		return result, err
	}
	result.Image = ImageIdentity{metadata.ArchiveDigest, metadata.ManifestDigest, metadata.ConfigDigest, metadata.Platform}
	var graph source.RuntimeGraph
	for _, g := range inventory.Runtime {
		if g.Component == o.Component {
			graph = g
		}
	}
	binary := image.Files[graph.Binary.Path]
	if graph.Component != o.Component || hash(binary) != graph.Binary.SHA256 || int64(len(binary)) != graph.Binary.Size {
		return result, fmt.Errorf("compiler final OCI executable differs from retained original")
	}
	build, err := buildinfo.Read(bytes.NewReader(binary))
	if err != nil {
		return result, fmt.Errorf("cannot read final executable compiler metadata")
	}
	policy := files["inputs/tools/go-runtime-sources.json"]
	if _, err = source.GoRuntimeSourcePin(policy, build.GoVersion); err != nil {
		return result, err
	}
	settings, err := buildSettings(build)
	if err != nil {
		return result, err
	}
	script, err := CompilerScript(o.Component, settings)
	if err != nil {
		return result, err
	}
	if o.Component == "backend" && build.GoVersion != CompilerVersion {
		return result, fmt.Errorf("backend executable differs from pinned application compiler")
	}
	private, err := os.MkdirTemp("", "psst-compiler-graph-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(private)
	buildRoot := filepath.Join(private, "build")
	if err = os.Mkdir(buildRoot, 0700); err != nil {
		return result, err
	}
	var vendorSum, vendorModules []byte
	if o.Component == "backend" {
		// Snapshot only backend: web/native/build caches cannot affect compilation.
		raw, e := (process.Runner{Dir: o.Root, Env: map[string]string{"PATH": os.Getenv("PATH")}, CleanEnv: true}).Output(ctx, 30*time.Second, "git", "archive", "--format=tar", o.Source.Commit, "backend")
		if e != nil {
			return result, e
		}
		result.Inputs["application_archive_sha256"] = hash(raw)
		if e = extractCompilerTar(bytes.NewReader(raw), buildRoot, 256<<20, o.Source.Commit); e != nil {
			return result, e
		}
		buildRoot = filepath.Join(buildRoot, "backend")
	} else {
		var caddy source.Source
		for _, item := range inventory.Sources {
			if item.Ecosystem == "caddy" {
				if caddy.ID != "" {
					return result, fmt.Errorf("ambiguous Caddy original")
				}
				caddy = item
			}
		}
		result.Signatures, err = source.AuthenticateCaddyForCompiler(ctx, caddy, files, binary, o.Platform, o.Cosign)
		if err != nil {
			return result, err
		}
		original := files[caddy.Original.Path]
		gz, e := gzip.NewReader(bytes.NewReader(original))
		if e != nil {
			return result, e
		}
		err = extractCompilerTar(gz, buildRoot, 2<<30, "")
		if err == nil {
			_, err = io.Copy(io.Discard, gz)
		}
		closeErr := gz.Close()
		if err != nil {
			return result, err
		}
		if closeErr != nil {
			return result, closeErr
		}
		vendorSum, err = bounded(filepath.Join(buildRoot, "go.sum"), scannerRawLimit)
		if err != nil {
			return result, err
		}
		vendorModules, err = bounded(filepath.Join(buildRoot, "vendor/modules.txt"), scannerRawLimit)
		if err != nil {
			return result, err
		}
		result.Inputs["authenticated_wrapper_sha256"] = hash(original)
		result.Inputs["wrapper_revision"] = caddy.Authentication["source_commit"]
		result.Inputs["vendor_modules_sha256"] = hash(vendorModules)
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		raw, e := bounded(filepath.Join(buildRoot, name), scannerRawLimit)
		if e != nil {
			return result, e
		}
		result.Inputs[strings.ReplaceAll(name, ".", "_")+"_sha256"] = hash(raw)
	}
	if err = os.Mkdir(o.Output, 0700); err != nil {
		return result, err
	}
	if err = os.WriteFile(filepath.Join(o.Output, "runtime-binary"), binary, 0600); err != nil {
		return result, err
	}
	defer os.Remove(filepath.Join(o.Output, "runtime-binary"))
	defer os.Remove(filepath.Join(o.Output, "rebuilt-backend"))
	execute := o.execute()
	result.Builder, err = nativeBuilder(ctx, execute, o.GoImage, o.Platform)
	if err != nil {
		return result, err
	}
	if err = runCompiler(ctx, execute, private, buildRoot, o.Output, result.Builder.Config, o.Platform, script); err != nil {
		return result, err
	}
	raw := map[string][]byte{}
	for _, name := range []string{"go-version.txt", "binary-build-info.json", "compiler-graph.json", "compiler-environment.json", "compiler-execution.complete"} {
		body, e := bounded(filepath.Join(o.Output, name), scannerRawLimit)
		if e != nil {
			return result, e
		}
		raw[name] = body
	}
	if o.Component == "backend" {
		body, e := bounded(filepath.Join(o.Output, "rebuilt-backend-sha256.txt"), scannerRawLimit)
		if e != nil {
			return result, e
		}
		raw["rebuilt-backend-sha256.txt"] = body
	}
	result.Graph, err = CompilerCorrespondence(raw, o.Component, o.Platform, binary, build, vendorSum, vendorModules)
	if err != nil {
		return result, err
	}
	for _, id := range o.Advisories {
		if !regexp.MustCompile(`^GO-[0-9]{4}-[0-9]+$`).MatchString(id) || result.Advisories[id].RawFile != "" {
			return result, fmt.Errorf("invalid/duplicate official advisory identifier")
		}
		origin := "https://vuln.go.dev/ID/" + id + ".json"
		fetch := o.Fetch
		if fetch == nil {
			fetch = advisoryFetch
		}
		body, e := fetch(ctx, origin, 1<<20)
		if e != nil {
			return result, e
		}
		var advisory map[string]any
		if contract.Decode(body, 1<<20, &advisory) != nil || advisory["id"] != id {
			return result, fmt.Errorf("official advisory identity differs")
		}
		name := id + ".json"
		if err = os.WriteFile(filepath.Join(o.Output, name), body, 0600); err != nil {
			return result, err
		}
		raw[name] = body
		result.Advisories[id] = Advisory{name, hash(body), origin}
	}
	for name, body := range raw {
		result.RawFiles[name] = hash(body)
	}
	// Do not let an inherited mounted file or nested directory hide in reports.
	entries, err := os.ReadDir(o.Output)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if entry.Name() == "runtime-binary" || entry.Name() == "rebuilt-backend" {
			continue
		}
		if _, ok := raw[entry.Name()]; !ok {
			return result, fmt.Errorf("unexpected compiler report output")
		}
	}
	finalAsset, err := source.RuntimeAsset(o.Collection, identity, o.Platform)
	if err != nil || finalAsset != asset {
		return result, fmt.Errorf("compiler source offering changed during measurement")
	}
	finalDigest, err := scanFileDigest(o.Archive, 2<<30)
	if err != nil || finalDigest != metadata.ArchiveDigest {
		return result, fmt.Errorf("compiler image input changed during measurement")
	}
	result.Binary = CompilerBinary{hash(binary), build, hash(legacyJSON(build))}
	result.Status = "complete"
	result.Completed = time.Now().UTC().Format(time.RFC3339Nano)
	body, err := contract.JSON(result)
	if err != nil {
		return result, err
	}
	if err = os.WriteFile(filepath.Join(o.Output, "compiler-graph-measurement.json"), body, 0600); err != nil {
		return result, err
	}
	return result, nil
}

func runCompiler(ctx context.Context, execute func(context.Context, time.Duration, ...string) ([]byte, error), private, build, output, config, platform, script string) error {
	cid := filepath.Join(private, "compiler.cid")
	defer func() {
		body, e := os.ReadFile(cid)
		if e == nil && regexp.MustCompile(`^[a-f0-9]{64}$`).Match(bytes.TrimSpace(body)) {
			cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			_, _ = execute(cleanup, 15*time.Second, "docker", "container", "rm", "--force", strings.TrimSpace(string(body)))
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if compilerReportsBounded(output) != nil {
					cancel()
					return
				}
			}
		}
	}()
	_, err := execute(ctx, 15*time.Minute, "docker", "run", "--rm", "--cidfile", cid, "--platform", platform, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--pids-limit", "256", "--memory", "3g", "--cpus", "2", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=2g", "--tmpfs", "/go:rw,nosuid,nodev,size=1g", "--env", "HOME=/tmp", "--env", "TMPDIR=/tmp", "--mount", "type=bind,src="+build+",dst=/build,readonly", "--mount", "type=bind,src="+output+",dst=/reports", "--entrypoint", "/bin/sh", config, "-ec", script)
	if err != nil {
		return err
	}
	return compilerReportsBounded(output)
}
func compilerReportsBounded(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) > 64 {
		return fmt.Errorf("compiler report inventory exceeds bounds")
	}
	var total int64
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 128<<20 {
			return fmt.Errorf("compiler report output exceeds bounds")
		}
		total += info.Size()
	}
	if total > 512<<20 {
		return fmt.Errorf("compiler reports exceed total byte bound")
	}
	return nil
}

// Extraction is a separate regular-source policy, not an OCI/tar policy. Tar
// paths, directories and physical bytes are bounded; links, devices and modes
// from upstream cannot affect the private build workspace.
func extractCompilerTar(input io.Reader, destination string, maximum int64, gitCommit string) error {
	limited := &io.LimitedReader{R: input, N: maximum + 1}
	reader := tar.NewReader(limited)
	seen := map[string]bool{}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	for count := 0; ; count++ {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if h.Typeflag == tar.TypeXGlobalHeader && gitCommit != "" && len(h.PAXRecords) == 1 && h.PAXRecords["comment"] == gitCommit {
			continue
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !validImport(name) || seen[name] || count >= 100000 || h.Size < 0 || h.Size > 512<<20 || h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
			return fmt.Errorf("unsafe compiler source member")
		}
		seen[name] = true
		if h.Typeflag == tar.TypeDir {
			if e = root.MkdirAll(filepath.FromSlash(name), 0700); e != nil {
				return e
			}
			continue
		}
		if e = root.MkdirAll(filepath.Dir(filepath.FromSlash(name)), 0700); e != nil {
			return e
		}
		file, e := root.OpenFile(filepath.FromSlash(name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, copyErr := io.CopyN(file, reader, h.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if _, err = io.Copy(io.Discard, limited); err != nil || limited.N <= 0 {
		return fmt.Errorf("compiler source archive expansion exceeds bounds")
	}
	return nil
}

func scanFileDigest(name string, limit int64) (string, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return "", fmt.Errorf("scanner file must be a bounded regular input")
	}
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", fmt.Errorf("scanner file changed before hashing")
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(file, limit+1))
	after, afterErr := file.Stat()
	if err != nil || afterErr != nil || n != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", fmt.Errorf("scanner file changed during hashing")
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func advisoryFetch(ctx context.Context, address string, limit int64) ([]byte, error) {
	if !regexp.MustCompile(`^https://vuln\.go\.dev/ID/GO-[0-9]{4}-[0-9]+\.json$`).MatchString(address) {
		return nil, fmt.Errorf("untrusted advisory origin")
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{ResponseHeaderTimeout: 15 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("official advisory redirects refused") }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid advisory request")
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("official advisory request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.ContentLength > limit {
		return nil, fmt.Errorf("official advisory response failed or exceeds bounds")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > limit {
		return nil, fmt.Errorf("official advisory body exceeds bounds")
	}
	return raw, nil
}
