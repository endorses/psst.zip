package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

// NativeOptions identifies both tested final images and their retained originals.
// Capture is used by VerifyNative; Output is a new CollectNative directory.
// Execute is the bounded command seam; callers cannot supply successful facts.
type NativeOptions struct {
	Root, Platform, BuilderConfig, CommonCollection, RuntimeCollection, Output, Capture, Cosign string
	Smoke, RuntimeSourceSHA256, RuntimeReport                                                   string
	Source                                                                                      source.Context
	Archives, Configs                                                                           map[string]string
	Execute                                                                                     func(context.Context, time.Duration, ...string) ([]byte, error)
}

// NativeFacts retains observations and explicit unresolved approval boundaries.
// The native platform is part of Source, matching the existing measurement key.
type NativeFacts struct {
	runtimeReplay                     map[string]any
	SchemaVersion                     int               `json:"schema_version"`
	Kind                              string            `json:"kind"`
	Source                            map[string]string `json:"source"`
	BuilderConfig                     string            `json:"builder_config"`
	TestedWebConfig                   string            `json:"tested_web_config"`
	Image                             map[string]any    `json:"image"`
	Verification                      FinalFacts        `json:"verification"`
	BrowserInputsSHA256               string            `json:"browser_inputs_sha256"`
	BuilderObservedInputsVerified     bool              `json:"builder_observed_inputs_verified"`
	GitSourceBindingVerified          bool              `json:"git_source_binding_verified"`
	NPMMemberIntegrityVerified        bool              `json:"npm_member_integrity_verified"`
	OCIImageVerified                  bool              `json:"oci_image_verified"`
	PreferredSourceComplete           bool              `json:"preferred_source_complete"`
	PublicationAuthorized             bool              `json:"publication_authorized"`
	DistributionAuthorized            bool              `json:"distribution_authorized"`
	BrowserModuleClosureVerified      bool              `json:"browser_module_closure_verified"`
	SourceReproductionVerified        bool              `json:"source_reproduction_verified"`
	MeasurementAuthenticationRequired bool              `json:"measurement_authentication_required"`
}

var containerIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (o NativeOptions) checked() error {
	if err := o.Source.Validate(); err != nil {
		return err
	}
	if o.Root == "" || (o.Platform != "linux/amd64" && o.Platform != "linux/arm64") || !digestPattern.MatchString(o.BuilderConfig) || o.CommonCollection == "" || o.RuntimeCollection == "" || o.Cosign == "" || len(o.Archives) != 2 || len(o.Configs) != 2 || !digestPattern.MatchString(o.Configs["backend"]) || !digestPattern.MatchString(o.Configs["web"]) || o.Configs["backend"] == o.Configs["web"] || o.Archives["backend"] == "" || o.Archives["web"] == "" || o.Archives["backend"] == o.Archives["web"] {
		return fmt.Errorf("native browser verification requires exact source, platform, builder, common/runtime originals and final image pair")
	}
	if o.Smoke != "" || o.RuntimeSourceSHA256 != "" || o.RuntimeReport != "" {
		if o.Smoke == "" || !digestPattern.MatchString(o.RuntimeSourceSHA256) || o.RuntimeReport == "" {
			return fmt.Errorf("combined native runtime replay requires smoke, source digest and new report path")
		}
	}
	return nil
}

func (o NativeOptions) execute() func(context.Context, time.Duration, ...string) ([]byte, error) {
	if o.Execute != nil {
		return o.Execute
	}
	return o.runner().Output
}

func (o NativeOptions) runner() process.Runner {
	environment := map[string]string{}
	for _, name := range []string{"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "XDG_RUNTIME_DIR"} {
		if value, found := os.LookupEnv(name); found {
			environment[name] = value
		}
	}
	return process.Runner{Dir: o.Root, CleanEnv: true, Env: environment}
}

type captureWriter struct {
	io.Writer
	remaining int64
}

func (w *captureWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, fmt.Errorf("browser capture stream exceeds bounds")
	}
	n, err := w.Writer.Write(data)
	w.remaining -= int64(n)
	return n, err
}

func streamCapture(ctx context.Context, o NativeOptions, destination, cid string) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	sink := &captureWriter{Writer: file, remaining: maximumTotal + (64 << 20)}
	args := []string{"docker", "exec", cid, "cat", "/tmp/psst-browser-builder.tar"}
	if o.Execute != nil {
		var raw []byte
		raw, err = o.Execute(ctx, time.Minute, args...)
		if err == nil {
			_, err = sink.Write(raw)
		}
	} else {
		err = o.runner().RunTo(ctx, time.Minute, sink, args...)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(destination)
		return fmt.Errorf("cannot retain bounded browser archive stream: %w", err)
	}
	return nil
}

func browserCommand(ctx context.Context, execute func(context.Context, time.Duration, ...string) ([]byte, error), timeout time.Duration, args ...string) ([]byte, error) {
	raw, err := execute(ctx, timeout, args...)
	if err != nil {
		return nil, fmt.Errorf("browser container operation %s failed: %w", args[1], err)
	}
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("browser container control output exceeds bounds")
	}
	return raw, nil
}

// CaptureNative runs only the exact committed selective capture script in a
// preloaded immutable native builder. It mounts no host files or Docker socket.
// Cleanup uses the private CID file even after cancellation/uncertain creation.
func CaptureNative(ctx context.Context, o NativeOptions, git map[string][]byte) (_ map[string][]byte, err error) {
	if err := o.Source.Validate(); err != nil {
		return nil, err
	}
	if (o.Platform != "linux/amd64" && o.Platform != "linux/arm64") || !digestPattern.MatchString(o.BuilderConfig) {
		return nil, fmt.Errorf("browser capture requires an immutable native builder")
	}
	script := git["web/scripts/pack-browser-builder-inputs.mjs"]
	if len(script) == 0 || len(script) > 96<<10 {
		return nil, fmt.Errorf("missing bounded committed browser capture script")
	}
	execute := o.execute()
	raw, err := browserCommand(ctx, execute, 30*time.Second, "docker", "info", "--format", "{{.Architecture}}")
	if err != nil {
		return nil, err
	}
	arch := strings.TrimSpace(string(raw))
	if value := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[arch]; value != "" {
		arch = value
	}
	if o.Platform != "linux/"+arch {
		return nil, fmt.Errorf("browser capture forbids emulated Docker hosts")
	}
	raw, err = browserCommand(ctx, execute, 30*time.Second, "docker", "image", "inspect", o.BuilderConfig)
	if err != nil {
		return nil, err
	}
	var checked json.RawMessage
	if err := contract.Decode(raw, 1<<20, &checked); err != nil {
		return nil, err
	}
	var images []struct {
		ID           string `json:"Id"`
		OS           string `json:"Os"`
		Architecture string `json:"Architecture"`
	}
	if json.Unmarshal(checked, &images) != nil || len(images) != 1 || images[0].ID != o.BuilderConfig || images[0].OS != "linux" || images[0].Architecture != arch {
		return nil, fmt.Errorf("preloaded browser builder configuration/platform differs")
	}
	private, err := os.MkdirTemp("", "psst-browser-capture-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(private)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	name := "psst-browser-" + hex.EncodeToString(nonce[:])
	cidfile := filepath.Join(private, "container.id")
	defer func() {
		target := name
		if cid, e := readBrowserCID(cidfile); e == nil {
			target = cid
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if _, e := browserCommand(cleanup, execute, 30*time.Second, "docker", "rm", "--force", target); e != nil && err == nil {
			err = fmt.Errorf("cannot remove owned browser capture container")
		}
	}()
	// Keep the tmpfs mounted until archive streaming finishes. A stopped container loses
	// its tmpfs, even when the capture itself exited successfully.
	raw, err = browserCommand(ctx, execute, 30*time.Second, "docker", "create", "--name", name, "--cidfile", cidfile, "--pull=never", "--platform", o.Platform, "--network", "none", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=640m,mode=1777", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "65532:65532", "--pids-limit", "64", "--memory", "1g", "--cpus", "1", "--ulimit", "nofile=1024:1024", "--workdir", "/build", "--entrypoint", "node", o.BuilderConfig, "-e", "setInterval(() => {}, 1000)")
	if err != nil {
		return nil, err
	}
	cid, err := readBrowserCID(cidfile)
	if err != nil || strings.TrimSpace(string(raw)) != cid {
		return nil, fmt.Errorf("browser create did not retain its exact container ID")
	}
	if _, err := browserCommand(ctx, execute, 30*time.Second, "docker", "start", cid); err != nil {
		return nil, err
	}
	if _, err := browserCommand(ctx, execute, 3*time.Minute, "docker", "exec", cid, "node", "--input-type=module", "-e", string(script)); err != nil {
		return nil, err
	}
	raw, err = browserCommand(ctx, execute, 30*time.Second, "docker", "inspect", "--format", "{{.State.Running}}", cid)
	if err != nil || strings.TrimSpace(string(raw)) != "true" {
		return nil, fmt.Errorf("browser capture container stopped before archive retention")
	}
	capture := filepath.Join(private, "capture.tar")
	if err := streamCapture(ctx, o, capture, cid); err != nil {
		return nil, err
	}
	files, _, err := ReadCapture(capture)
	return files, err
}

func readBrowserCID(name string) (string, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 64 || info.Size() > 65 {
		return "", fmt.Errorf("missing regular browser CID file")
	}
	file, err := os.Open(name)
	if err != nil {
		return "", fmt.Errorf("cannot open browser CID file")
	}
	opened, statErr := file.Stat()
	raw, readErr := io.ReadAll(io.LimitReader(file, 66))
	closeErr := file.Close()
	cid := strings.TrimSpace(string(raw))
	if statErr != nil || !os.SameFile(info, opened) || readErr != nil || closeErr != nil || len(raw) > 65 || !containerIDPattern.MatchString(cid) {
		return "", fmt.Errorf("invalid browser CID file")
	}
	return cid, nil
}

// VerifyNativeFacts consumes already authenticated shared common/runtime inputs.
// Its caller binds BrowserInputsSHA256 to the retained WriteCapture/ReadCapture
// fact. No complete preferred-source or transformed-byte reproduction is claimed.
func VerifyNativeFacts(ctx context.Context, binding BindingOptions, runtime source.RuntimeVerification, platform, builderConfig string) (NativeFacts, error) {
	result := NativeFacts{}
	if (platform != "linux/amd64" && platform != "linux/arm64") || !digestPattern.MatchString(builderConfig) || runtime.Inventory.Source != binding.Source || runtime.Inventory.SchemaVersion != 2 || len(runtime.Images) != 2 || len(runtime.Files) != 2 || !digestPattern.MatchString(runtime.Images["web"].ConfigDigest) || !digestPattern.MatchString(runtime.Images["backend"].ConfigDigest) || runtime.Images["web"].ConfigDigest == runtime.Images["backend"].ConfigDigest || runtime.Images["web"].Platform != platform || runtime.Images["backend"].Platform != platform {
		return result, fmt.Errorf("shared native browser runtime/source pair differs")
	}
	if binding.GitFiles == nil {
		var err error
		binding.GitFiles, err = GitInputs(ctx, binding.Root, binding.Source.Commit)
		if err != nil {
			return result, err
		}
	}
	bound, err := BindCaptured(ctx, binding)
	if err != nil {
		return result, err
	}
	final, err := VerifyFinal(bound, binding.Captured, binding.GitFiles, runtime, platform)
	if err != nil {
		return result, err
	}
	result = NativeFacts{SchemaVersion: 2, Kind: "native-browser-input-measurement", Source: map[string]string{"repository": binding.Source.Repository, "version": binding.Source.Version, "commit": binding.Source.Commit, "platform": platform}, BuilderConfig: builderConfig, TestedWebConfig: runtime.Images["web"].ConfigDigest, Image: source.OCIMeasurement(runtime.Images["web"]), Verification: final, BuilderObservedInputsVerified: true, GitSourceBindingVerified: true, NPMMemberIntegrityVerified: true, OCIImageVerified: true, MeasurementAuthenticationRequired: true}
	return result, nil
}

func replayNative(ctx context.Context, o NativeOptions, captured map[string][]byte) (NativeFacts, error) {
	git, err := GitInputs(ctx, o.Root, o.Source.Commit)
	if err != nil {
		return NativeFacts{}, err
	}
	return replayNativeWithGit(ctx, o, captured, git)
}

// CollectNative captures, independently verifies and retains one native browser
// measurement. Partial new output is evidence, never a completed result.
func CollectNative(ctx context.Context, o NativeOptions) (NativeFacts, error) {
	if err := o.checked(); err != nil {
		return NativeFacts{}, err
	}
	if o.Output == "" {
		return NativeFacts{}, fmt.Errorf("native browser collection requires new output directory")
	}
	if _, err := os.Lstat(o.Output); !os.IsNotExist(err) {
		return NativeFacts{}, fmt.Errorf("browser output already exists or cannot be inspected")
	}
	git, err := GitInputs(ctx, o.Root, o.Source.Commit)
	if err != nil {
		return NativeFacts{}, err
	}
	captured, err := CaptureNative(ctx, o, git)
	if err != nil {
		return NativeFacts{}, err
	}
	result, err := replayNativeWithGit(ctx, o, captured, git)
	if err != nil {
		return result, err
	}
	if err := os.Mkdir(o.Output, 0700); err != nil {
		return result, err
	}
	fact, err := WriteCapture(filepath.Join(o.Output, "browser-inputs.tar"), captured)
	if err != nil {
		return result, err
	}
	result.BrowserInputsSHA256 = fact.SHA256
	if o.RuntimeReport != "" {
		if err := writeNativeReport(o.RuntimeReport, result.runtimeReplay); err != nil {
			return result, err
		}
	}
	if err := writeNativeReport(filepath.Join(o.Output, "browser-verification.json"), result); err != nil {
		return result, err
	}
	return result, nil
}

func replayNativeWithGit(ctx context.Context, o NativeOptions, captured, git map[string][]byte) (NativeFacts, error) {
	common, files, err := source.ReadCommon(ctx, source.CommonOptions{Root: o.Root, Collection: o.CommonCollection, Source: o.Source})
	if err != nil {
		return NativeFacts{}, err
	}
	runtime, err := source.VerifyRuntimeImages(ctx, source.RuntimeOptions{Root: o.Root, Collection: o.RuntimeCollection, Platform: o.Platform, Source: o.Source, Cosign: o.Cosign, Execute: o.Execute}, o.Archives, o.Configs)
	if err != nil {
		return NativeFacts{}, err
	}
	result, err := VerifyNativeFacts(ctx, BindingOptions{Root: o.Root, Source: o.Source, Captured: captured, Common: common, CommonFiles: files, GitFiles: git}, runtime, o.Platform, o.BuilderConfig)
	if err != nil {
		return result, err
	}
	if o.Smoke != "" {
		if runtime.Asset.SHA256 != o.RuntimeSourceSHA256 {
			return result, fmt.Errorf("combined runtime source differs from external native binding")
		}
		smoke, err := readNativeSmoke(o.Smoke)
		if err != nil {
			return result, err
		}
		result.runtimeReplay, err = source.RuntimeReplayReport(source.RuntimeOptions{Source: o.Source, Platform: o.Platform, BackendConfig: o.Configs["backend"], WebConfig: o.Configs["web"]}, runtime, smoke)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// VerifyNative replays a regular retained capture and both exact OCI archives.
func VerifyNative(ctx context.Context, o NativeOptions) (NativeFacts, error) {
	if err := o.checked(); err != nil {
		return NativeFacts{}, err
	}
	captured, fact, err := ReadCapture(o.Capture)
	if err != nil {
		return NativeFacts{}, err
	}
	result, err := replayNative(ctx, o, captured)
	if err != nil {
		return result, err
	}
	result.BrowserInputsSHA256 = fact.SHA256
	if o.RuntimeReport != "" {
		if err := writeNativeReport(o.RuntimeReport, result.runtimeReplay); err != nil {
			return result, err
		}
	}
	return result, nil
}

func writeNativeReport(name string, result any) (err error) {
	raw, err := contract.JSON(result)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("cannot retain native browser verification")
	}
	return nil
}

func readNativeSmoke(name string) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > source.MetadataLimit {
		return nil, fmt.Errorf("native smoke must be a bounded regular observation")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("native smoke changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, source.MetadataLimit+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || int64(len(raw)) != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, fmt.Errorf("native smoke changed while reading")
	}
	return raw, nil
}
