package scanning

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
	"github.com/endorses/psst.zip/tools/release/internal/process"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

type ImageOptions struct {
	Source                                                                          NativeIdentity
	Root, Component, Archive, TestedConfig, ToolArchive, ToolBundle, Cosign, Output string
	Execute                                                                         func(context.Context, time.Duration, map[string]string, ...string) ([]byte, error)
}
type imageMeasurementPolicy struct {
	Pin                                       trivyPin
	CosignVersion, CosignCommit, CosignDigest string
	Stage                                     func(string, string, oci.Options) (oci.Image, error)
}

func (o ImageOptions) execute() func(context.Context, time.Duration, map[string]string, ...string) ([]byte, error) {
	if o.Execute != nil {
		return o.Execute
	}
	return func(ctx context.Context, timeout time.Duration, env map[string]string, args ...string) ([]byte, error) {
		return (process.Runner{CleanEnv: true, Env: env, Dir: o.Root}).Output(ctx, timeout, args...)
	}
}
func scanEnvironment(private string) map[string]string {
	return map[string]string{"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": private, "TMPDIR": private, "XDG_CONFIG_HOME": filepath.Join(private, "config"), "XDG_CACHE_HOME": filepath.Join(private, "cache"), "SIGSTORE_NO_CACHE": "1"}
}

// MeasureImage authenticates fixed official scanner bytes before execution and
// owns a fresh scanner-downloaded database. Every finding is retained; exit zero
// only means measurement completed. AggregateImages derives finding policy.
func MeasureImage(ctx context.Context, o ImageOptions) (map[string]any, error) {
	version, commit, digest, err := source.CaddyVerifierPolicy(o.Source.Platform)
	if err != nil {
		return nil, err
	}
	return measureImage(ctx, o, imageMeasurementPolicy{trivyPins[o.Source.Platform], version, commit, digest, oci.StageArchive})
}
func measureImage(ctx context.Context, o ImageOptions, policy imageMeasurementPolicy) (map[string]any, error) {
	if err := (source.Context{Repository: o.Source.Repository, Version: o.Source.Version, Commit: o.Source.Commit}).Validate(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" || o.Source.Platform != "linux/"+runtime.GOARCH || compilerTarget(o.Component) == "" || !digestPattern.MatchString(o.TestedConfig) || policy.Pin.Archive == "" {
		return nil, fmt.Errorf("image scan requires exact native source/image inputs")
	}
	if o.Output == "" {
		return nil, fmt.Errorf("image scan requires a new private output directory")
	}
	if _, err := os.Lstat(o.Output); !os.IsNotExist(err) {
		return nil, fmt.Errorf("image scan output already exists")
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Minute)
	defer cancel()
	private, err := os.MkdirTemp("", "psst-image-scan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(private)
	if err = os.WriteFile(filepath.Join(private, "empty.yaml"), []byte("{}\n"), 0600); err != nil {
		return nil, err
	}
	executable, tool, err := authenticateImageTool(ctx, o, private, policy)
	if err != nil {
		return nil, err
	}
	env := scanEnvironment(private)
	execute := o.execute()
	cache := filepath.Join(private, "cache")
	db := filepath.Join(cache, "db")
	if err = os.MkdirAll(db, 0700); err != nil {
		return nil, err
	}
	startedDownload := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = execute(ctx, 330*time.Second, env, executable, "image", "--config", filepath.Join(private, "empty.yaml"), "--cache-dir", cache, "--db-repository", trivyDatabase, "--download-db-only", "--timeout", "5m"); err != nil {
		return nil, err
	}
	dbHash, err := scanFileDigest(filepath.Join(db, "trivy.db"), 2<<30)
	if err != nil {
		return nil, err
	}
	metadataRaw, err := bounded(filepath.Join(db, "metadata.json"), 1<<20)
	if err != nil {
		return nil, err
	}
	var database map[string]any
	if contract.Decode(metadataRaw, 1<<20, &database) != nil || !exactFields(database, "Version", "UpdatedAt", "NextUpdate", "DownloadedAt") || database["Version"] != float64(2) {
		return nil, fmt.Errorf("owned scanner database metadata differs")
	}
	for _, name := range []string{"UpdatedAt", "NextUpdate", "DownloadedAt"} {
		if _, err = scanTime(database[name]); err != nil {
			return nil, err
		}
	}
	database["sha256"] = dbHash
	database["metadata_sha256"] = hash(metadataRaw)
	database["acquisition"] = map[string]any{"kind": "owned-authenticated-trivy-download", "repository": trivyDatabase, "scanner_sha256": tool["sha256"], "database_sha256": dbHash, "metadata_sha256": hash(metadataRaw), "started_at": startedDownload, "completed_at": time.Now().UTC().Format(time.RFC3339Nano)}
	// The shared OCI parser checks the full encoded/uncompressed image graph
	// once and stages only runnable content. Trivy never receives the source tar.
	layout := filepath.Join(private, "layout")
	metadata, err := policy.Stage(o.Archive, layout, oci.Options{Repository: o.Source.Repository, Version: o.Source.Version, Commit: o.Source.Commit, Component: o.Component, Platform: o.Source.Platform, TestedConfig: o.TestedConfig})
	if err != nil {
		return nil, err
	}
	layoutBefore, err := scanLayout(layout)
	if err != nil {
		return nil, err
	}
	started := time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := execute(ctx, 660*time.Second, env, executable, "image", "--config", filepath.Join(private, "empty.yaml"), "--cache-dir", cache, "--input", layout, "--scanners", "vuln", "--format", "json", "--list-all-pkgs=true", "--severity", "UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL", "--ignore-unfixed=false", "--ignorefile=", "--ignore-policy=", "--offline-scan", "--skip-db-update", "--skip-java-db-update", "--skip-vex-repo-update", "--show-suppressed", "--exit-code", "0", "--timeout", "10m")
	if err != nil {
		return nil, err
	}
	report, findings, err := CheckedImageReport(raw, o.Source.Platform, o.Component, o.TestedConfig)
	if err != nil {
		return nil, err
	}
	layoutAfter, err := scanLayout(layout)
	if err != nil || !equalStringMaps(layoutBefore, layoutAfter) {
		return nil, fmt.Errorf("scanner changed its private OCI input")
	}
	archiveDigest, err := scanFileDigest(o.Archive, 2<<30)
	if err != nil || archiveDigest != metadata.ArchiveDigest {
		return nil, fmt.Errorf("image archive changed during scan")
	}
	for name, expected := range map[string]string{executable: text(tool["sha256"]), filepath.Join(db, "trivy.db"): dbHash, filepath.Join(db, "metadata.json"): hash(metadataRaw)} {
		actual, e := scanFileDigest(name, 2<<30)
		if e != nil || actual != expected {
			return nil, fmt.Errorf("scanner executable/database changed during scan")
		}
	}
	image := map[string]any{"platform": metadata.Platform, "manifest_digest": metadata.ManifestDigest, "manifest_size": metadata.ManifestSize, "manifest_media_type": metadata.ManifestMediaType, "config_digest": metadata.ConfigDigest, "archive_digest": metadata.ArchiveDigest, "blob_count": metadata.BlobCount}
	record := map[string]any{"schema_version": 1, "kind": "native-image-scanner-measurement", "source": o.Source, "execution": "native", "target": o.Component + "-" + runtime.GOARCH, "image": image, "scanner": tool, "database": database, "started_at": started, "scanned_at": report["CreatedAt"], "completed_at": time.Now().UTC().Format(time.RFC3339Nano), "status": "complete", "exit_code": 0, "raw_report_sha256": hash(raw), "findings": findings, "publication_authorized": false, "findings_review_required": true, "final_image_scanners_gate_pending": true}
	measurement, err := contract.JSON(record)
	if err != nil {
		return nil, err
	}
	// Reserve once after successful measurement; incomplete writes are never
	// reported as success, and existing outputs are never overwritten.
	if err = os.Mkdir(o.Output, 0700); err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(o.Output)
		}
	}()
	for _, entry := range []struct {
		name string
		body []byte
	}{{"scan.json", raw}, {"measurement.json", measurement}} {
		file, e := os.OpenFile(filepath.Join(o.Output, entry.name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
		if e != nil {
			return nil, e
		}
		_, e = file.Write(entry.body)
		if e == nil {
			e = file.Sync()
		}
		closeErr := file.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	complete = true
	return record, nil
}
func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, value := range a {
		if b[name] != value {
			return false
		}
	}
	return true
}
func scanLayout(directory string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(directory, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == directory {
			return nil
		}
		if entry.IsDir() {
			relative, _ := filepath.Rel(directory, name)
			files[filepath.ToSlash(relative)+"/"] = "directory"
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || len(files) > 264 {
			return fmt.Errorf("private OCI layout contains unexpected entries")
		}
		digest, err := scanFileDigest(name, 2<<30)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(directory, name)
		files[filepath.ToSlash(relative)] = digest
		return nil
	})
	return files, err
}

func authenticateImageTool(ctx context.Context, o ImageOptions, private string, policy imageMeasurementPolicy) (string, map[string]any, error) {
	verifier := filepath.Join(private, "cosign")
	verifierHash, err := snapshotScanFile(o.Cosign, verifier, 512<<20)
	if err != nil || verifierHash != policy.CosignDigest {
		return "", nil, fmt.Errorf("native scanner verifier differs from reviewed pin")
	}
	if err = os.Chmod(verifier, 0500); err != nil {
		return "", nil, err
	}
	env := scanEnvironment(private)
	execute := o.execute()
	raw, err := execute(ctx, 55*time.Second, env, verifier, "version", "--json")
	if err != nil {
		return "", nil, err
	}
	var version map[string]any
	if contract.Decode(raw, 1<<20, &version) != nil || version["gitVersion"] != policy.CosignVersion || version["gitCommit"] != policy.CosignCommit || version["gitTreeState"] != "clean" || version["platform"] != o.Source.Platform {
		return "", nil, fmt.Errorf("scanner verifier actual build identity differs")
	}
	archive, bundle := filepath.Join(private, "trivy.tar.gz"), filepath.Join(private, "trivy.sigstore.json")
	archiveHash, err := snapshotScanFile(o.ToolArchive, archive, 512<<20)
	if err != nil || archiveHash != "sha256:"+policy.Pin.Archive {
		return "", nil, fmt.Errorf("scanner official archive differs from reviewed pin")
	}
	bundleHash, err := snapshotScanFile(o.ToolBundle, bundle, 1<<20)
	if err != nil || bundleHash != "sha256:"+policy.Pin.Bundle {
		return "", nil, fmt.Errorf("scanner official signature bundle differs from reviewed pin")
	}
	_, err = execute(ctx, 55*time.Second, env, verifier, "--timeout", "45s", "verify-blob", "--certificate-identity", trivyIdentity, "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com", "--certificate-github-workflow-sha", TrivySource, "--certificate-github-workflow-ref", "refs/tags/v"+TrivyVersion, "--certificate-github-workflow-repository", "aquasecurity/trivy", "--certificate-github-workflow-trigger", "push", "--certificate-github-workflow-name", "Release", "--rekor-url", "https://rekor.sigstore.dev", "--insecure-ignore-tlog=false", "--insecure-ignore-sct=false", "--private-infrastructure=false", "--offline=false", "--new-bundle-format=true", "--bundle", bundle, archive)
	if err != nil {
		return "", nil, err
	}
	for name, wanted := range map[string]string{verifier: verifierHash, archive: archiveHash, bundle: bundleHash} {
		actual, e := scanFileDigest(name, 512<<20)
		if e != nil || actual != wanted {
			return "", nil, fmt.Errorf("scanner tool input changed during signature verification")
		}
	}
	executable := filepath.Join(private, "trivy")
	if err = extractScannerExecutable(archive, executable); err != nil {
		return "", nil, err
	}
	binaryHash, err := scanFileDigest(executable, 512<<20)
	if err != nil || policy.Pin.Binary != "" && binaryHash != "sha256:"+policy.Pin.Binary {
		return "", nil, fmt.Errorf("scanner executable differs from reviewed pin")
	}
	file, err := os.Open(executable)
	if err != nil {
		return "", nil, err
	}
	header := make([]byte, 20)
	_, err = io.ReadFull(file, header)
	file.Close()
	arch := uint16(62)
	if o.Source.Platform == "linux/arm64" {
		arch = 183
	}
	if err != nil || !bytes.Equal(header[:6], []byte{0x7f, 'E', 'L', 'F', 2, 1}) || binary.LittleEndian.Uint16(header[18:]) != arch {
		return "", nil, fmt.Errorf("scanner executable native architecture differs")
	}
	if err = os.Chmod(executable, 0500); err != nil {
		return "", nil, err
	}
	raw, err = execute(ctx, 55*time.Second, env, executable, "--version", "--format", "json", "--config", filepath.Join(private, "empty.yaml"))
	if err != nil {
		return "", nil, err
	}
	version = nil
	if contract.Decode(raw, 1<<20, &version) != nil || version["Version"] != TrivyVersion {
		return "", nil, fmt.Errorf("scanner actual release version differs")
	}
	tool := map[string]any{"name": "Trivy", "version": TrivyVersion, "source_commit": TrivySource, "platform": o.Source.Platform, "sha256": binaryHash, "archive_sha256": archiveHash, "bundle_sha256": bundleHash, "certificate_identity": trivyIdentity, "oidc_issuer": "https://token.actions.githubusercontent.com", "verification": "online-cosign-bundle-sct-rekor", "cosign": map[string]any{"version": policy.CosignVersion, "commit": policy.CosignCommit, "sha256": verifierHash}}
	return executable, tool, nil
}

func snapshotScanFile(input, output string, maximum int64) (string, error) {
	info, err := os.Lstat(input)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return "", fmt.Errorf("scanner source must be bounded regular input")
	}
	in, err := os.Open(input)
	if err != nil {
		return "", err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", fmt.Errorf("scanner input changed before snapshot")
	}
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, maximum+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || n != info.Size() {
		return "", fmt.Errorf("scanner input changed or exceeded snapshot bound")
	}
	digest, err := scanFileDigest(output, maximum)
	if err != nil {
		return "", err
	}
	original, err := scanFileDigest(input, maximum)
	if err != nil || original != digest {
		return "", fmt.Errorf("scanner source changed during snapshot")
	}
	return digest, nil
}

func extractScannerExecutable(archive, output string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: (512 << 20) + 1}
	reader := tar.NewReader(limited)
	seen := map[string]bool{}
	found := false
	for count := 0; ; count++ {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !validImport(name) || seen[name] || count >= 200 || h.Size < 0 || h.Size > 512<<20 || h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
			return fmt.Errorf("unsafe scanner release archive")
		}
		seen[name] = true
		if name != "trivy" {
			continue
		}
		if h.Typeflag == tar.TypeDir {
			return fmt.Errorf("scanner executable member is not regular")
		}
		out, e := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
		if e != nil {
			return e
		}
		_, copyErr := io.CopyN(out, reader, h.Size)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		found = true
	}
	if _, err = io.Copy(io.Discard, limited); err != nil || limited.N <= 0 || !found {
		return fmt.Errorf("scanner archive incomplete or exceeds expansion bound")
	}
	return nil
}
