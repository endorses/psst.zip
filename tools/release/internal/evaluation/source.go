package evaluation

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/browser"
	"github.com/endorses/psst.zip/tools/release/internal/containerbuild"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

func preferredKey(modules source.Inventory) string {
	type item struct {
		ID       string
		Original source.File
		Members  *source.File
	}
	items := make([]item, 0, len(modules.Sources))
	for _, entry := range modules.Sources {
		items = append(items, item{entry.ID, entry.Original, entry.MemberInventory})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	raw, _ := contract.JSON(items)
	return digest(raw)
}

func correspondingSource(ctx context.Context, root string, f containerbuild.ArtifactFoundation) (map[string]any, map[string]any, error) {
	selected := source.Context{Repository: f.Repository, Version: f.Version, Commit: f.SourceCommit}
	policy, err := scanning.PublicationPolicyFact(ctx, root, f.Repository, f.SourceCommit)
	if err != nil {
		return nil, nil, err
	}
	covered := map[string]string{}
	subjects := map[string]string{}
	for key, value := range f.Subjects {
		if strings.HasPrefix(key, "source:") {
			subjects[key] = value
		}
	}
	retain := func(name, sha string) error {
		if f.Assets[name] != sha || f.Subjects["source:"+name] != "file:"+name+"@"+sha {
			return fmt.Errorf("corresponding source asset differs from final release binding")
		}
		covered[name] = sha
		return nil
	}
	if err = retain(f.Application.Asset.Path, f.Application.Asset.SHA256); err != nil {
		return nil, nil, err
	}
	commonName := "psst.zip-upstream-inputs-" + f.Version + ".tar.gz"
	if err = retain(commonName, f.Assets[commonName]); err != nil {
		return nil, nil, err
	}
	evidence := map[string]any{"schema_version": 2, "kind": "complete-corresponding-source-replay-evidence", "binding_digest": f.BindingSHA256, "application": f.Application, "common_inventory": f.Common, "backend_preferred": map[string]source.BackendPreferredFacts{}, "native": map[string]any{}, "policy": policy, "distribution_review_required": true, "byte_reproduction_verified": false, "publication_authorized": false}
	preferredCache := map[string]source.BackendPreferredFacts{}
	noticeCache := map[string]map[string]source.File{}
	coverage := map[string]any{}
	images := map[string]any{}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		runtime, ok := f.Runtime[platform]
		modules, modOK := f.Modules[platform]
		native, nativeOK := f.Native[platform]
		if !ok || !modOK || !nativeOK || runtime.Inventory.Source != selected || modules.Inventory.Source != selected {
			return nil, nil, fmt.Errorf("corresponding-source native replay context incomplete")
		}
		build, err := buildinfo.Read(bytes.NewReader(runtime.Files["backend"]["app/server"]))
		if err != nil || build.GoVersion != scanning.CompilerVersion {
			return nil, nil, fmt.Errorf("backend source coverage lacks actual current executable metadata")
		}
		retainedModules := map[string]source.Source{}
		for _, item := range modules.Inventory.Sources {
			if item.Ecosystem == "go" {
				retainedModules[item.Name+"@"+item.Version] = item
			}
		}
		for _, dependency := range build.Deps {
			if dependency == nil {
				return nil, nil, fmt.Errorf("backend executable dependency metadata incomplete")
			}
			retained, ok := retainedModules[dependency.Path+"@"+dependency.Version]
			if !ok || dependency.Replace != nil || dependency.Sum == "" || retained.Authentication["zip_h1"] != dependency.Sum {
				return nil, nil, fmt.Errorf("actual backend executable dependency lacks retained H1-authenticated original")
			}
		}
		key := preferredKey(modules.Inventory)
		preferred, ok := preferredCache[key]
		if !ok {
			preferred, err = source.BackendPreferred(ctx, f.Common, f.CommonFiles, modules.Inventory, modules.Files)
			if err != nil {
				return nil, nil, err
			}
			preferredCache[key] = preferred
			upstream, err := source.VerifyBackendSnapshot(ctx, selected, f.Application, modules.Inventory, modules.Files)
			if err != nil {
				return nil, nil, err
			}
			noticeCache[key] = upstream
		}
		backendNotices, err := source.VerifyFinalBackendNotices(selected, f.Application, runtime, noticeCache[key])
		if err != nil {
			return nil, nil, err
		}
		// The browser verifier already compared every final static file with the
		// exact native builder and preferred-source originals in this foundation.
		browserFacts := f.Browsers[platform]
		if err := checkBrowserSource(browserFacts, native.Preferred); err != nil {
			return nil, nil, err
		}
		if !bytes.Equal(f.Application.Files["LICENSE"], f.Application.Files["web/static/licenses/AGPL-3.0-only.txt"]) || len(f.Application.Files["LICENSE"]) == 0 {
			return nil, nil, fmt.Errorf("browser application license differs from original application license")
		}
		webNotices := map[string]any{}
		for name, file := range browserFacts.Verification.FinalStatic {
			if strings.HasPrefix(name, "licenses/") {
				webNotices[strings.TrimPrefix(name, "licenses/")] = file
			}
		}
		for _, name := range []string{"AGPL-3.0-only.txt", "THIRD_PARTY_NOTICES.txt", "dependency-inventory.json", "release.json", "backend/AGPL-3.0-only.txt", "runtime/THIRD_PARTY_NOTICES.txt", "runtime/SOURCE.txt", "runtime/runtime-inventory.json"} {
			if webNotices[name] == nil {
				return nil, nil, fmt.Errorf("final browser original notice/source tree incomplete")
			}
		}
		webRaw, _ := contract.JSON(webNotices)
		webNoticeDigest := digest(webRaw)
		arch := strings.TrimPrefix(platform, "linux/")
		moduleName := "psst.zip-dependency-inputs-" + f.Version + "-" + arch + ".tar.gz"
		if err = retain(moduleName, f.Assets[moduleName]); err != nil {
			return nil, nil, err
		}
		if err = retain(runtime.Asset.Path, runtime.Asset.SHA256); err != nil {
			return nil, nil, err
		}
		evidence["backend_preferred"].(map[string]source.BackendPreferredFacts)[key] = preferred
		evidence["native"].(map[string]any)[platform] = map[string]any{"module_inventory": modules.Inventory, "runtime_inventory": runtime.Inventory, "runtime_source_asset": runtime.Asset, "runtime_inventory_file": runtime.InventoryFile, "backend_preferred_key": key, "backend_notices": backendNotices, "browser_preferred": native.Preferred, "browser_notices": webNotices, "native_inputs": native.Inputs}
		for _, component := range []string{"backend", "web"} {
			target := component + "-" + arch
			noticeDigest := backendNotices.Digest
			if component == "web" {
				noticeDigest = webNoticeDigest
			}
			image := runtime.Images[component]
			if f.Subjects[target] != "oci://"+strings.Split(f.Manifest.Images[component].Index, "@")[0]+"@"+image.ManifestDigest {
				return nil, nil, fmt.Errorf("corresponding-source image subject differs from final native replay")
			}
			images[target] = map[string]any{"subject": f.Subjects[target], "notice_inventory_digest": noticeDigest}
			coverage[target] = map[string]any{"application_source_asset": f.Application.Asset.Path, "common_source_asset": commonName, "runtime_source_asset": runtime.Asset.Path, "runtime_inventory_sha256": runtime.InventoryFile.SHA256, "module_source_asset": moduleName, "backend_preferred_key": key, "notice_inventory_digest": noticeDigest, "tested_config": image.ConfigDigest, "manifest_sha256": image.ManifestDigest, "archive_sha256": image.ArchiveDigest, "preferred_form_verified": true}
		}
	}
	if len(covered) != len(subjects) {
		return nil, nil, fmt.Errorf("offered source asset lacks substantive source coverage")
	}
	evidence["source_assets"] = covered
	evidence["coverage"] = coverage
	details := map[string]any{"schema_version": 2, "source_subjects": subjects, "images": images, "policy": policy, "coverage": coverage}
	return details, evidence, nil
}

func checkBrowserSource(measured browser.NativeFacts, preferred browser.PreferredFacts) error {
	// PreferredSourceComplete deliberately remains false in the native
	// measurement. Approval comes from Assemble's independently derived
	// VerifyPreferred facts, rather than mutating a measured success flag.
	if !measured.BuilderObservedInputsVerified || !measured.GitSourceBindingVerified || !measured.NPMMemberIntegrityVerified || !measured.OCIImageVerified || len(measured.Verification.UnresolvedJavaScript) != 0 || measured.PublicationAuthorized || measured.DistributionAuthorized || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(preferred.Catalog.SHA256) || preferred.Catalog.Size <= 0 || len(preferred.Modules) == 0 || len(preferred.Generators) == 0 || preferred.Reproduced {
		return fmt.Errorf("browser preferred-source/final-byte replay incomplete")
	}
	return nil
}
