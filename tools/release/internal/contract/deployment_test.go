package contract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deploymentManifestFixture() ManifestV1 {
	const repository = "endorses/psst.zip"
	m := ManifestV1{SchemaVersion: 1, Version: "v1.2.3", PayloadProfile: "deployment-ready", Source: SourceV1{Repository: repository, Commit: strings.Repeat("a", 40), ArchiveURL: "https://github.com/" + repository + "/archive/" + strings.Repeat("a", 40) + ".tar.gz"}, Platforms: []string{"linux/amd64", "linux/arm64"}, Images: map[string]ImageV1{}, Bundle: BundleV1{Name: "psst.zip-deployment-v1.2.3.tar.gz", SHA256: strings.Repeat("0", 64)}, Requirements: RequirementsV1{Docker: "27.0.0", Compose: "2.24.4"}, Notes: NotesV1{Migration: "Normal startup may migrate.", Rollback: "Restore matching images onto new volumes.", CheckpointRequired: true}, Build: BuildV1{BaseImages: map[string]string{}, Toolchains: map[string]string{"go": "1.27.3", "node": "24.0.0"}}}
	for _, component := range []string{"backend", "web"} {
		m.Images[component] = ImageV1{Index: "ghcr.io/endorses/psst-zip-" + component + "@sha256:" + strings.Repeat("b", 64), PlatformDigests: map[string]string{"linux/amd64": "sha256:" + strings.Repeat("c", 64), "linux/arm64": "sha256:" + strings.Repeat("d", 64)}}
	}
	for _, name := range []string{"golang", "node", "alpine", "caddy"} {
		m.Build.BaseImages[name] = name + "@sha256:" + strings.Repeat("e", 64)
	}
	return m
}
func TestManifestV1PreservesExactDeploymentContract(t *testing.T) {
	m := deploymentManifestFixture()
	data, err := JSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeManifestV1(data, m.Source.Repository); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(map[string]any){func(v map[string]any) { delete(v, "source") }, func(v map[string]any) { v["unexpected"] = true }, func(v map[string]any) { v["version"] = "v01.2.3" }, func(v map[string]any) { v["schema_version"] = true }, func(v map[string]any) { v["platforms"] = []string{"linux/arm64", "linux/amd64"} }, func(v map[string]any) { v["requirements"].(map[string]any)["docker"] = "1.0.0" }, func(v map[string]any) { v["notes"].(map[string]any)["checkpoint_required"] = false }, func(v map[string]any) {
		v["build"].(map[string]any)["base_images"].(map[string]any)["caddy"] = "caddy:latest"
	}, func(v map[string]any) {
		v["images"].(map[string]any)["backend"].(map[string]any)["index"] = "ghcr.io/foreign/app@sha256:" + strings.Repeat("b", 64)
	}, func(v map[string]any) {
		v["images"].(map[string]any)["web"].(map[string]any)["platform_digests"].(map[string]any)["linux/arm64"] = "sha256:" + strings.Repeat("c", 64)
	}, func(v map[string]any) {
		v["source"].(map[string]any)["archive_url"] = "https://foreign.invalid/archive.tar.gz"
	}} {
		var v map[string]any
		_ = json.Unmarshal(data, &v)
		mutate(v)
		encoded, _ := JSON(v)
		if _, err = DecodeManifestV1(encoded, m.Source.Repository); err == nil {
			t.Fatal("invalid deployment manifest accepted", string(encoded))
		}
	}
	if _, err = DecodeManifestV1(append(data, []byte(`{}`)...), m.Source.Repository); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err = DecodeManifestV1(data, "foreign/repository"); err == nil {
		t.Fatal("untrusted repository accepted")
	}
}
func deploymentBundleFixture(t *testing.T, m *ManifestV1, extra string, duplicate, omitUpdater bool) string {
	t.Helper()
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	members := append([]string{}, DeploymentRequiredFiles...)
	if !omitUpdater {
		members = append(members, "deploy/update.py")
	}
	members = append(members, "release-bundle.json", "backend/licenses/example/dependency/LICENSE")
	if extra != "" {
		members = append(members, extra)
	}
	if duplicate {
		members = append(members, "LICENSE")
	}
	for _, name := range members {
		data := []byte("inert fixture input\n")
		if name == "release-bundle.json" {
			data, _ = JSON(map[string]any{"schema_version": 1, "version": m.Version, "source_commit": m.Source.Commit, "payload_profile": m.PayloadProfile})
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Uid: 0, Gid: 0, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(compressed.Bytes())
	m.Bundle.SHA256 = hex.EncodeToString(hash[:])
	p := filepath.Join(t.TempDir(), m.Bundle.Name)
	if err := os.WriteFile(p, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestBundleV1ValidatesEveryInertMemberBeforeExtraction(t *testing.T) {
	for _, tc := range []struct {
		extra                  string
		duplicate, omit, valid bool
	}{{"", false, false, true}, {"backend/licenses/example/dependency/NOTICE", false, false, true}, {"web/static/licenses/foreign.js", false, false, false}, {"backend/licenses/example/dependency/not-license.go", false, false, false}, {"../escape", false, false, false}, {"/absolute", false, false, false}, {"", true, false, false}, {"", false, true, false}} {
		m := deploymentManifestFixture()
		p := deploymentBundleFixture(t, &m, tc.extra, tc.duplicate, tc.omit)
		files, err := ReadBundleV1(p, m)
		if (err == nil) != tc.valid {
			t.Fatalf("case %+v err=%v", tc, err)
		}
		if tc.valid && len(files) <= len(DeploymentRequiredFiles) {
			t.Fatal("members omitted")
		}
		m.Bundle.SHA256 = strings.Repeat("0", 64)
		if _, err = ReadBundleV1(p, m); err == nil {
			t.Fatal("bad digest accepted")
		}
	}
}
