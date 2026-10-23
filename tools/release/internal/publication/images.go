package publication

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/artifact"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
)

func writePrivate(name string, raw []byte) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("private image metadata unavailable")
	}
	_, writeErr := f.Write(raw)
	syncErr, closeErr := f.Sync(), f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return fmt.Errorf("private image metadata could not be synced")
	}
	return nil
}

// stageImages reuses the strict shared OCI reader. It moves verified blobs into
// one private pair layout, avoiding a second layer copy or recompression. The
// catalog selects the exact prepared multi-platform index by digest.
func stageImages(ctx context.Context, inputs Inputs, state string) (map[string]string, error) {
	root := filepath.Join(state, "oci")
	if err := os.Mkdir(root, 0700); err != nil {
		return nil, fmt.Errorf("private image staging already exists or unavailable")
	}
	layouts := map[string]string{}
	for _, component := range []string{"backend", "web"} {
		layout := filepath.Join(root, component)
		for _, directory := range []string{layout, filepath.Join(layout, "blobs"), filepath.Join(layout, "blobs/sha256")} {
			if err := os.Mkdir(directory, 0700); err != nil {
				return nil, fmt.Errorf("private OCI pair layout unavailable")
			}
		}
		for _, arch := range []string{"amd64", "arm64"} {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("publication image staging cancelled")
			}
			key := component + "-" + arch
			native := filepath.Join(root, key)
			image, err := oci.StageArchive(inputs.Archives[key], native, oci.Options{Repository: inputs.Binding.Repository, Version: inputs.Binding.Version, Commit: inputs.Binding.Commit, Component: component, Platform: "linux/" + arch, TestedConfig: inputs.TestedConfigs[key]})
			if err != nil {
				return nil, fmt.Errorf("final native OCI archive validation failed")
			}
			if image.ManifestDigest != inputs.Manifest.Images[component].PlatformDigests["linux/"+arch] || image.ConfigDigest != inputs.TestedConfigs[key] {
				return nil, fmt.Errorf("native OCI child/config differs from authenticated tested image")
			}
			blobs, err := os.ReadDir(filepath.Join(native, "blobs/sha256"))
			if err != nil {
				return nil, fmt.Errorf("verified native OCI payload unavailable")
			}
			for _, blob := range blobs {
				if !digestPattern.MatchString("sha256:"+blob.Name()) || !blob.Type().IsRegular() {
					return nil, fmt.Errorf("unsafe verified OCI staging inventory")
				}
				from, to := filepath.Join(native, "blobs/sha256", blob.Name()), filepath.Join(layout, "blobs/sha256", blob.Name())
				if existing, err := os.Lstat(to); err == nil {
					hash, hashErr := artifact.HashFile(to)
					if !existing.Mode().IsRegular() || hashErr != nil || hash != blob.Name() {
						return nil, fmt.Errorf("shared OCI blob differs from verified digest")
					}
					if os.Remove(from) != nil {
						return nil, fmt.Errorf("duplicate private OCI staging cleanup failed")
					}
				} else if !os.IsNotExist(err) || os.Rename(from, to) != nil {
					return nil, fmt.Errorf("verified OCI blob could not be retained")
				}
			}
			if os.RemoveAll(native) != nil {
				return nil, fmt.Errorf("native OCI intermediate cleanup failed")
			}
		}
		raw, err := read(inputs.Indexes[component])
		if err != nil || oci.ValidateIndex(raw, inputs.Manifest.Images[component]) != nil {
			return nil, fmt.Errorf("paired OCI index differs from reviewed native children")
		}
		_, expected, _ := strings.Cut(inputs.Manifest.Images[component].Index, "@")
		if digest(raw) != expected {
			return nil, fmt.Errorf("paired OCI index bytes differ from authenticated subject")
		}
		var index struct {
			MediaType string `json:"mediaType"`
		}
		if json.Unmarshal(raw, &index) != nil {
			return nil, fmt.Errorf("invalid paired OCI index document")
		}
		if err = writePrivate(filepath.Join(layout, "blobs/sha256", strings.TrimPrefix(expected, "sha256:")), raw); err != nil {
			return nil, err
		}
		catalog, _ := contract.JSON(map[string]any{"schemaVersion": 2, "mediaType": oci.IndexMediaType, "manifests": []any{map[string]any{"mediaType": index.MediaType, "digest": expected, "size": len(raw)}}})
		if err = writePrivate(filepath.Join(layout, "index.json"), catalog); err != nil {
			return nil, err
		}
		if err = writePrivate(filepath.Join(layout, "oci-layout"), []byte("{\"imageLayoutVersion\":\"1.0.0\"}\n")); err != nil {
			return nil, err
		}
		layouts[component] = layout
	}
	return layouts, nil
}
