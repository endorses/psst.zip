// Package scanning measures scanner inputs once without deciding finding policy.
package scanning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

const npmScript = `set -eu
export PATH=/usr/local/bin:/usr/bin:/bin
export npm_config_cache=/tmp/npm-cache npm_config_userconfig=/tmp/empty-npmrc npm_config_globalconfig=/tmp/global-npmrc
export npm_config_registry=https://registry.npmjs.org/ npm_config_ignore_scripts=true npm_config_audit=true
export npm_config_omit= npm_config_include=dev npm_config_workspaces=false
: > /tmp/empty-npmrc
: > /tmp/global-npmrc
node --version > /reports/node-version.txt
npm --version > /reports/npm-version.txt
cd /source/web
npm ls --package-lock-only --all --json > /reports/npm-lock-graph.json 2> /reports/npm-graph.stderr
set +e
npm audit --package-lock-only --ignore-scripts --json --registry=https://registry.npmjs.org/ > /reports/npm-audit.json 2> /reports/npm-audit.stderr
printf '%s\n' "$?" > /reports/npm-audit.exit
set -e
printf 'complete\n' > /reports/node-execution.complete
`

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var repositoryPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9._-]*$`)

var npmFiles = []string{"node-version.txt", "npm-version.txt", "npm-lock-graph.json", "npm-graph.stderr", "npm-audit.json", "npm-audit.stderr", "npm-audit.exit", "node-execution.complete", "execution.stderr"}

type Source struct {
	Repository string `json:"repository"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
}

type Builder struct {
	Reference    string   `json:"reference"`
	Config       string   `json:"config_digest"`
	Architecture string   `json:"architecture"`
	Layers       []string `json:"rootfs_layers"`
	Daemon       string   `json:"docker_engine_architecture"`
}

type NPMMeasurement struct {
	SchemaVersion int               `json:"schema_version"`
	Kind          string            `json:"kind"`
	Source        Source            `json:"source"`
	Execution     string            `json:"execution"`
	Platform      string            `json:"platform"`
	SourceInputs  map[string]string `json:"source_inputs"`
	Builder       Builder           `json:"builder"`
	Started       string            `json:"started_at"`
	Completed     string            `json:"completed_at"`
	RawFiles      map[string]string `json:"raw_files"`
	Authorized    bool              `json:"publication_authorized"`
}

func hash(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func dockerEnvironment() map[string]string {
	env := map[string]string{"PATH": os.Getenv("PATH")}
	for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "XDG_RUNTIME_DIR"} {
		if value, present := os.LookupEnv(name); present {
			env[name] = value
		}
	}
	return env
}

// ReadNPM authenticates retained raw bytes against the exact source lock. It
// does not suppress findings or substitute for the independent policy replay.
func ReadNPM(directory string, source Source, inputs map[string]string, image string) (NPMMeasurement, error) {
	var result NPMMeasurement
	data, err := bounded(filepath.Join(directory, "common-npm-audit.json"), 4<<20)
	if err != nil {
		return result, err
	}
	if err = contract.Decode(data, 4<<20, &result); err != nil {
		return result, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 11 || !bytes.Equal(bytes.TrimSpace(fields["publication_authorized"]), []byte("false")) {
		return result, errors.New("incomplete common npm measurement")
	}
	if result.SchemaVersion != 1 || result.Kind != "common-npm-lock-scanner-measurement" || result.Execution != "common-lock" || result.Source != source || result.Authorized || result.Platform != "linux/amd64" && result.Platform != "linux/arm64" || result.Builder.Reference != image || !digestPattern.MatchString(result.Builder.Config) || result.Builder.Architecture != strings.TrimPrefix(result.Platform, "linux/") || len(result.Builder.Layers) == 0 || len(result.SourceInputs) != 2 || len(result.RawFiles) != len(npmFiles) {
		return result, errors.New("common npm measurement identity differs")
	}
	arch := result.Builder.Architecture
	if result.Builder.Daemon != arch && !(arch == "amd64" && result.Builder.Daemon == "x86_64") && !(arch == "arm64" && result.Builder.Daemon == "aarch64") {
		return result, errors.New("common npm collector was not native")
	}
	for _, layer := range result.Builder.Layers {
		if !digestPattern.MatchString(layer) {
			return result, errors.New("invalid common npm collector layer identity")
		}
	}
	started, err := time.Parse(time.RFC3339Nano, result.Started)
	if err != nil {
		return result, errors.New("invalid common npm measurement time")
	}
	completed, err := time.Parse(time.RFC3339Nano, result.Completed)
	if err != nil || completed.Before(started) {
		return result, errors.New("invalid common npm completion time")
	}
	for _, name := range []string{"web/package.json", "web/package-lock.json"} {
		if !digestPattern.MatchString(inputs[name]) || result.SourceInputs[name] != inputs[name] {
			return result, errors.New("common npm measurement lock differs")
		}
	}
	for _, name := range npmFiles {
		data, err := bounded(filepath.Join(directory, name), 32<<20)
		if err != nil || result.RawFiles[name] != hash(data) {
			return result, errors.New("common npm raw report changed")
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(npmFiles)+1 {
		return result, errors.New("unexpected common npm measurement files")
	}
	return result, nil
}

func bounded(name string, limit int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("scanner input must be a bounded regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit || int64(len(data)) != info.Size() {
		return nil, errors.New("scanner input changed or exceeded bounds")
	}
	return data, nil
}

func MeasureNPM(ctx context.Context, root, output, image string, source Source) (NPMMeasurement, error) {
	result := NPMMeasurement{SchemaVersion: 1, Kind: "common-npm-lock-scanner-measurement", Source: source, Execution: "common-lock", Platform: "linux/" + runtime.GOARCH, SourceInputs: map[string]string{}, RawFiles: map[string]string{}}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" || !repositoryPattern.MatchString(source.Repository) || !versionPattern.MatchString(source.Version) || !commitPattern.MatchString(source.Commit) || !strings.HasPrefix(image, "docker.io/library/node@") || !digestPattern.MatchString(strings.TrimPrefix(image, "docker.io/library/node@")) {
		return result, errors.New("npm audit requires exact source and an official immutable native Node builder")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return result, errors.New("npm audit output must be a new directory")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	private, err := os.MkdirTemp("", "psst-npm-audit-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(private)
	git := process.Runner{Dir: root, CleanEnv: true, Env: map[string]string{"PATH": os.Getenv("PATH")}}
	sourceDir := filepath.Join(private, "source")
	if err := os.MkdirAll(filepath.Join(sourceDir, "web"), 0o700); err != nil {
		return result, err
	}
	for _, name := range []string{"web/package.json", "web/package-lock.json"} {
		data, err := git.Output(ctx, 15*time.Second, "git", "show", source.Commit+":"+name)
		if err != nil {
			return result, err
		}
		result.SourceInputs[name] = hash(data)
		if err := os.WriteFile(filepath.Join(sourceDir, filepath.FromSlash(name)), data, 0o600); err != nil {
			return result, err
		}
	}
	runner := process.Runner{CleanEnv: true, Env: dockerEnvironment()}
	daemon, err := runner.Output(ctx, 15*time.Second, "docker", "info", "--format", "{{json .Architecture}}")
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(daemon, &result.Builder.Daemon); err != nil || result.Builder.Daemon != runtime.GOARCH && !(runtime.GOARCH == "amd64" && result.Builder.Daemon == "x86_64") && !(runtime.GOARCH == "arm64" && result.Builder.Daemon == "aarch64") {
		return result, errors.New("npm audit daemon would emulate its collector architecture")
	}
	if _, err := runner.Output(ctx, 2*time.Minute, "docker", "pull", "--platform", result.Platform, image); err != nil {
		return result, err
	}
	raw, err := runner.Output(ctx, 15*time.Second, "docker", "image", "inspect", image)
	if err != nil {
		return result, err
	}
	var images []struct {
		ID, Os, Architecture string
		RootFS               struct{ Layers []string }
	}
	if err = json.Unmarshal(raw, &images); err != nil || len(images) != 1 || images[0].Os != "linux" || images[0].Architecture != runtime.GOARCH || !digestPattern.MatchString(images[0].ID) {
		return result, errors.New("npm audit builder identity differs")
	}
	result.Builder.Reference, result.Builder.Config, result.Builder.Architecture, result.Builder.Layers = image, images[0].ID, images[0].Architecture, images[0].RootFS.Layers
	if err := os.MkdirAll(output, 0o700); err != nil {
		return result, err
	}
	cidfile := filepath.Join(private, "collector.cid")
	defer func() {
		id, err := os.ReadFile(cidfile)
		if err == nil && regexp.MustCompile(`^[a-f0-9]{64}$`).Match(bytesTrim(id)) {
			cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			_, _ = runner.Output(cleanup, 15*time.Second, "docker", "container", "rm", "--force", string(bytesTrim(id)))
		}
	}()
	arguments := []string{"docker", "run", "--rm", "--cidfile", cidfile, "--platform", result.Platform, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--pids-limit", "256", "--memory", "1g", "--cpus", "2", "--tmpfs", "/tmp:rw,nosuid,nodev,size=512m", "--env", "HOME=/tmp", "--mount", "type=bind,src=" + sourceDir + ",dst=/source,readonly", "--mount", "type=bind,src=" + output + ",dst=/reports", "--entrypoint", "/bin/sh", images[0].ID, "-ec", npmScript}
	result.Started = time.Now().UTC().Format(time.RFC3339Nano)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := reportsBounded(output); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	measurement, err := runner.Result(ctx, 2*time.Minute, arguments...)
	cancel()
	<-watchDone
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(output, "execution.stderr"), measurement.Stderr, 0o600); err != nil {
		return result, err
	}
	if err := reportsBounded(output); err != nil {
		return result, err
	}
	for _, name := range npmFiles {
		data, err := bounded(filepath.Join(output, name), 32<<20)
		if err != nil {
			return result, err
		}
		result.RawFiles[name] = hash(data)
	}
	result.Completed = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := contract.JSON(result)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(output, "common-npm-audit.json"), data, 0o600); err != nil {
		return result, err
	}
	return ReadNPM(output, source, result.SourceInputs, image)
}

func bytesTrim(data []byte) []byte { return []byte(strings.TrimSpace(string(data))) }

func reportsBounded(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) > len(npmFiles) {
		return errors.New("npm report inventory exceeds bounds")
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
			return errors.New("npm report exceeds bounds")
		}
	}
	return nil
}

func Run(ctx context.Context, root string, args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "npm" {
		return errors.New("scan requires npm")
	}
	flags := flag.NewFlagSet("scan npm", flag.ContinueOnError)
	var source Source
	flags.StringVar(&source.Repository, "repository", "", "source repository")
	flags.StringVar(&source.Version, "version", "", "exact release version")
	flags.StringVar(&source.Commit, "commit", "", "exact source commit")
	image := flags.String("node-image", "", "official immutable Node image")
	directory := flags.String("output", "", "new private output directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *directory == "" {
		return errors.New("scan npm requires source identity, --node-image and --output")
	}
	absolute, err := filepath.Abs(*directory)
	if err != nil {
		return err
	}
	if _, err = MeasureNPM(ctx, root, absolute, *image, source); err != nil {
		return err
	}
	fmt.Fprintln(output, "Retained one exact-lock npm audit; findings require independent release review.")
	return nil
}
