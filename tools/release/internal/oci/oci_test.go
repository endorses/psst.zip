package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

type fixture struct {
	opts       Options
	files      map[string][]byte
	config     map[string]any
	layer      []byte
	encoded    []byte
	layerType  string
	depth      int
	manifest   map[string]any
	manifestID string
}

func jsonBytes(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := contract.JSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func desc(raw []byte, mediaType string) map[string]any {
	return map[string]any{"mediaType": mediaType, "digest": hashBytes(raw), "size": len(raw)}
}

func newFixture(t *testing.T, arch string, compressed bool) *fixture {
	t.Helper()
	f := &fixture{opts: Options{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40), Component: "backend", Platform: "linux/" + arch}, layerType: LayerMediaType}
	var layer bytes.Buffer
	tarWriter := tar.NewWriter(&layer)
	body := []byte("retained " + arch + " layer bytes")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "app/payload", Mode: 0o600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(body); err != nil {
		t.Fatal(err)
	}
	// Image layers legitimately contain links and whiteouts. The OCI container
	// policy must not be confused with the source consumer's filesystem replay.
	if err := tarWriter.WriteHeader(&tar.Header{Name: "app/link", Typeflag: tar.TypeSymlink, Linkname: "payload", Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	f.layer = layer.Bytes()
	f.encoded = bytes.Clone(f.layer)
	if compressed {
		var encoded bytes.Buffer
		writer := gzip.NewWriter(&encoded)
		if _, err := writer.Write(f.layer); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		f.encoded, f.layerType = encoded.Bytes(), GzipLayerType
	}
	f.config = map[string]any{"os": "linux", "architecture": arch, "created": "2026-10-24T00:00:00Z", "history": []any{map[string]any{"created_by": "fixture"}}, "config": map[string]any{"User": "10001", "Env": []string{"FIXTURE=1"}, "Labels": map[string]string{"org.opencontainers.image.title": "psst.zip backend", "org.opencontainers.image.source": "https://github.com/endorses/psst.zip", "org.opencontainers.image.version": f.opts.Version, "org.opencontainers.image.revision": f.opts.Commit, "org.opencontainers.image.licenses": "AGPL-3.0-only", "zip.psst.source.archive": "https://github.com/endorses/psst.zip/archive/" + f.opts.Commit + ".tar.gz"}}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{hashBytes(f.layer)}}}
	f.rebuild(t)
	return f
}

func (f *fixture) rebuild(t *testing.T) {
	t.Helper()
	f.files = map[string][]byte{"oci-layout": jsonBytes(t, map[string]string{"imageLayoutVersion": "1.0.0"})}
	add := func(raw []byte) { f.files["blobs/sha256/"+strings.TrimPrefix(hashBytes(raw), "sha256:")] = raw }
	config := jsonBytes(t, f.config)
	f.opts.TestedConfig = hashBytes(config)
	f.manifest = map[string]any{"schemaVersion": 2, "mediaType": ManifestMediaType, "config": desc(config, ConfigMediaType), "layers": []any{desc(f.encoded, f.layerType)}}
	add(config)
	add(f.encoded)
	f.rewriteManifest(t)
}

func (f *fixture) rewriteManifest(t *testing.T) {
	t.Helper()
	if f.manifestID != "" {
		delete(f.files, "blobs/sha256/"+strings.TrimPrefix(f.manifestID, "sha256:"))
	}
	current := jsonBytes(t, f.manifest)
	f.manifestID = hashBytes(current)
	media := ManifestMediaType
	for i := 0; ; i++ {
		f.files["blobs/sha256/"+strings.TrimPrefix(hashBytes(current), "sha256:")] = current
		child := desc(current, media)
		child["platform"] = map[string]string{"os": "linux", "architecture": strings.TrimPrefix(f.opts.Platform, "linux/")}
		index := jsonBytes(t, map[string]any{"schemaVersion": 2, "mediaType": IndexMediaType, "manifests": []any{child}})
		if i == f.depth {
			f.files["index.json"] = index
			break
		}
		current, media = index, IndexMediaType
	}
}

func (f *fixture) archive(t *testing.T, extra ...*tar.Header) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "image.tar")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	for _, directory := range []string{"blobs/", "blobs/sha256/"} {
		if err := writer.WriteHeader(&tar.Header{Name: directory, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	names := make([]string, 0, len(f.files))
	for name := range f.files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := f.files[name]
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o777, Uid: 1234, Gid: 5678, Size: int64(len(body)), Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	for _, header := range extra {
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := writer.Write(bytes.Repeat([]byte{'x'}, int(header.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestNativeArchiveAndCanonicalStage(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			f := newFixture(t, arch, arch == "amd64")
			f.depth = 3 // Four descriptors: the maximum accepted native nesting.
			f.rebuild(t)
			path := f.archive(t)
			a, err := OpenArchive(path, f.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			image := a.Inspect()
			if image.ConfigDigest != f.opts.TestedConfig || image.ManifestDigest != f.manifestID || image.Architecture != arch || image.BlobCount != 6 || len(image.Layers) != 1 || image.RootFS[0] != hashBytes(f.layer) {
				t.Fatalf("wrong validated image: %+v", image)
			}
			raw, err := os.ReadFile(path)
			if err != nil || image.ArchiveDigest != hashBytes(raw) {
				t.Fatal("archive digest mismatch", err)
			}
			// Metadata copies cannot alter future reads or canonical staging.
			image.Config[0] = '!'
			image.RootFS[0] = "wrong"
			image.Labels["org.opencontainers.image.version"] = "wrong"
			image.Layers[0].Digest = "wrong"
			var group sync.WaitGroup
			for range 2 {
				group.Go(func() {
					reader, err := a.OpenLayer(0)
					if err != nil {
						t.Error(err)
						return
					}
					got, readErr := io.ReadAll(reader)
					closeErr := reader.Close()
					if readErr != nil || closeErr != nil || !bytes.Equal(got, f.encoded) {
						t.Error("encoded layer changed", readErr, closeErr)
					}
				})
			}
			group.Wait()
			destination := filepath.Join(t.TempDir(), "canonical")
			staged, err := StageArchive(path, destination, f.opts)
			if err != nil || staged.ManifestDigest != f.manifestID {
				t.Fatal(staged, err)
			}
			count := 0
			if err := filepath.WalkDir(destination, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if entry.IsDir() {
					if info.Mode().Perm() != 0o700 {
						return fmt.Errorf("non-private staged directory")
					}
				} else {
					count++
					if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
						return fmt.Errorf("unsafe staged file mode")
					}
					body, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					if strings.Contains(path, "blobs/sha256/") && "sha256:"+filepath.Base(path) != hashBytes(body) {
						return fmt.Errorf("staged content digest differs")
					}
				}
				return nil
			}); err != nil || count != 5 {
				t.Fatal("canonical stage contains nested/extra data", count, err)
			}
			if _, err := StageArchive(path, destination, f.opts); err == nil {
				t.Fatal("replaced existing stage")
			}
		})
	}
}

func TestRejectsSubstitutedGraphAndIdentity(t *testing.T) {
	cases := map[string]func(*fixture){
		"tested-config": func(f *fixture) { f.opts.TestedConfig = "sha256:" + strings.Repeat("b", 64) },
		"source-labels": func(f *fixture) { f.opts.Commit = strings.Repeat("b", 40) },
		"component":     func(f *fixture) { f.opts.Component = "web" },
		"platform":      func(f *fixture) { f.opts.Platform = "linux/arm64" },
		"blob-checksum": func(f *fixture) {
			f.files["blobs/sha256/"+strings.TrimPrefix(f.opts.TestedConfig, "sha256:")] = []byte("changed")
		},
		"missing-blob": func(f *fixture) { delete(f.files, "blobs/sha256/"+strings.TrimPrefix(f.opts.TestedConfig, "sha256:")) },
		"unreferenced": func(f *fixture) {
			f.files["blobs/sha256/"+strings.TrimPrefix(hashBytes([]byte("unused")), "sha256:")] = []byte("unused")
		},
		"descriptor-size": func(f *fixture) { f.manifest["config"].(map[string]any)["size"] = 1; f.rewriteManifest(t) },
		"external-url": func(f *fixture) {
			f.manifest["config"].(map[string]any)["urls"] = []string{"https://example.test/config"}
			f.rewriteManifest(t)
		},
		"inline-data": func(f *fixture) { f.manifest["config"].(map[string]any)["data"] = "eA=="; f.rewriteManifest(t) },
		"artifact-subject": func(f *fixture) {
			f.manifest["subject"] = desc([]byte("other"), ManifestMediaType)
			f.rewriteManifest(t)
		},
		"too-deep":         func(f *fixture) { f.depth = 4; f.rebuild(t) },
		"layer-count":      func(f *fixture) { f.config["rootfs"].(map[string]any)["diff_ids"] = []string{}; f.rebuild(t) },
		"layer-media-type": func(f *fixture) { f.layerType = "application/vnd.oci.image.layer.v1.tar+zstd"; f.rebuild(t) },
		"substituted-layer": func(f *fixture) {
			f.encoded = []byte("different valid content-addressed layer")
			f.layerType = LayerMediaType
			f.rebuild(t)
		},
		"gzip-crc":       func(f *fixture) { f.encoded[len(f.encoded)-1] ^= 1; f.rebuild(t) },
		"duplicate-json": func(f *fixture) { f.files["index.json"] = []byte(`{"schemaVersion":2,"schemaVersion":2}`) },
		"extra-runnable-child": func(f *fixture) {
			var index map[string]any
			if err := json.Unmarshal(f.files["index.json"], &index); err != nil {
				t.Fatal(err)
			}
			index["manifests"] = append(index["manifests"].([]any), index["manifests"].([]any)[0])
			f.files["index.json"] = jsonBytes(t, index)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "amd64", true)
			mutate(f)
			destination := filepath.Join(t.TempDir(), "must-not-exist")
			if _, err := StageArchive(f.archive(t), destination, f.opts); err == nil {
				t.Fatal("accepted substituted OCI graph")
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatal("invalid input created a staging directory", err)
			}
		})
	}
}

func TestRejectsOuterHeadersPathsTruncationAndPadding(t *testing.T) {
	f := newFixture(t, "amd64", true)
	for name, header := range map[string]*tar.Header{
		"symlink":         {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"hardlink":        {Name: "link", Typeflag: tar.TypeLink, Linkname: "index.json"},
		"device":          {Name: "device", Typeflag: tar.TypeChar},
		"duplicate":       {Name: "index.json", Size: 1},
		"escape":          {Name: "../escape", Size: 1},
		"extra-directory": {Name: "other/", Typeflag: tar.TypeDir},
		"pax":             {Name: "index.json", PAXRecords: map[string]string{"comment": "hidden"}, Format: tar.FormatPAX},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := InspectArchive(f.archive(t, header), f.opts); err == nil {
				t.Fatal("accepted unsafe outer archive")
			}
		})
	}
	for _, suffix := range [][]byte{[]byte("hidden second archive"), make([]byte, maxDocument+1)} {
		path := f.archive(t)
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write(suffix)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
		if _, err := InspectArchive(path, f.opts); err == nil {
			t.Fatal("accepted hidden/oversized trailing data")
		}
	}
	path := f.archive(t)
	if err := os.Truncate(path, 513); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectArchive(path, f.opts); err == nil {
		t.Fatal("accepted truncated archive")
	}
	if err := os.Truncate(path, maxArchive+1); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectArchive(path, f.opts); err == nil {
		t.Fatal("accepted oversized archive")
	}
	link := filepath.Join(t.TempDir(), "archive-link")
	if err := os.Symlink(f.archive(t), link); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectArchive(link, f.opts); err == nil {
		t.Fatal("accepted archive symlink")
	}
}

func TestLayerReaderDetectsMutationEvenWithRestoredTimestamp(t *testing.T) {
	f := newFixture(t, "amd64", false)
	path := f.archive(t)
	a, err := OpenArchive(path, f.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	reader, err := a.OpenLayer(0)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteAt([]byte("X"), a.blobs[a.image.Layers[0].Digest].offset)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if err := os.Chtimes(path, time.Now(), a.initial.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("layer substitution escaped digest check")
	}
	if err := reader.Close(); err == nil {
		t.Fatal("close hid the layer verification failure")
	}
	if err := a.verifyArchiveDigest(); err == nil {
		t.Fatal("archive identity changed despite preserved timestamps")
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestBoundsCompressedLayerExpansion(t *testing.T) {
	f := newFixture(t, "amd64", true)
	var encoded bytes.Buffer
	writer, err := gzip.NewWriterLevel(&encoded, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(writer, zeros{}, maxLayer+1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	f.encoded = encoded.Bytes()
	f.rebuild(t)
	if _, err := InspectArchive(f.archive(t), f.opts); err == nil || !strings.Contains(err.Error(), "exceed bounds") {
		t.Fatal("uncompressed limit not enforced", err)
	}
}

func TestPairedIndexExactBytesPlatformsAndNoAttachments(t *testing.T) {
	am, arm := newFixture(t, "amd64", true), newFixture(t, "arm64", false)
	children := []any{}
	for _, f := range []*fixture{am, arm} {
		child := desc(jsonBytes(t, f.manifest), ManifestMediaType)
		child["platform"] = map[string]string{"os": "linux", "architecture": strings.TrimPrefix(f.opts.Platform, "linux/"), "variant": ""}
		children = append(children, child)
	}
	index := map[string]any{"schemaVersion": 2, "mediaType": IndexMediaType, "manifests": children}
	raw := jsonBytes(t, index)
	record := contract.ImageV1{Index: "ghcr.io/endorses/psst-zip-backend@" + hashBytes(raw), PlatformDigests: map[string]string{"linux/amd64": am.manifestID, "linux/arm64": arm.manifestID}}
	if err := ValidateIndex(raw, record); err != nil {
		t.Fatal(err)
	}
	if err := ValidateIndex(append(raw, ' '), record); err == nil {
		t.Fatal("did not bind exact raw bytes")
	}
	for _, mutation := range []func(map[string]any){
		func(v map[string]any) { v["manifests"] = append(v["manifests"].([]any), v["manifests"].([]any)[0]) },
		func(v map[string]any) {
			v["manifests"].([]any)[1].(map[string]any)["platform"] = map[string]string{"os": "linux", "architecture": "amd64"}
		},
		func(v map[string]any) {
			v["manifests"].([]any)[1].(map[string]any)["platform"] = map[string]string{"os": "unknown", "architecture": "unknown"}
		},
		func(v map[string]any) {
			v["manifests"].([]any)[1].(map[string]any)["platform"] = map[string]string{"os": "linux", "architecture": "arm64", "variant": "v9"}
		},
		func(v map[string]any) {
			v["manifests"].([]any)[0].(map[string]any)["urls"] = []string{"https://example.test/image"}
		},
		func(v map[string]any) { v["manifests"].([]any)[0].(map[string]any)["size"] = 0 },
	} {
		var changed map[string]any
		if err := json.Unmarshal(raw, &changed); err != nil {
			t.Fatal(err)
		}
		mutation(changed)
		bytes := jsonBytes(t, changed)
		record.Index = "ghcr.io/endorses/psst-zip-backend@" + hashBytes(bytes)
		if err := ValidateIndex(bytes, record); err == nil {
			t.Fatal("accepted substituted paired index")
		}
	}
}
