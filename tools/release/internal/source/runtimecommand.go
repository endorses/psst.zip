package source

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func runtimeCommand(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("sources runtime requires collect, overlays, describe, verify-live or replay")
	}
	flags := flag.NewFlagSet("sources runtime "+args[0], flag.ContinueOnError)
	o := RuntimeOptions{Root: root}
	flags.StringVar(&o.Source.Repository, "repository", "", "source repository")
	flags.StringVar(&o.Source.Version, "version", "", "exact stable version")
	flags.StringVar(&o.Source.Commit, "commit", "", "exact source commit")
	flags.StringVar(&o.Platform, "platform", "", "native Linux platform")
	flags.StringVar(&o.Output, "output", "", "new collection/report output")
	flags.StringVar(&o.Collection, "collection", "", "retained runtime collection")
	flags.StringVar(&o.BackendConfig, "backend-config", "", "exact backend config digest")
	flags.StringVar(&o.WebConfig, "web-config", "", "exact web config digest")
	flags.StringVar(&o.ImagesTar, "images-tar", "", "original paired Docker save")
	flags.StringVar(&o.HelperImage, "helper-image", "", "locally built unprivileged source helper")
	flags.StringVar(&o.CaddyBase, "caddy-base", "", "immutable official Caddy base")
	flags.StringVar(&o.Cosign, "cosign", "", "pinned official legacy verifier")
	flags.StringVar(&o.DeliveryBase, "delivery-base", "", "exact release download base")
	var backendArchive, webArchive, smoke, expectedAsset string
	flags.StringVar(&backendArchive, "backend-archive", "", "tested backend OCI archive")
	flags.StringVar(&webArchive, "web-archive", "", "tested web OCI archive")
	flags.StringVar(&smoke, "smoke", "", "actual completed native smoke report")
	flags.StringVar(&expectedAsset, "source-sha256", "", "externally bound original runtime source digest")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("runtime source command accepts flags only")
	}
	if err := o.Source.Validate(); err != nil {
		return err
	}
	if o.Platform != "linux/amd64" && o.Platform != "linux/arm64" {
		return fmt.Errorf("runtime command requires native platform")
	}
	if o.DeliveryBase == "" {
		o.DeliveryBase = "https://github.com/" + o.Source.Repository + "/releases/download/" + o.Source.Version
	}
	var result any
	switch args[0] {
	case "collect":
		if o.Output == "" || o.ImagesTar == "" || o.Cosign == "" {
			return fmt.Errorf("runtime collect requires output, original image save and verifier")
		}
		_, err := CollectRuntime(ctx, o)
		return err
	case "overlays":
		if o.Collection == "" {
			return fmt.Errorf("runtime overlays requires collection")
		}
		return WriteRuntimeOverlays(ctx, o)
	case "describe":
		var err error
		result, err = DescribeRuntime(o)
		if err != nil {
			return err
		}
	case "verify-live":
		var err error
		result, err = VerifyRuntimeLive(ctx, o, map[string]string{"backend": o.BackendConfig, "web": o.WebConfig})
		if err != nil {
			return err
		}
	case "replay":
		if smoke == "" || backendArchive == "" || webArchive == "" || !digestRE.MatchString(expectedAsset) {
			return fmt.Errorf("runtime replay requires smoke, both archives and externally bound source digest")
		}
		verified, err := VerifyRuntimeImages(ctx, o, map[string]string{"backend": backendArchive, "web": webArchive}, map[string]string{"backend": o.BackendConfig, "web": o.WebConfig})
		if err != nil {
			return err
		}
		if verified.Asset.SHA256 != expectedAsset {
			return fmt.Errorf("runtime source artifact differs from external binding")
		}
		smokeBytes, err := readRegular(smoke, MetadataLimit)
		if err != nil {
			return err
		}
		if err = checkRuntimeSmoke(smokeBytes, o, verified.InventoryFile.SHA256); err != nil {
			return err
		}
		result = runtimeReplayReport(o, verified, smokeBytes)
	default:
		return fmt.Errorf("unknown runtime source command")
	}
	raw, err := contract.JSON(result)
	if err != nil {
		return err
	}
	if o.Output != "" {
		return writeNew(o.Output, raw)
	}
	_, err = os.Stdout.Write(raw)
	return err
}
func checkRuntimeSmoke(raw []byte, o RuntimeOptions, inventoryDigest string) error {
	var smoke struct {
		SchemaVersion                                int `json:"schema_version"`
		Kind, Version, Revision, Platform, Execution string
		TestedConfigs                                map[string]string `json:"tested_configs"`
		RuntimePackSHA256                            string            `json:"runtime_pack_sha256"`
		Checks                                       []string
		CompletedAt                                  string `json:"completed_at"`
		PublicationAuthorized                        bool   `json:"publication_authorized"`
	}
	if err := contract.Decode(raw, MetadataLimit, &smoke); err != nil {
		return err
	}
	if smoke.SchemaVersion != 1 || smoke.Kind != "release-image-smoke" || smoke.Version != o.Source.Version || smoke.Revision != o.Source.Commit || smoke.Platform != o.Platform || smoke.Execution != "native" || smoke.RuntimePackSHA256 != inventoryDigest || smoke.PublicationAuthorized || len(smoke.TestedConfigs) != 2 || smoke.TestedConfigs["backend"] != o.BackendConfig || smoke.TestedConfigs["web"] != o.WebConfig {
		return fmt.Errorf("runtime source replay smoke belongs to another actual native pair")
	}
	if _, err := time.Parse(time.RFC3339Nano, smoke.CompletedAt); err != nil {
		return fmt.Errorf("actual native smoke completion time missing")
	}
	required := map[string]bool{"image-metadata": true, "fixture-isolation": true, "container-hardening": true, "health-public-config": true, "compiled-web-assets": true, "application-notices": true, "runtime-offer": true, "administrator-authentication": true, "credential-free-restart": true, "cleanup": true}
	if len(smoke.Checks) != len(required) {
		return fmt.Errorf("actual native smoke coverage incomplete")
	}
	for _, check := range smoke.Checks {
		if !required[check] {
			return fmt.Errorf("actual native smoke check missing or duplicate")
		}
		delete(required, check)
	}
	return nil
}
func runtimeReplayReport(o RuntimeOptions, verified RuntimeVerification, smoke []byte) map[string]any {
	images := map[string]any{}
	goSources := map[string]any{}
	executables := map[string]string{}
	origins := map[string]int{}
	packageCount := 0
	byID := map[string]Source{}
	for _, source := range verified.Inventory.Sources {
		byID[source.ID] = source
	}
	for component, image := range verified.Images {
		images[component] = OCIMeasurement(image)
	}
	for _, graph := range verified.Inventory.Runtime {
		source := byID["go-runtime-"+graph.GoVersion]
		goSources[graph.Component] = map[string]any{"version": source.Version, "commit": source.Authentication["source_commit"], "archive_sha256": source.Original.SHA256}
		executables[graph.Component] = graph.GoVersion
		packageCount += len(graph.Packages)
		seen := map[string]bool{}
		for _, p := range graph.Packages {
			seen[apkOrigin(p)] = true
		}
		origins[graph.Component] = len(seen)
	}
	return map[string]any{"schema_version": 1, "kind": "runtime-source-completeness", "verified_at": time.Now().UTC().Format(time.RFC3339Nano), "repository": o.Source.Repository, "version": o.Source.Version, "revision": o.Source.Commit, "platform": o.Platform, "runtime_source_asset_sha256": verified.Asset.SHA256, "runtime_pack_sha256": verified.InventoryFile.SHA256, "native_smoke_report_sha256": hash(smoke), "images": images, "coverage": map[string]any{"backend_origins": origins["backend"], "web_origins": origins["web"], "retained_package_versions": packageCount, "go_runtime": map[string]any{"sources": goSources, "executables": executables}}, "caddy_signature_verification": map[string]any{"legacy_sigstore_signatures_verified": true, "verification_source": "authenticated-native-producer", "verifications": verified.Inventory.Signatures}, "runtime_source_inputs_verified": true, "application_source_verified": false, "apk_binary_signatures_verified": false, "source_publication_verified": false, "distribution_authorized": false}
}

// CheckRuntimeSmoke shares the exact completed native smoke policy with assembly.
func CheckRuntimeSmoke(raw []byte, o RuntimeOptions, inventoryDigest string) error {
	return checkRuntimeSmoke(raw, o, inventoryDigest)
}
