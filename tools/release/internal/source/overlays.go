package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

type webRelease struct {
	Name, Version, Revision, License, Source string
	SourceArchive                            string   `json:"source_archive"`
	NoticeFiles                              []string `json:"notice_files"`
}

func webReleaseBytes(source Context, runtime bool) ([]byte, error) {
	files := []string{"/licenses/backend/THIRD_PARTY_NOTICES.txt"}
	if runtime {
		files = append(files, "/licenses/runtime/THIRD_PARTY_NOTICES.txt")
	}
	url := "https://github.com/" + source.Repository
	return contract.JSON(map[string]any{"name": "psst.zip", "version": source.Version, "revision": source.Commit, "license": "AGPL-3.0-only", "source": url, "source_archive": url + "/archive/" + source.Commit + ".tar.gz", "notice_files": files})
}
func checkWebRelease(raw []byte, source Context, runtime bool) error {
	var actual webRelease
	if err := contract.Decode(raw, 65536, &actual); err != nil {
		return err
	}
	expectedRaw, _ := webReleaseBytes(source, runtime)
	var expected webRelease
	json.Unmarshal(expectedRaw, &expected)
	got, _ := contract.JSON(actual)
	want, _ := contract.JSON(expected)
	if string(got) != string(want) {
		return fmt.Errorf("hosted release metadata differs from exact source")
	}
	return nil
}
func streamFact(name, relative string, limit int64) (File, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return File{}, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return File{}, fmt.Errorf("artifact is not a bounded regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return File{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(opened, info) {
		return File{}, fmt.Errorf("artifact changed while opening")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil || n != info.Size() {
		return File{}, fmt.Errorf("artifact changed while hashing")
	}
	after, err := file.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return File{}, fmt.Errorf("artifact changed while hashing")
	}
	return File{relative, "sha256:" + hex.EncodeToString(hash.Sum(nil)), n}, nil
}
func RuntimeAsset(collection string, source Context, platform string) (File, error) {
	name := runtimeAssetName(source.Version, platform)
	return streamFact(filepath.Join(collection, name), name, OfferingLimit)
}
func overlayPayload(o RuntimeOptions, inventory Inventory, files map[string][]byte, asset File) (map[string][]byte, error) {
	result := map[string][]byte{}
	inventoryRaw := files["source-inventory.json"]
	for _, graph := range inventory.Runtime {
		prefix := "overlays/" + graph.Component + "/"
		notices := files[graph.Notices.Path]
		if fact(graph.Notices.Path, notices) != graph.Notices {
			return nil, fmt.Errorf("runtime notice bytes differ from retained inventory")
		}
		result[prefix+"runtime/THIRD_PARTY_NOTICES.txt"] = notices
		reference, err := contract.JSON(map[string]any{"schema_version": 2, "kind": "runtime-corresponding-source-reference", "name": "psst.zip", "source": inventory.Source, "platform": graph.Platform, "component": graph.Component, "source_asset": map[string]any{"file": asset.Path, "sha256": asset.SHA256, "size": asset.Size, "url": o.DeliveryBase + "/" + asset.Path}, "source_inventory": fact("source-inventory.json", inventoryRaw), "source_ids": graph.SourceIDs})
		if err != nil {
			return nil, err
		}
		result[prefix+"runtime/runtime-inventory.json"] = reference
		result[prefix+"runtime/SOURCE.txt"] = []byte(fmt.Sprintf("psst.zip %s\nApplication revision: %s\nRuntime source archive: %s/%s\nArchive SHA256: %s\nOriginal source/recipe/license inventory inside archive: source-inventory.json\nInventory SHA256: %s\n", o.Source.Version, o.Source.Commit, o.DeliveryBase, asset.Path, strings.TrimPrefix(asset.SHA256, "sha256:"), strings.TrimPrefix(hash(inventoryRaw), "sha256:")))
		for name, body := range files {
			if strings.HasPrefix(name, "legal-review/licenses/") {
				relative := strings.TrimPrefix(name, "legal-review/")
				if !safePath(relative) {
					return nil, fmt.Errorf("unsafe standard runtime license path")
				}
				result[prefix+"runtime/"+relative] = body
			}
		}
		target := "/app/licenses/runtime"
		if graph.Component == "web" {
			target = "/srv/web/licenses/runtime"
		}
		dockerfile := "ARG ORIGINAL_IMAGE\nFROM ${ORIGINAL_IMAGE}\nCOPY runtime/ " + target + "/\n"
		if graph.Component == "web" {
			release, e := webReleaseBytes(o.Source, true)
			if e != nil {
				return nil, e
			}
			result[prefix+"release.json"] = release
			dockerfile += "COPY release.json /srv/web/licenses/release.json\n"
		}
		result[prefix+"Dockerfile"] = []byte(dockerfile)
	}
	return result, nil
}
func WriteRuntimeOverlays(ctx context.Context, o RuntimeOptions) error {
	o = o.defaults()
	inventory, files, err := RuntimeFiles(o.Collection, o.Source, o.Platform)
	if err != nil {
		return err
	}
	asset, err := RuntimeAsset(o.Collection, o.Source, o.Platform)
	if err != nil {
		return err
	}
	payload, err := overlayPayload(o, inventory, files, asset)
	if err != nil {
		return err
	}
	target := filepath.Join(o.Collection, "overlays")
	if _, err = os.Lstat(target); !os.IsNotExist(err) {
		return fmt.Errorf("runtime overlays already exist")
	}
	var names []string
	for name := range payload {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		destination := filepath.Join(o.Collection, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		if err = writeNew(destination, payload[name]); err != nil {
			return err
		}
	}
	return nil
}

// DescribeRuntime is a small adapter for native orchestration. It describes
// actual retained bytes and generated overlay files; substantive source replay
// and final OCI checks remain separate operations, not inferred from this JSON.
func DescribeRuntime(o RuntimeOptions) (map[string]any, error) {
	o = o.defaults()
	raw, err := readRegular(filepath.Join(o.Collection, "source-inventory.json"), MetadataLimit)
	if err != nil {
		return nil, err
	}
	inventory, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	if inventory.Source != o.Source || len(inventory.Runtime) != 2 {
		return nil, fmt.Errorf("runtime source inventory belongs to another native context")
	}
	asset, err := RuntimeAsset(o.Collection, o.Source, o.Platform)
	if err != nil {
		return nil, err
	}
	overlays := map[string]map[string]string{}
	bindings := map[string]any{}
	additional := map[string]map[string]string{}
	for _, graph := range inventory.Runtime {
		if graph.Platform != o.Platform {
			return nil, fmt.Errorf("runtime source graph native platform differs")
		}
		root := filepath.Join(o.Collection, "overlays", graph.Component, "runtime")
		files, err := regularTree(root)
		if err != nil {
			return nil, err
		}
		hashes := map[string]string{}
		for name, body := range files {
			hashes[name] = strings.TrimPrefix(hash(body), "sha256:")
		}
		for _, name := range []string{"THIRD_PARTY_NOTICES.txt", "runtime-inventory.json", "SOURCE.txt"} {
			if hashes[name] == "" {
				return nil, fmt.Errorf("runtime overlay/source offer incomplete")
			}
		}
		if hashes["THIRD_PARTY_NOTICES.txt"] != strings.TrimPrefix(graph.Notices.SHA256, "sha256:") {
			return nil, fmt.Errorf("runtime original notice aggregate changed")
		}
		overlays[graph.Component] = hashes
		bindings[graph.Component] = map[string]any{"original_image_id": graph.Config, "rootfs_layers": graph.RootFS, "runtime_config_sha256": strings.TrimPrefix(graph.RuntimeConfigSHA256, "sha256:"), "binary_path": "/" + graph.Binary.Path, "binary_sha256": strings.TrimPrefix(graph.Binary.SHA256, "sha256:"), "go_version": graph.GoVersion, "retained_packages": graph.Packages}
		additional[graph.Component] = map[string]string{}
		if graph.Component == "web" {
			body, err := readRegular(filepath.Join(o.Collection, "overlays/web/release.json"), 65536)
			if err != nil {
				return nil, err
			}
			if err = checkWebRelease(body, o.Source, true); err != nil {
				return nil, err
			}
			additional[graph.Component]["/srv/web/licenses/release.json"] = strings.TrimPrefix(hash(body), "sha256:")
		}
	}
	return map[string]any{"schema_version": 2, "kind": "native-runtime-source-description", "version": o.Source.Version, "revision": o.Source.Commit, "architecture": strings.TrimPrefix(o.Platform, "linux/"), "source_inventory_sha256": hash(raw), "source_asset": map[string]any{"file": asset.Path, "sha256": strings.TrimPrefix(asset.SHA256, "sha256:"), "size": asset.Size, "url": o.DeliveryBase + "/" + asset.Path}, "bindings": bindings, "overlays": overlays, "additional_files": additional}, nil
}

func VerifyRuntimeLive(ctx context.Context, o RuntimeOptions, configs map[string]string) (map[string]any, error) {
	description, err := DescribeRuntime(o)
	if err != nil {
		return nil, err
	}
	raw, err := readRegular(filepath.Join(o.Collection, "source-inventory.json"), MetadataLimit)
	if err != nil {
		return nil, err
	}
	inventory, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	execute := o.execute()
	images := map[string]any{}
	for _, graph := range inventory.Runtime {
		config := configs[graph.Component]
		if !digestRE.MatchString(config) {
			return nil, fmt.Errorf("missing final runtime image configuration")
		}
		inspected, err := execute(ctx, 55*time.Second, "docker", "image", "inspect", config)
		if err != nil {
			return nil, err
		}
		var result []struct {
			ID, Architecture, OS string
			Config               json.RawMessage
			RootFS               struct{ Layers []string }
		}
		if json.Unmarshal(inspected, &result) != nil || len(result) != 1 || result[0].ID != config || result[0].Architecture != strings.TrimPrefix(o.Platform, "linux/") || result[0].OS != "linux" || len(result[0].RootFS.Layers) <= len(graph.RootFS) {
			return nil, fmt.Errorf("live overlay image metadata differs")
		}
		for i, diff := range graph.RootFS {
			if result[0].RootFS.Layers[i] != diff {
				return nil, fmt.Errorf("live overlay image changed inherited layers")
			}
		}
		runtimeConfig, err := canonicalConfig(result[0].Config)
		if err != nil || hash(runtimeConfig) != graph.RuntimeConfigSHA256 {
			return nil, fmt.Errorf("live overlay image changed inherited runtime settings")
		}
		expectedFiles := map[string]string{"/" + graph.Binary.Path: strings.TrimPrefix(graph.Binary.SHA256, "sha256:")}
		if len(graph.Layers) == 0 {
			return nil, fmt.Errorf("runtime original database missing")
		}
		expectedFiles["/lib/apk/db/installed"] = graph.Layers[len(graph.Layers)-1].DatabaseSHA256
		prefix := "/app/licenses/runtime/"
		if graph.Component == "web" {
			prefix = "/srv/web/licenses/runtime/"
		}
		overlays := description["overlays"].(map[string]map[string]string)[graph.Component]
		for name, digest := range overlays {
			expectedFiles[prefix+name] = digest
		}
		if graph.Component == "web" {
			expectedFiles["/srv/web/licenses/release.json"] = description["additional_files"].(map[string]map[string]string)["web"]["/srv/web/licenses/release.json"]
		}
		var names []string
		for name := range expectedFiles {
			names = append(names, name)
		}
		sort.Strings(names)
		args := []string{"docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--entrypoint", "sha256sum", config}
		args = append(args, names...)
		measured, err := execute(ctx, 55*time.Second, args...)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSpace(string(measured)), "\n") {
			digest, name, ok := strings.Cut(line, "  ")
			if !ok || seen[name] || expectedFiles[name] != digest {
				return nil, fmt.Errorf("live runtime executable/database/legal bytes differ")
			}
			seen[name] = true
		}
		if len(seen) != len(expectedFiles) {
			return nil, fmt.Errorf("live runtime byte checks incomplete")
		}
		images[graph.Component] = map[string]any{"image_id": config, "binary_sha256": strings.TrimPrefix(graph.Binary.SHA256, "sha256:")}
	}
	return map[string]any{"schema_version": 2, "runtime_pack_sha256": strings.TrimPrefix(hash(raw), "sha256:"), "images": images, "overlay_verified": true, "publication_pending": true}, nil
}
