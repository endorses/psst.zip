package containerbuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

func assemblyHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func sameAssemblyJSON(a, b any) bool {
	normalize := func(value any) ([]byte, error) {
		raw, err := contract.JSON(value)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return nil, err
		}
		return contract.JSON(decoded)
	}
	x, e := normalize(a)
	y, f := normalize(b)
	return e == nil && f == nil && bytes.Equal(x, y)
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func retainAssemblyAsset(ctx context.Context, from, to string, limit int64) (result source.File, err error) {
	before, err := os.Lstat(from)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > limit {
		return result, fmt.Errorf("source asset must be a bounded regular file")
	}
	input, err := os.Open(from)
	if err != nil {
		return result, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return result, fmt.Errorf("source asset changed before retention")
	}
	output, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(to)
		}
	}()
	hash := sha256.New()
	count, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(contextReader{ctx, input}, limit+1))
	after, statErr := input.Stat()
	syncErr, closeErr := output.Sync(), output.Close()
	if copyErr != nil || statErr != nil || syncErr != nil || closeErr != nil || count != before.Size() || count > limit || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return result, fmt.Errorf("source asset changed or failed during bounded retention")
	}
	return source.File{Path: filepath.Base(to), SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Size: count}, nil
}

// snapshotCollection validates the exact asset that remains in final output.
// Hardlinking our own retained file into a private two-file reader directory
// avoids a second payload copy and prevents a mutable caller file being replayed.
func snapshotCollection(ctx context.Context, collection, name, output, snapshot string) (string, source.File, error) {
	var asset source.File
	if err := os.Mkdir(snapshot, 0700); err != nil {
		return "", asset, err
	}
	retained := filepath.Join(output, name)
	asset, err := retainAssemblyAsset(ctx, filepath.Join(collection, name), retained, source.OfferingLimit)
	if err != nil {
		return "", asset, err
	}
	raw, err := readJSONFile(filepath.Join(collection, "source-inventory.json"))
	if err != nil {
		return "", asset, err
	}
	if err := writeRecord(filepath.Join(snapshot, "source-inventory.json"), raw); err != nil {
		return "", asset, err
	}
	if err := os.Link(retained, filepath.Join(snapshot, name)); err != nil {
		return "", asset, fmt.Errorf("cannot bind private source collection to retained asset")
	}
	return snapshot, asset, nil
}

func readAssemblyBuild(name string, candidate Candidate, platform string) (BuildRecord, []byte, map[string]string, error) {
	var record BuildRecord
	raw, err := readJSONFile(name)
	if err != nil {
		return record, nil, nil, err
	}
	if err := contract.Decode(raw, maxJSON, &record); err != nil {
		return record, nil, nil, err
	}
	if !sameAssemblyJSON(record.Candidate, candidate) || record.CheckedPlatform != platform || !record.NativeExecution || len(record.ToolchainOutput) != 6 || len(record.BuildMetadata) != 2 || len(record.Limitations) == 0 || filepath.Base(record.ImageArchive.Name) != record.ImageArchive.Name || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,150}$`).MatchString(record.ImageArchive.Name) || !strings.HasSuffix(record.ImageArchive.Name, ".tar") || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(record.ImageArchive.SHA256) || record.ImageArchive.Size <= 0 || record.ImageArchive.Size > 8<<30 {
		return record, nil, nil, fmt.Errorf("native build record differs from candidate/platform/original archive")
	}
	for _, key := range []string{"go", "node", "docker", "compose", "buildx", "builder"} {
		value := record.ToolchainOutput[key]
		if value == "" || len(value) > 8192 {
			return record, nil, nil, fmt.Errorf("missing actual native build toolchain output")
		}
	}
	goVersion := regexp.MustCompile(`^go version (go[0-9]+\.[0-9]+\.[0-9]+) ` + regexp.QuoteMeta(platform) + `$`).FindStringSubmatch(record.ToolchainOutput["go"])
	if len(goVersion) != 2 || !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(record.ToolchainOutput["node"]) {
		return record, nil, nil, fmt.Errorf("native build compiler/runtime versions are malformed")
	}
	defaults := DefaultBases()
	goTag := strings.Split(strings.Split(defaults["golang"], ":")[1], "-")[0]
	nodeTag := strings.Split(strings.Split(defaults["node"], ":")[1], "-")[0]
	if goVersion[1] != "go"+goTag || record.ToolchainOutput["node"] != "v"+nodeTag {
		return record, nil, nil, fmt.Errorf("native application compiler/runtime differs from selected current producer policy")
	}
	originals := map[string]bool{}
	for _, component := range []string{"backend", "web"} {
		var metadata map[string]json.RawMessage
		if err := contract.Decode(record.BuildMetadata[component], maxJSON, &metadata); err != nil {
			return record, nil, nil, err
		}
		for _, key := range []string{"containerimage.config.digest", "containerimage.digest"} {
			var value string
			if json.Unmarshal(metadata[key], &value) != nil || !digestRE.MatchString(value) {
				return record, nil, nil, fmt.Errorf("native build metadata image/config digest missing")
			}
			if key == "containerimage.config.digest" {
				originals[value] = true
			}
		}
	}
	if len(originals) != 2 {
		return record, nil, nil, fmt.Errorf("original native image configurations must be distinct")
	}
	return record, raw, map[string]string{"go": goVersion[1], "node": record.ToolchainOutput["node"]}, nil
}

type nativeMeasurement struct {
	SchemaVersion int                        `json:"schema_version"`
	Kind          string                     `json:"kind"`
	Source        scanning.NativeIdentity    `json:"source"`
	Smoke         json.RawMessage            `json:"smoke"`
	Images        map[string]json.RawMessage `json:"images"`
	Runtime       struct {
		Inventory string `json:"runtime_pack_sha256"`
		Asset     struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"source_asset"`
		Notices        map[string]map[string]string `json:"notice_files"`
		ReviewRequired bool                         `json:"distribution_review_required"`
	} `json:"runtime"`
	Authorized *bool `json:"publication_authorized"`
}

func readNativeMeasurement(name string, selected source.Context, platform string) (nativeMeasurement, []byte, error) {
	var value nativeMeasurement
	raw, err := readJSONFile(name)
	if err != nil {
		return value, nil, err
	}
	if err := contract.Decode(raw, maxJSON, &value); err != nil {
		return value, nil, err
	}
	expected := scanning.NativeIdentity{Source: scanning.Source{Repository: selected.Repository, Version: selected.Version, Commit: selected.Commit}, Platform: platform}
	if value.SchemaVersion != 2 || value.Kind != "native-release-measurement" || value.Source != expected || value.Authorized == nil || *value.Authorized || !value.Runtime.ReviewRequired || len(value.Images) != 2 || len(value.Runtime.Notices) != 2 || !digestRE.MatchString(value.Runtime.Inventory) || !digestRE.MatchString(value.Runtime.Asset.Digest) || value.Runtime.Asset.Size <= 0 {
		return value, nil, fmt.Errorf("native measurement source/runtime/review boundary differs")
	}
	return value, raw, nil
}

func measuredConfigs(smoke json.RawMessage) (map[string]string, error) {
	var value map[string]json.RawMessage
	if err := contract.Decode(smoke, maxJSON, &value); err != nil {
		return nil, err
	}
	required := []string{"schema_version", "kind", "version", "revision", "platform", "execution", "tested_configs", "runtime_pack_sha256", "checks", "completed_at", "publication_authorized"}
	if len(value) != len(required) {
		return nil, fmt.Errorf("native smoke has incomplete or unexpected fields")
	}
	for _, name := range required {
		if _, present := value[name]; !present {
			return nil, fmt.Errorf("native smoke required field missing")
		}
	}
	if !bytes.Equal(bytes.TrimSpace(value["publication_authorized"]), []byte("false")) {
		return nil, fmt.Errorf("native smoke cannot authorize publication")
	}
	var configs map[string]string
	if json.Unmarshal(value["tested_configs"], &configs) != nil || len(configs) != 2 || !digestRE.MatchString(configs["backend"]) || !digestRE.MatchString(configs["web"]) || configs["backend"] == configs["web"] {
		return nil, fmt.Errorf("native smoke lacks exact distinct tested configuration pair")
	}
	return configs, nil
}

func checkNativeCorrespondence(measurement nativeMeasurement, build BuildRecord, verified source.RuntimeVerification) error {
	if measurement.Runtime.Inventory != verified.InventoryFile.SHA256 || measurement.Runtime.Asset.Name != verified.Asset.Path || measurement.Runtime.Asset.Digest != verified.Asset.SHA256 || measurement.Runtime.Asset.Size != verified.Asset.Size || len(verified.Images) != 2 || len(verified.Inventory.Runtime) != 2 {
		return fmt.Errorf("native measurement belongs to another retained runtime offering")
	}
	for _, graph := range verified.Inventory.Runtime {
		var metadata map[string]json.RawMessage
		if err := contract.Decode(build.BuildMetadata[graph.Component], maxJSON, &metadata); err != nil {
			return err
		}
		var original string
		if json.Unmarshal(metadata["containerimage.config.digest"], &original) != nil || original != graph.Config {
			return fmt.Errorf("runtime inherited image differs from original native build configuration")
		}
		var measured map[string]any
		if err := contract.Decode(measurement.Images[graph.Component], maxJSON, &measured); err != nil {
			return err
		}
		if !sameAssemblyJSON(measured, source.OCIMeasurement(verified.Images[graph.Component])) {
			return fmt.Errorf("native measured OCI child/config/archive differs from verified bytes")
		}
		notices := map[string]string{}
		prefix := "app/licenses/runtime/"
		if graph.Component == "web" {
			prefix = "srv/web/licenses/runtime/"
		}
		for _, file := range verified.NoticeFiles[graph.Component] {
			if strings.HasPrefix(file.Path, prefix) {
				notices[strings.TrimPrefix(file.Path, prefix)] = strings.TrimPrefix(file.SHA256, "sha256:")
			}
		}
		if !sameAssemblyJSON(notices, measurement.Runtime.Notices[graph.Component]) {
			return fmt.Errorf("native runtime notice measurement differs from exact verified offering")
		}
	}
	return nil
}

func checkScanApplication(scan scanning.SourceMeasurement, files map[string][]byte) error {
	inputs := map[string]string{}
	for name, body := range files {
		if strings.HasPrefix(name, "backend/") || strings.HasPrefix(name, "web/") {
			inputs[name] = assemblyHash(body)
		}
	}
	if !sameAssemblyJSON(inputs, scan.Inputs) {
		return fmt.Errorf("source scanner inputs differ from retained exact application Git files")
	}
	return nil
}
