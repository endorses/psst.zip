package containerbuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/oci"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

// OCIMetadata is the small native producer/recovery view of the shared verifier.
// It contains no final filesystem bytes or publication authorization.
type OCIMetadata struct {
	Platform          string `json:"platform"`
	ManifestDigest    string `json:"manifest_digest"`
	ManifestSize      int64  `json:"manifest_size"`
	ManifestMediaType string `json:"manifest_media_type"`
	ConfigDigest      string `json:"config_digest"`
	ArchiveDigest     string `json:"archive_digest"`
	BlobCount         int    `json:"blob_count"`
}

func metadata(image oci.Image) OCIMetadata {
	return OCIMetadata{image.Platform, image.ManifestDigest, image.ManifestSize, image.ManifestMediaType, image.ConfigDigest, image.ArchiveDigest, image.BlobCount}
}

type OCIAssemblyOptions struct {
	Repository    string            `json:"repository"`
	Version       string            `json:"version"`
	Commit        string            `json:"commit"`
	Archives      map[string]string `json:"archives"`
	TestedConfigs map[string]string `json:"tested_configs"`
	Output        string            `json:"output"`
}

type OCIAssembly struct {
	SchemaVersion         int                         `json:"schema_version"`
	Kind                  string                      `json:"kind"`
	Repository            string                      `json:"repository"`
	Version               string                      `json:"version"`
	SourceCommit          string                      `json:"source_commit"`
	Images                map[string]contract.ImageV1 `json:"images"`
	Children              map[string]OCIMetadata      `json:"children"`
	PublicationAuthorized bool                        `json:"publication_authorized"`
}

// AssembleOCI exists for the retained Python measurement fixtures and adapters;
// actual release assembly reuses already inspected images through AssembleIndexes.
func AssembleOCI(ctx context.Context, options OCIAssemblyOptions) (OCIAssembly, error) {
	var result OCIAssembly
	if options.Output == "" || len(options.Archives) != 4 || len(options.TestedConfigs) != 4 {
		return result, fmt.Errorf("OCI assembly requires four exact native image inputs")
	}
	if info, err := os.Lstat(options.Output); err != nil && !os.IsNotExist(err) || err == nil && !info.IsDir() {
		return result, fmt.Errorf("OCI assembly output must be a regular directory")
	}
	for _, name := range []string{"backend-index.json", "web-index.json", "oci-correspondence.json"} {
		if _, err := os.Lstat(filepath.Join(options.Output, name)); !os.IsNotExist(err) {
			return result, fmt.Errorf("OCI assembly output already exists")
		}
	}
	images := map[string]oci.Image{}
	children := map[string]OCIMetadata{}
	for _, component := range []string{"backend", "web"} {
		for _, platform := range releasePlatforms {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			key := component + "-" + strings.TrimPrefix(platform, "linux/")
			image, err := oci.InspectArchive(options.Archives[key], oci.Options{Repository: options.Repository, Version: options.Version, Commit: options.Commit, Platform: platform, Component: component, TestedConfig: options.TestedConfigs[key]})
			if err != nil {
				return result, err
			}
			images[key], children[key] = image, metadata(image)
		}
	}
	records, indexes, err := AssembleIndexes(source.Context{Repository: options.Repository, Version: options.Version, Commit: options.Commit}, images)
	if err != nil {
		return result, err
	}
	if err = os.MkdirAll(options.Output, 0700); err != nil {
		return result, err
	}
	for component, raw := range indexes {
		if err = writeRecord(filepath.Join(options.Output, component+"-index.json"), raw); err != nil {
			return result, err
		}
	}
	result = OCIAssembly{SchemaVersion: 1, Kind: "local-oci-correspondence", Repository: options.Repository, Version: options.Version, SourceCommit: options.Commit, Images: records, Children: children}
	raw, err := contract.JSON(result)
	if err == nil {
		err = writeRecord(filepath.Join(options.Output, "oci-correspondence.json"), raw)
	}
	return result, err
}
