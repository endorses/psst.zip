package source

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func plainTar(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for name, raw := range files {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(raw)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		w.Write(raw)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func apkFixture(version string) []byte {
	return []byte("P:fixture\nV:" + version + "\nA:x86_64\nL:MIT\no:fixture\nc:" + strings.Repeat("b", 40) + "\nC:fixture-checksum\n\n")
}
func TestImageReplayRetainsLowerOriginsAndAppliesWhiteouts(t *testing.T) {
	first := plainTar(t, map[string][]byte{"lib/apk/db/installed": apkFixture("1.0-r0"), "app/licenses/stale.txt": []byte("old"), "app/server": []byte("binary")})
	second := plainTar(t, map[string][]byte{"lib/apk/db/installed": apkFixture("2.0-r0"), "app/licenses/.wh..wh..opq": {}, "app/licenses/new.txt": []byte("new")})
	config, _ := json.Marshal(map[string]any{"architecture": "amd64", "os": "linux", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{hash(first), hash(second)}}, "config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.version": "v1.2.3"}}})
	manifest, _ := json.Marshal([]any{map[string]any{"Config": "config.json", "RepoTags": []string{"fixture:latest"}, "Layers": []string{"first.tar", "second.tar"}}})
	saved := plainTar(t, map[string][]byte{"manifest.json": manifest, "config.json": config, "first.tar": first, "second.tar": second})
	name := filepath.Join(t.TempDir(), "saved.tar")
	os.WriteFile(name, saved, 0600)
	image, err := InspectSaved(name, hash(config))
	if err != nil {
		t.Fatal(err)
	}
	if len(image.Packages) != 2 || len(image.Databases) != 2 || image.Packages[0].Present || !image.Packages[1].Present || string(image.Files["app/licenses/new.txt"]) != "new" || image.Files["app/licenses/stale.txt"] != nil {
		t.Fatal("retained origins or final notice replay differ")
	}
	first[700] ^= 1
	changed := plainTar(t, map[string][]byte{"manifest.json": manifest, "config.json": config, "first.tar": first, "second.tar": second})
	os.WriteFile(name, changed, 0600)
	if _, err = InspectSaved(name, hash(config)); err == nil {
		t.Fatal("layer bytes changed without matching tested configuration")
	}
}
