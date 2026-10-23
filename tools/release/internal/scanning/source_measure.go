package scanning

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

const goSourceScript = `set -eu
export PATH=/tmp/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export GOTOOLCHAIN=local GOPATH=/tmp/go GOMODCACHE=/tmp/modules GOCACHE=/tmp/build GOBIN=/tmp/bin
export GOWORK=off GOFLAGS=-mod=readonly GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org
export GOPRIVATE= GONOPROXY= GONOSUMDB= GOVULNDB=https://vuln.go.dev CGO_ENABLED=0 GOEXPERIMENT=
cd /tmp
go version > /reports/go-version.txt
go env -json GOOS GOARCH CGO_ENABLED GOAMD64 GOARM64 GOFLAGS GOTOOLCHAIN GOSUMDB GOPROXY > /reports/go-env.json
go mod download -json golang.org/x/vuln@` + GovulnVersion + ` > /reports/scanner-module.json
grep -F '"Sum": "` + GovulnSum + `"' /reports/scanner-module.json >/dev/null
go install golang.org/x/vuln/cmd/govulncheck@` + GovulnVersion + `
sha256sum /tmp/bin/govulncheck > /reports/scanner-binary-sha256.txt
go version -m -json /tmp/bin/govulncheck > /reports/scanner-build.json
cd /build/backend
go list -json ./... > /reports/go-roots.json
go list -deps -json ./... > /reports/go-all-graph.json
go list -deps -json ./cmd/server > /reports/go-server-graph.json
go list -m -json all > /reports/go-modules.json
set +e
govulncheck -db https://vuln.go.dev -json ./... > /reports/govulncheck.json 2> /reports/govulncheck.stderr
scan_status=$?
printf '%s\n' "$scan_status" > /reports/govulncheck.exit
govulncheck -mode convert -show verbose < /reports/govulncheck.json > /reports/govulncheck.txt 2> /reports/govulncheck-convert.stderr
printf '%s\n' "$?" > /reports/govulncheck-convert.exit
set -e
printf 'complete\n' > /reports/go-execution.complete
`

type SourceOptions struct {
	Root, Output, Platform, GoImage, NodeImage, CommonNPM string
	Source                                                Source
	Execute                                               func(context.Context, time.Duration, ...string) ([]byte, error)
}

func (o SourceOptions) execute() func(context.Context, time.Duration, ...string) ([]byte, error) {
	if o.Execute != nil {
		return o.Execute
	}
	return (process.Runner{Dir: o.Root, CleanEnv: true, Env: dockerEnvironment()}).Output
}
func sourceInputFiles(directory string) (map[string]string, error) {
	result := map[string]string{}
	var total int64
	for _, prefix := range []string{"backend", "web"} {
		err := filepath.WalkDir(filepath.Join(directory, prefix), func(name string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(directory, name)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			raw, err := bounded(name, source.ApplicationLimit)
			if err != nil {
				return err
			}
			total += int64(len(raw))
			if total > source.ApplicationLimit || len(result) > 50000 {
				return fmt.Errorf("source snapshot input inventory exceeds bounds")
			}
			result[relative] = hash(raw)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"backend/go.mod", "backend/go.sum", "web/package.json", "web/package-lock.json"} {
		if !digestPattern.MatchString(result[name]) {
			return nil, fmt.Errorf("committed source dependency input missing")
		}
	}
	return result, nil
}
func sourceReadDirectory(directory string) (map[string][]byte, error) {
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) > 1024 {
		return nil, fmt.Errorf("scanner raw directory invalid or oversized")
	}
	result := map[string][]byte{}
	var total int64
	for _, entry := range entries {
		if !rawFilename(entry.Name()) {
			return nil, fmt.Errorf("unsafe scanner raw name")
		}
		raw, err := bounded(filepath.Join(directory, entry.Name()), scannerRawLimit)
		if err != nil {
			return nil, err
		}
		total += int64(len(raw))
		if total > 512<<20 {
			return nil, fmt.Errorf("source reports exceed total byte bound")
		}
		result[entry.Name()] = raw
	}
	return result, nil
}
func sourceWrite(name string, raw []byte) (err error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(name)
		}
	}()
	if _, err = file.Write(raw); err != nil {
		return err
	}
	return file.Sync()
}
func sourceSnapshot(ctx context.Context, o SourceOptions, directory string) (string, error) {
	var captured bytes.Buffer
	err := (process.Runner{Dir: o.Root, CleanEnv: true, Env: map[string]string{"PATH": os.Getenv("PATH")}}).RunTo(ctx, 60*time.Second, &sourceCapture{Writer: &captured, remaining: source.ApplicationLimit}, "git", "archive", o.Source.Commit)
	if err != nil {
		return "", err
	}
	raw := captured.Bytes()
	if len(raw) == 0 {
		return "", fmt.Errorf("source Git snapshot is empty")
	}
	if err = extractCompilerTar(bytes.NewReader(raw), directory, source.ApplicationLimit, o.Source.Commit); err != nil {
		return "", err
	}
	return hash(raw), nil
}

// MeasureSources runs the actual native Go source scanner and maps the retained
// common npm audit into the same observation. It never reruns npm per platform.
func MeasureSources(ctx context.Context, o SourceOptions) (SourceMeasurement, error) {
	identity := NativeIdentity{o.Source, o.Platform}
	result := SourceMeasurement{Schema: 2, Kind: "native-source-scanner-measurement", Source: identity, Execution: "native-go-common-lock", Pending: true, Review: true, Scans: []map[string]any{}}
	if err := identity.Validate(); err != nil {
		return result, err
	}
	if runtime.GOOS != "linux" || o.Platform != "linux/"+runtime.GOARCH || o.CommonNPM == "" || !strings.HasPrefix(o.NodeImage, "docker.io/library/node@") || !digestPattern.MatchString(strings.TrimPrefix(o.NodeImage, "docker.io/library/node@")) {
		return result, fmt.Errorf("source scan requires native Go execution and one common npm audit")
	}
	if _, err := os.Lstat(o.Output); !os.IsNotExist(err) {
		return result, fmt.Errorf("source scan output must be a new directory")
	}
	private, err := os.MkdirTemp("", "psst-source-scans-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(private)
	sourceRoot := filepath.Join(private, "source")
	if err = os.Mkdir(sourceRoot, 0700); err != nil {
		return result, err
	}
	result.ArchiveSHA256, err = sourceSnapshot(ctx, o, sourceRoot)
	if err != nil {
		return result, err
	}
	result.Inputs, err = sourceInputFiles(sourceRoot)
	if err != nil {
		return result, err
	}
	execute := o.execute()
	builder, err := nativeBuilder(ctx, execute, o.GoImage, o.Platform)
	if err != nil {
		return result, err
	}
	shared, err := sourceReadDirectory(o.CommonNPM)
	if err != nil {
		return result, err
	}
	common, err := sourceCommon(shared, o.Source, result.Inputs, o.NodeImage)
	if err != nil {
		return result, err
	}
	if err = os.MkdirAll(o.Output, 0700); err != nil {
		return result, err
	}
	for _, target := range []string{"backend-source", "web-source"} {
		directory := filepath.Join(o.Output, target)
		if err = os.Mkdir(directory, 0700); err != nil {
			return result, err
		}
		started := time.Now().UTC().Format(time.RFC3339Nano)
		var raw map[string][]byte
		actualBuilder := builder
		completed := ""
		if target == "backend-source" {
			if err = runCompiler(ctx, execute, private, sourceRoot, directory, builder.Config, o.Platform, goSourceScript); err != nil {
				return result, err
			}
			raw, err = sourceReadDirectory(directory)
			if err != nil {
				return result, err
			}
			completed = time.Now().UTC().Format(time.RFC3339Nano)
		} else {
			raw = shared
			for name, body := range raw {
				if err = sourceWrite(filepath.Join(directory, name), body); err != nil {
					return result, err
				}
			}
			actualBuilder = common.Builder
			started = common.Started
			completed = common.Completed
		}
		var scan map[string]any
		if target == "backend-source" {
			scan, err = AnalyzeGo(raw, o.Platform)
		} else {
			lock, readErr := bounded(filepath.Join(sourceRoot, "web/package-lock.json"), scannerRawLimit)
			if readErr != nil {
				return result, readErr
			}
			scan, err = AnalyzeNPM(raw, lock)
		}
		if err != nil {
			return result, err
		}
		inventory := map[string]string{}
		for name, body := range raw {
			inventory[name] = hash(body)
		}
		scan["target"] = target
		scan["subject"] = "git:" + o.Source.Repository + "@" + o.Source.Commit
		scan["started_at"] = started
		scan["scanned_at"] = completed
		scan["builder"] = actualBuilder
		scan["raw_files"] = inventory
		if target == "web-source" {
			scan["common_lock"] = map[string]string{"platform": common.Platform, "measurement_sha256": hash(raw["common-npm-audit.json"])}
		}
		result.Scans = append(result.Scans, scan)
	}
	after, err := sourceInputFiles(sourceRoot)
	if err != nil || !sourceSame(result.Inputs, after) {
		return result, fmt.Errorf("read-only source fixture changed during scans")
	}
	result.Completed = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := contract.JSON(result)
	if err != nil {
		return result, err
	}
	if err = sourceWrite(filepath.Join(o.Output, "source-scan-measurement.json"), raw); err != nil {
		return result, err
	}
	return ReadSourceMeasurement(raw, identity, o.GoImage, o.NodeImage)
}

type sourceCapture struct {
	io.Writer
	remaining int64
}

func (w *sourceCapture) Write(raw []byte) (int, error) {
	if int64(len(raw)) > w.remaining {
		return 0, fmt.Errorf("source snapshot exceeds bounds")
	}
	n, err := w.Writer.Write(raw)
	w.remaining -= int64(n)
	return n, err
}
