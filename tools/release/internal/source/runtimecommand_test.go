package source

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func TestRuntimeReplayRequiresCompleteSmokeForExactPair(t *testing.T) {
	identity := Context{"endorses/psst.zip", "v1.2.3", strings.Repeat("a", 40)}
	o := RuntimeOptions{Source: identity, Platform: "linux/amd64", BackendConfig: "sha256:" + strings.Repeat("b", 64), WebConfig: "sha256:" + strings.Repeat("c", 64)}
	inventory := newInventory(identity)
	digest := "sha256:" + strings.Repeat("d", 64)
	record := map[string]any{
		"schema_version": 1, "kind": "release-image-smoke", "version": identity.Version, "revision": identity.Commit, "platform": o.Platform, "execution": "native",
		"tested_configs": map[string]string{"backend": o.BackendConfig, "web": o.WebConfig}, "runtime_pack_sha256": digest,
		"checks":       []string{"image-metadata", "fixture-isolation", "container-hardening", "health-public-config", "compiled-web-assets", "application-notices", "runtime-offer", "administrator-authentication", "credential-free-restart", "cleanup"},
		"completed_at": "2026-10-24T00:00:00Z", "publication_authorized": false,
	}
	raw, _ := contract.JSON(record)
	if err := CheckRuntimeSmoke(raw, o, digest); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"execution": "emulated", "revision": strings.Repeat("f", 40), "checks": []string{"image-metadata"}, "tested_configs": map[string]string{"backend": o.WebConfig, "web": o.BackendConfig}, "runtime_pack_sha256": "sha256:" + strings.Repeat("e", 64), "publication_authorized": true} {
		var altered map[string]any
		if err := json.Unmarshal(raw, &altered); err != nil {
			t.Fatal(err)
		}
		altered[name] = value
		bad, _ := contract.JSON(altered)
		if err := CheckRuntimeSmoke(bad, o, digest); err == nil {
			t.Errorf("accepted substituted completed smoke field %s", name)
		}
	}
	// A complete smoke cannot be attached to an unrelated shared verification.
	if _, err := RuntimeReplayReport(o, RuntimeVerification{Inventory: inventory}, raw); err == nil {
		t.Fatal("runtime report accepted an unrelated verified image pair")
	}
}
