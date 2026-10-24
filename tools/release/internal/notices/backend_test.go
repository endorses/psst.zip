package notices

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBackendLegalNormalizationAndOriginalIdentity(t *testing.T) {
	original := []byte("Copyright Example\r\n  Terms \t\r\n\r\n")
	distributed, err := normalizedLegal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(distributed, []byte("Copyright Example\n  Terms\n")) || Hash(original) == Hash(distributed) {
		t.Fatal("normalization lost content or original identity")
	}
	if _, err := normalizedLegal([]byte{0xff}); err == nil {
		t.Fatal("invalid legal text accepted")
	}
}

func TestBackendNoticeReadbackRejectsCorruptionAndObsoleteText(t *testing.T) {
	output := t.TempDir()
	name := "dependencies/example.org/library@v1.0.0/LICENSE"
	files := map[string][]byte{name: []byte("terms\n"), "dependency-inventory.json": []byte("{}\n")}
	if err := writeBackendFiles(output, files, false); err != nil {
		t.Fatal(err)
	}
	if err := writeBackendFiles(output, files, true); err != nil {
		t.Fatal(err)
	}
	license := filepath.Join(output, filepath.FromSlash(name))
	if err := os.WriteFile(license, []byte("altered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeBackendFiles(output, files, true); err == nil {
		t.Fatal("changed distributed legal text accepted")
	}
	if err := writeBackendFiles(output, files, false); err != nil {
		t.Fatal(err)
	}
	obsolete := filepath.Join(filepath.Dir(license), "OBSOLETE")
	if err := os.WriteFile(obsolete, []byte("old terms\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeBackendFiles(output, files, true); err == nil {
		t.Fatal("obsolete terms accepted")
	}
	if err := writeBackendFiles(output, files, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(obsolete); !os.IsNotExist(err) {
		t.Fatal("obsolete dependency notice was not retired")
	}
}
