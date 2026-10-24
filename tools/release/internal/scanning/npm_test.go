package scanning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func TestCommonLockReadbackBindsEveryRawByteAndExplicitSource(t *testing.T) {
	directory := t.TempDir()
	source := Source{"endorses/psst.zip", "v1.2.3", strings.Repeat("a", 40)}
	image := "docker.io/library/node@sha256:" + strings.Repeat("b", 64)
	inputs := map[string]string{"web/package.json": hash([]byte("manifest")), "web/package-lock.json": hash([]byte("lock"))}
	record := NPMMeasurement{SchemaVersion: 1, Kind: "common-npm-lock-scanner-measurement", Source: source, Execution: "common-lock", Platform: "linux/amd64", SourceInputs: inputs, Builder: Builder{Reference: image, Config: "sha256:" + strings.Repeat("c", 64), Architecture: "amd64", Daemon: "x86_64", Layers: []string{"sha256:" + strings.Repeat("d", 64)}}, Started: "2026-10-24T00:00:00Z", Completed: "2026-10-24T00:00:01Z", RawFiles: map[string]string{}}
	for _, name := range npmFiles {
		data := []byte("retained raw fixture " + name)
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
		record.RawFiles[name] = hash(data)
	}
	data, _ := contract.JSON(record)
	file := filepath.Join(directory, "common-npm-audit.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadNPM(directory, source, inputs, image); err != nil {
		t.Fatal(err)
	}
	otherLock := map[string]string{"web/package.json": inputs["web/package.json"], "web/package-lock.json": hash([]byte("another lock"))}
	if _, err := ReadNPM(directory, source, otherLock, image); err == nil {
		t.Fatal("audit from another lock accepted")
	}
	var incomplete map[string]any
	json.Unmarshal(data, &incomplete)
	delete(incomplete, "publication_authorized")
	partial, _ := contract.JSON(incomplete)
	os.WriteFile(file, partial, 0o600)
	if _, err := ReadNPM(directory, source, inputs, image); err == nil {
		t.Fatal("incomplete receipt accepted")
	}
	os.WriteFile(file, data, 0o600)
	os.WriteFile(filepath.Join(directory, "npm-audit.json"), []byte("altered"), 0o600)
	if _, err := ReadNPM(directory, source, inputs, image); err == nil {
		t.Fatal("altered audit response accepted")
	}
}
