package evaluation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/containerbuild"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/publication"
)

type recoveryCoverage struct {
	Application      string `json:"application_source_asset"`
	Common           string `json:"common_source_asset"`
	Runtime          string `json:"runtime_source_asset"`
	RuntimeInventory string `json:"runtime_inventory_sha256"`
	Modules          string `json:"module_source_asset"`
	PreferredKey     string `json:"backend_preferred_key"`
	Notices          string `json:"notice_inventory_digest"`
	Config           string `json:"tested_config"`
	Manifest         string `json:"manifest_sha256"`
	Archive          string `json:"archive_sha256"`
	Verified         bool   `json:"preferred_form_verified"`
}
type recoverySourceDetails struct {
	Schema   int               `json:"schema_version"`
	Subjects map[string]string `json:"source_subjects"`
	Images   map[string]struct {
		Subject string `json:"subject"`
		Notices string `json:"notice_inventory_digest"`
	} `json:"images"`
	Policy struct {
		Path   string `json:"path"`
		Commit string `json:"source_commit"`
		Digest string `json:"record_digest"`
		Blob   string `json:"git_blob"`
	} `json:"policy"`
	Coverage map[string]recoveryCoverage `json:"coverage"`
}

func checkRecoverySourceFacts(result publication.Result, foundation containerbuild.ArtifactFoundation, manifest contract.ManifestV1) error {
	var details recoverySourceDetails
	if err := decodeComplete(result.Checks["corresponding-source"], &details); err != nil {
		return err
	}
	if details.Schema != 2 || details.Policy.Path != "tools/container-distribution-policy.json" || details.Policy.Commit != result.Commit || !recoveryDigest.MatchString(details.Policy.Digest) || !github.CommitPattern.MatchString(details.Policy.Blob) || len(details.Coverage) != 4 || len(details.Images) != 4 {
		return fmt.Errorf("recovery requires complete current preferred-source facts")
	}
	expected := map[string]string{}
	add := func(name string) error {
		sha := foundation.Assets[name]
		if !recoveryDigest.MatchString(sha) || result.Subjects["source:"+name] != "file:"+name+"@"+sha {
			return fmt.Errorf("recovery source offering differs from exact parent subjects")
		}
		expected["source:"+name] = result.Subjects["source:"+name]
		return nil
	}
	for _, name := range []string{"psst.zip-source-" + result.Version + ".tar.gz", "psst.zip-upstream-inputs-" + result.Version + ".tar.gz"} {
		if err := add(name); err != nil {
			return err
		}
	}
	for _, arch := range []string{"amd64", "arm64"} {
		platform := "linux/" + arch
		runtimeName := "psst.zip-" + result.Version + "-runtime-sources-" + arch + ".tar.gz"
		moduleName := "psst.zip-dependency-inputs-" + result.Version + "-" + arch + ".tar.gz"
		for _, name := range []string{runtimeName, moduleName} {
			if err := add(name); err != nil {
				return err
			}
		}
		for _, component := range []string{"backend", "web"} {
			target := component + "-" + arch
			coverage, ok := details.Coverage[target]
			image, err := foundationRecoveryImage(foundation, platform, component)
			if !ok || err != nil || coverage.Application != "psst.zip-source-"+result.Version+".tar.gz" || coverage.Common != "psst.zip-upstream-inputs-"+result.Version+".tar.gz" || coverage.Runtime != runtimeName || coverage.Modules != moduleName || !coverage.Verified || !recoveryDigest.MatchString(coverage.PreferredKey) || !recoveryDigest.MatchString(coverage.Notices) || coverage.RuntimeInventory != foundation.Native[platform].Inputs["runtime_inventory"] || !recoveryDigest.MatchString(coverage.RuntimeInventory) || coverage.Config != image.Config || coverage.Manifest != image.Manifest || coverage.Archive != image.Archive || details.Images[target].Subject != result.Subjects[target] || details.Images[target].Notices != coverage.Notices {
				return fmt.Errorf("recovery preferred-source coverage differs from authenticated native facts")
			}
			repository, _, _ := strings.Cut(manifest.Images[component].Index, "@")
			if result.Subjects[target] != "oci://"+repository+"@"+image.Manifest || image.Manifest != manifest.Images[component].PlatformDigests[platform] || image.Config != foundation.Native[platform].TestedConfigs[component] {
				return fmt.Errorf("recovery image source or configuration differs from manifest")
			}
		}
	}
	if !reflect.DeepEqual(details.Subjects, expected) || len(foundation.Assets) != 8 || len(result.Subjects) != 14 {
		return fmt.Errorf("recovery source subject set is incomplete or contains extras")
	}
	for _, component := range []string{"backend", "web"} {
		if result.Subjects[component+"-index"] != "oci://"+manifest.Images[component].Index {
			return fmt.Errorf("recovery index differs from exact native pair")
		}
	}
	return nil
}
func foundationRecoveryImage(f containerbuild.ArtifactFoundation, platform, component string) (recoveryImage, error) {
	var image recoveryImage
	raw, err := json.Marshal(f.Native[platform].Images[component])
	if err != nil {
		return image, err
	}
	err = decodeComplete(raw, &image)
	if err != nil || image.Platform != platform || !recoveryDigest.MatchString(image.Manifest) || !recoveryDigest.MatchString(image.Config) || !recoveryDigest.MatchString(image.Archive) || image.ManifestSize <= 0 || image.BlobCount <= 0 || image.MediaType != "application/vnd.oci.image.manifest.v1+json" && image.MediaType != "application/vnd.docker.distribution.manifest.v2+json" {
		return image, fmt.Errorf("authenticated final native recovery image metadata is incomplete")
	}
	return image, nil
}
func checkRecoveryImages(images map[string]recoveryImage, in recoveryInputs) error {
	if len(images) != 4 {
		return fmt.Errorf("recovery requires all four final native images")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, component := range []string{"backend", "web"} {
			platform, target := "linux/"+arch, component+"-"+arch
			want, err := foundationRecoveryImage(in.foundation, platform, component)
			if err != nil || images[target] != want || want.Manifest != in.manifest.Images[component].PlatformDigests[platform] || want.Config != in.descriptors[platform].TestedConfigs[component] || want.Archive != in.descriptors[platform].Artifacts[component+"_archive"].SHA256 {
				return fmt.Errorf("recovery final image/configuration differs from retained native assembly")
			}
		}
	}
	return nil
}
