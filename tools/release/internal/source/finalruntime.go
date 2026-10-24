package source

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
)

type RuntimeVerification struct {
	Inventory     Inventory
	Asset         File
	InventoryFile File
	Images        map[string]oci.Image
	Files         map[string]map[string][]byte
	SourceFiles   map[string][]byte `json:"-"`
	NoticeFiles   map[string][]File
	GoLicenses    map[string]File
}

func CheckRuntimeImage(graph RuntimeGraph, image SavedImage, expected map[string][]byte) error {
	if image.Architecture != strings.TrimPrefix(graph.Platform, "linux/") || len(image.RootFS) <= len(graph.RootFS) {
		return fmt.Errorf("overlaid runtime native platform/inherited layers differ")
	}
	for i, diff := range graph.RootFS {
		if image.RootFS[i] != diff {
			return fmt.Errorf("overlaid image changed inherited filesystem layers")
		}
	}
	raw, err := canonicalConfig(image.RuntimeConfig)
	if err != nil || hash(raw) != graph.RuntimeConfigSHA256 {
		return fmt.Errorf("overlaid image changed inherited runtime configuration")
	}
	if fact(graph.Binary.Path, image.Files[graph.Binary.Path]) != graph.Binary {
		return fmt.Errorf("overlaid runtime executable differs from retained original")
	}
	got, _ := contract.JSON(image.Packages)
	want, _ := contract.JSON(graph.Packages)
	if !bytes.Equal(got, want) || len(image.Databases) != len(graph.Layers) {
		return fmt.Errorf("overlaid image APK origin graph differs")
	}
	for i, db := range graph.Layers {
		actual := image.Databases[i]
		if actual.Layer != db.Layer || actual.LayerSHA256 != db.LayerSHA256 || actual.DatabaseSHA256 != db.DatabaseSHA256 {
			return fmt.Errorf("overlaid image retained database/layer correspondence differs")
		}
	}
	allowedFiles := map[string]bool{}
	allowedDirs := map[string]bool{}
	for name, body := range expected {
		allowedFiles[name] = true
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			allowedDirs[parent] = true
		}
		if !bytes.Equal(image.Files[name], body) {
			return fmt.Errorf("final runtime notice/source bytes differ from exact retained offering")
		}
	}
	for _, layer := range image.LayerEntries[len(graph.RootFS):] {
		for _, entry := range layer {
			if entry.Type == tar.TypeDir {
				if !allowedDirs[entry.Path] {
					return fmt.Errorf("runtime overlay introduces an unrelated directory")
				}
			} else if entry.Type != tar.TypeReg && entry.Type != tar.TypeRegA || !allowedFiles[entry.Path] {
				return fmt.Errorf("runtime overlay changes an unrelated path or entry type")
			}
		}
	}
	return nil
}
func VerifyRuntimeImages(ctx context.Context, o RuntimeOptions, archives, configs map[string]string) (RuntimeVerification, error) {
	o = o.defaults()
	inventory, sourceFiles, err := ReplayRuntimeRetained(ctx, o)
	result := RuntimeVerification{Inventory: inventory, SourceFiles: sourceFiles, Images: map[string]oci.Image{}, Files: map[string]map[string][]byte{}, NoticeFiles: map[string][]File{}, GoLicenses: map[string]File{}}
	if err != nil {
		return result, err
	}
	asset, err := RuntimeAsset(o.Collection, o.Source, o.Platform)
	if err != nil {
		return result, err
	}
	result.Asset = asset
	result.InventoryFile = fact("source-inventory.json", sourceFiles["source-inventory.json"])
	payload, err := overlayPayload(o, inventory, sourceFiles, asset)
	if err != nil {
		return result, err
	}
	if len(archives) != 2 || len(configs) != 2 {
		return result, fmt.Errorf("runtime verification requires exact native pair")
	}
	for _, graph := range inventory.Runtime {
		component := graph.Component
		image, metadata, err := InspectOCI(archives[component], oci.Options{Repository: o.Source.Repository, Version: o.Source.Version, Commit: o.Source.Commit, Component: component, Platform: o.Platform, TestedConfig: configs[component]})
		if err != nil {
			return result, err
		}
		expected := map[string][]byte{}
		prefix := "overlays/" + component + "/runtime/"
		target := "app/licenses/runtime/"
		if component == "web" {
			target = "srv/web/licenses/runtime/"
		}
		for name, body := range payload {
			if strings.HasPrefix(name, prefix) {
				expected[target+strings.TrimPrefix(name, prefix)] = body
			}
		}
		if component == "web" {
			expected["srv/web/licenses/release.json"] = payload["overlays/web/release.json"]
		}
		if err = CheckRuntimeImage(graph, image, expected); err != nil {
			return result, err
		}
		if component == "backend" {
			goSource := Source{}
			for _, source := range inventory.Sources {
				if source.ID == "go-runtime-"+graph.GoVersion {
					goSource = source
					break
				}
			}
			found := false
			for label, file := range goSource.NoticeFiles {
				if strings.Count(label, "/") == 1 && strings.HasSuffix(label, "/LICENSE") {
					found = true
					result.GoLicenses[component] = fact("app/licenses/go/LICENSE", sourceFiles[file.Path])
					if !bytes.Equal(sourceFiles[file.Path], image.Files["app/licenses/go/LICENSE"]) {
						return result, fmt.Errorf("distributed Go runtime license differs from exact compiler original")
					}
				}
			}
			if !found {
				return result, fmt.Errorf("compiler original root license missing")
			}
		}
		result.Images[component] = metadata
		result.Files[component] = image.Files
		var paths []string
		for name := range expected {
			paths = append(paths, name)
		}
		sort.Strings(paths)
		for _, name := range paths {
			result.NoticeFiles[component] = append(result.NoticeFiles[component], fact(name, expected[name]))
		}
	}
	return result, nil
}
func OCIMeasurement(image oci.Image) map[string]any {
	return map[string]any{"platform": image.Platform, "manifest_digest": image.ManifestDigest, "manifest_media_type": image.ManifestMediaType, "manifest_size": image.ManifestSize, "config_digest": image.ConfigDigest, "archive_digest": image.ArchiveDigest, "blob_count": image.BlobCount}
}
