package scanning

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
)

func imageExecutionFixture(t *testing.T, failure string) (ImageOptions, imageMeasurementPolicy, *[]string) {
	t.Helper()
	root := t.TempDir()
	identity, image, _, report := imageScannerFixture(t, "backend", runtime.GOARCH, []any{testFinding()})
	binaryTool := make([]byte, 40)
	copy(binaryTool, []byte{0x7f, 'E', 'L', 'F', 2, 1})
	arch := uint16(62)
	if runtime.GOARCH == "arm64" {
		arch = 183
	}
	binary.LittleEndian.PutUint16(binaryTool[18:], arch)
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	writer := tar.NewWriter(gz)
	writer.WriteHeader(&tar.Header{Name: "trivy", Mode: 0755, Typeflag: tar.TypeReg, Size: int64(len(binaryTool))})
	writer.Write(binaryTool)
	writer.Close()
	gz.Close()
	cosign, bundle, archive := []byte("disposable pinned verifier"), []byte("disposable official signature bundle"), []byte("disposable original OCI bytes")
	for name, body := range map[string][]byte{"cosign": cosign, "trivy.tar.gz": packed.Bytes(), "bundle.json": bundle, "image.tar": archive} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	policy := imageMeasurementPolicy{Pin: trivyPin{strings.TrimPrefix(hash(packed.Bytes()), "sha256:"), strings.TrimPrefix(hash(bundle), "sha256:"), strings.TrimPrefix(hash(binaryTool), "sha256:")}, CosignVersion: "v2.6.5", CosignCommit: strings.Repeat("d", 40), CosignDigest: hash(cosign)}
	policy.Stage = func(input, output string, opts oci.Options) (oci.Image, error) {
		if input != filepath.Join(root, "image.tar") || opts.TestedConfig != image.Config || opts.Platform != identity.Platform {
			t.Fatal("wrong staged image context")
		}
		os.Mkdir(output, 0700)
		os.WriteFile(filepath.Join(output, "index.json"), []byte("inert layout"), 0600)
		return oci.Image{Platform: identity.Platform, ConfigDigest: image.Config, ArchiveDigest: hash(archive), ManifestDigest: image.Manifest, ManifestSize: 42, ManifestMediaType: oci.ManifestMediaType, BlobCount: 3}, nil
	}
	privateRoots := []string{}
	verified := false
	o := ImageOptions{Root: root, Source: identity, Component: "backend", Archive: filepath.Join(root, "image.tar"), TestedConfig: image.Config, ToolArchive: filepath.Join(root, "trivy.tar.gz"), ToolBundle: filepath.Join(root, "bundle.json"), Cosign: filepath.Join(root, "cosign"), Output: filepath.Join(root, "measurement")}
	o.Execute = func(_ context.Context, _ time.Duration, env map[string]string, args ...string) ([]byte, error) {
		private := env["HOME"]
		privateRoots = append(privateRoots, private)
		if !reflect.DeepEqual(env, scanEnvironment(private)) {
			t.Fatal("scanner inherited credentials, proxy, plugins or config")
		}
		if filepath.Base(args[0]) == "cosign" && slices.Contains(args, "version") {
			return contract.JSON(map[string]any{"gitVersion": policy.CosignVersion, "gitCommit": policy.CosignCommit, "gitTreeState": "clean", "platform": identity.Platform})
		}
		if slices.Contains(args, "verify-blob") {
			for _, flag := range []string{"--insecure-ignore-tlog=false", "--insecure-ignore-sct=false", "--private-infrastructure=false", "--offline=false", "--new-bundle-format=true"} {
				if !slices.Contains(args, flag) {
					t.Fatal("scanner verifier weakened trust")
				}
			}
			if failure == "signature" {
				return nil, errors.New("signature verification failed")
			}
			verified = true
			return []byte("private verification log"), nil
		}
		if !verified {
			t.Fatal("scanner executed before official signature verification")
		}
		if slices.Contains(args, "--version") {
			return contract.JSON(map[string]any{"Version": TrivyVersion})
		}
		if slices.Contains(args, "--download-db-only") {
			db := filepath.Join(private, "cache/db")
			os.WriteFile(filepath.Join(db, "trivy.db"), []byte("disposable official downloaded db"), 0600)
			metadata, _ := contract.JSON(map[string]any{"Version": 2, "UpdatedAt": "2026-10-24T00:00:00Z", "NextUpdate": "2026-10-25T00:00:00Z", "DownloadedAt": "2026-10-24T00:00:00Z"})
			os.WriteFile(filepath.Join(db, "metadata.json"), metadata, 0600)
			return nil, nil
		}
		for _, flag := range []string{"--list-all-pkgs=true", "--ignore-unfixed=false", "--ignorefile=", "--ignore-policy=", "--offline-scan", "--skip-db-update", "--show-suppressed"} {
			if !slices.Contains(args, flag) {
				t.Fatal("scanner omitted all-findings/no-suppression policy")
			}
		}
		if failure == "scan" {
			return nil, errors.New("measurement failed")
		}
		if failure == "layout" {
			os.WriteFile(filepath.Join(private, "layout/index.json"), []byte("changed"), 0600)
		}
		return contract.JSON(report)
	}
	return o, policy, &privateRoots
}

func TestImageScannerRehearsalAuthenticatesBeforeExecutionAndRetainsEveryFinding(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native scanner runs on Linux")
	}
	o, policy, roots := imageExecutionFixture(t, "")
	record, err := measureImage(context.Background(), o, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(record["findings"].([]map[string]any)) != 1 || record["publication_authorized"] != false || record["final_image_scanners_gate_pending"] != true {
		t.Fatal("scanner authorized or lost finding")
	}
	for _, name := range []string{"scan.json", "measurement.json"} {
		info, err := os.Stat(filepath.Join(o.Output, name))
		if err != nil || info.Mode().Perm() != 0400 {
			t.Fatal("complete private output missing")
		}
	}
	for _, root := range *roots {
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("private scanner tool/database snapshot retained")
		}
	}
}
func TestFailedScannerOrChangedInputsCannotWriteCompletedMeasurement(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native scanner runs on Linux")
	}
	for _, failure := range []string{"signature", "scan", "layout", "pin"} {
		t.Run(failure, func(t *testing.T) {
			o, policy, roots := imageExecutionFixture(t, failure)
			if failure == "pin" {
				policy.Pin.Archive = strings.Repeat("f", 64)
			}
			if _, err := measureImage(context.Background(), o, policy); err == nil {
				t.Fatal("accepted failed or substituted scanner inputs")
			}
			if _, err := os.Stat(o.Output); !os.IsNotExist(err) {
				t.Fatal("incomplete scanner became retained success")
			}
			for _, root := range *roots {
				if _, err := os.Stat(root); !os.IsNotExist(err) {
					t.Fatal("failed scanner left private tool/database snapshot")
				}
			}
		})
	}
}
