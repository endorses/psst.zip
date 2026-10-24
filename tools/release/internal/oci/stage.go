package oci

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// StageArchive exclusively creates a private canonical OCI layout containing
// only the validated runnable child, config and layers. It copies content bytes,
// never archive paths, ownership or permissions, and fsyncs every output file.
// The caller must sync containing directories before beginning remote mutation.
func StageArchive(path, destination string, opts Options) (result Image, err error) {
	a, err := OpenArchive(path, opts)
	if err != nil {
		return Image{}, err
	}
	created := false
	defer func() {
		closeErr := a.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil && created {
			_ = os.RemoveAll(destination)
		}
	}()
	if err = os.Mkdir(destination, 0o700); err != nil {
		return Image{}, fmt.Errorf("create private OCI destination: %w", err)
	}
	created = true
	root, err := os.OpenRoot(destination)
	if err != nil {
		return Image{}, err
	}
	defer root.Close()
	for _, name := range []string{"blobs", "blobs/sha256"} {
		if err = root.Mkdir(name, 0o700); err != nil {
			return Image{}, err
		}
	}
	image := a.Inspect()
	used := map[string]bool{image.ManifestDigest: true, image.ConfigDigest: true}
	for _, layer := range image.Layers {
		used[layer.Digest] = true
	}
	digests := make([]string, 0, len(used))
	for digest := range used {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	for _, digest := range digests {
		source, err := a.openBlob(digest)
		if err != nil {
			return Image{}, err
		}
		writeErr := writeStaged(root, "blobs/sha256/"+strings.TrimPrefix(digest, "sha256:"), source)
		closeErr := source.Close()
		if writeErr != nil {
			return Image{}, writeErr
		}
		if closeErr != nil {
			return Image{}, closeErr
		}
	}
	index := map[string]any{"schemaVersion": 2, "mediaType": IndexMediaType, "manifests": []any{map[string]any{"mediaType": image.ManifestMediaType, "digest": image.ManifestDigest, "size": image.ManifestSize, "platform": map[string]string{"os": "linux", "architecture": image.Architecture}}}}
	for name, value := range map[string]any{"oci-layout": map[string]string{"imageLayoutVersion": "1.0.0"}, "index.json": index} {
		raw, err := contract.JSON(value)
		if err != nil {
			return Image{}, err
		}
		if err = writeStaged(root, name, bytes.NewReader(raw)); err != nil {
			return Image{}, err
		}
	}
	if err = a.verifyArchiveDigest(); err != nil {
		return Image{}, err
	}
	return image, nil
}

func writeStaged(root *os.Root, name string, source io.Reader) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create staged OCI content: %w", err)
	}
	_, err = io.Copy(file, source)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("write staged OCI content: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged OCI content: %w", closeErr)
	}
	return nil
}
