package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string, kind byte, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	writer := tar.NewWriter(gz)
	size := int64(len(payload))
	if kind != tar.TypeReg {
		size = 0
	}
	if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: kind, Mode: 0o644, Size: size, Linkname: "../escape"}); err != nil {
		t.Fatal(err)
	}
	if kind == tar.TypeReg {
		if _, err := writer.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestExtractionBoundsAndPaths(t *testing.T) {
	limits := Limits{Compressed: 1 << 20, Expanded: 1 << 20, Member: 1024, Members: 8}
	for _, test := range []struct {
		name    string
		kind    byte
		payload []byte
	}{{"../escape", tar.TypeReg, []byte("x")}, {"safe\\bad", tar.TypeReg, []byte("x")}, {"safe/link", tar.TypeSymlink, nil}, {"safe/large", tar.TypeReg, make([]byte, 1025)}} {
		if ExtractTar(bytes.NewReader(fixture(t, test.name, test.kind, test.payload)), t.TempDir(), limits) == nil {
			t.Fatalf("unsafe fixture accepted: %s", test.name)
		}
	}
	data := fixture(t, "safe/file", tar.TypeReg, []byte("bytes"))
	destination := t.TempDir()
	if err := ExtractTar(bytes.NewReader(data), destination, limits); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "safe/file")); err != nil || string(got) != "bytes" {
		t.Fatalf("wrong payload: %q %v", got, err)
	}
	limits.Expanded = 64
	if ExtractTar(bytes.NewReader(data), t.TempDir(), limits) == nil {
		t.Fatal("decompression/header bound ignored")
	}
}

func TestArchiveIsDeterministicAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".tar.gz", ".zip"} {
		first, second := filepath.Join(dir, "first"+ext), filepath.Join(dir, "second"+ext)
		files := map[string]File{"regular": {Source: source, Mode: 0o644}}
		if err := Archive(first, "release", files, 1700000000); err != nil {
			t.Fatal(err)
		}
		if err := Archive(second, "release", files, 1700000000); err != nil {
			t.Fatal(err)
		}
		a, _ := os.ReadFile(first)
		b, _ := os.ReadFile(second)
		if !bytes.Equal(a, b) {
			t.Fatal("archive is nondeterministic")
		}
		if Archive(first, "release", files, 1700000000) == nil {
			t.Fatal("existing release overwritten")
		}
	}
}
