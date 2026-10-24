// Package oci validates bounded native OCI exports against tested image identities.
// It does not grant license, scanner, provenance or publication approval.
package oci

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

const (
	maxArchive  int64 = 2 << 30
	maxDocument int64 = 1 << 20
	maxLayer    int64 = 512 << 20
	maxMembers        = 256 + 8
	maxLayers         = 128
)

type member struct{ offset, size int64 }

// Archive keeps the validated file descriptor open. Close it after every layer
// reader has been closed. Separate layer readers may be consumed concurrently.
type Archive struct {
	file    *os.File
	initial os.FileInfo
	blobs   map[string]member
	docs    map[string][]byte
	image   Image
}

// OpenArchive verifies every encoded blob and uncompressed layer identity before
// returning a native image. It rejects external, attached and unreferenced data.
func OpenArchive(path string, opts Options) (_ *Archive, err error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect OCI archive: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxArchive {
		return nil, fmt.Errorf("OCI archive must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open OCI archive: %w", err)
	}
	a := &Archive{file: file, initial: info, blobs: map[string]member{}, docs: map[string][]byte{}}
	defer func() {
		if err != nil {
			_ = file.Close()
		}
	}()
	if err = a.unchanged(); err != nil {
		return nil, err
	}
	if err = a.scan(); err != nil {
		return nil, err
	}
	if err = a.graph(opts); err != nil {
		return nil, err
	}
	if err = a.verifyLayers(); err != nil {
		return nil, err
	}
	if err = a.verifyArchiveDigest(); err != nil {
		return nil, err
	}
	return a, nil
}

// Inspect returns independent metadata copies; callers cannot mutate the
// archive's validated descriptor graph through the result.
func (a *Archive) Inspect() Image {
	v := a.image
	v.Config = bytes.Clone(v.Config)
	v.RootFS = append([]string(nil), v.RootFS...)
	v.Layers = append([]Descriptor(nil), v.Layers...)
	v.Labels = make(map[string]string, len(a.image.Labels))
	for key, value := range a.image.Labels {
		v.Labels[key] = value
	}
	return v
}

// Close releases the file descriptor. It must follow all layer reader closes.
func (a *Archive) Close() error { return a.file.Close() }

// InspectArchive validates an archive without retaining an open file descriptor.
func InspectArchive(path string, opts Options) (Image, error) {
	a, err := OpenArchive(path, opts)
	if err != nil {
		return Image{}, err
	}
	v := a.Inspect()
	if err := a.Close(); err != nil {
		return Image{}, err
	}
	return v, nil
}

func (a *Archive) unchanged() error {
	info, err := a.file.Stat()
	if err != nil {
		return fmt.Errorf("stat OCI archive: %w", err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(a.initial, info) || info.Size() != a.initial.Size() || !info.ModTime().Equal(a.initial.ModTime()) {
		return fmt.Errorf("OCI archive changed during verification or use")
	}
	return nil
}

// Keep the original archive identity bound across validation/staging, including
// header or padding substitutions that preserve file size and timestamps.
func (a *Archive) verifyArchiveDigest() error {
	if err := a.unchanged(); err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(a.file, 0, a.initial.Size())); err != nil {
		return fmt.Errorf("rehash OCI archive: %w", err)
	}
	if digest(h) != a.image.ArchiveDigest {
		return fmt.Errorf("OCI archive changed during verification or staging")
	}
	return a.unchanged()
}

func digest(h hash.Hash) string { return "sha256:" + hex.EncodeToString(h.Sum(nil)) }
func hashBytes(raw []byte) string {
	h := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(h[:])
}

// scan checks each physical header before archive/tar can consume hidden PAX,
// GNU longname or sparse metadata. Layer tar bytes remain opaque at this boundary.
func (a *Archive) scan() error {
	h := sha256.New()
	r := io.TeeReader(a.file, h)
	var offset, payload int64
	seen := map[string]bool{}
	for {
		var block [512]byte
		n, err := io.ReadFull(r, block[:])
		if err == io.EOF && n == 0 {
			break
		}
		if err != nil {
			return fmt.Errorf("truncated OCI archive header")
		}
		offset += 512
		if bytes.Equal(block[:], make([]byte, 512)) {
			tail, err := io.ReadAll(io.LimitReader(r, maxDocument+1))
			if err != nil || int64(len(tail))+512 > maxDocument || len(bytes.Trim(tail, "\x00")) != 0 {
				return fmt.Errorf("OCI archive has trailing data")
			}
			offset += int64(len(tail))
			break
		}
		kind := block[156]
		if kind != tar.TypeReg && kind != tar.TypeRegA && kind != tar.TypeDir {
			return fmt.Errorf("OCI archive contains unsupported headers or entry types")
		}
		entry, err := tar.NewReader(bytes.NewReader(block[:])).Next()
		if err != nil || entry.Size < 0 || entry.Size > maxArchive || len(entry.PAXRecords) != 0 {
			return fmt.Errorf("invalid OCI archive header")
		}
		name := entry.Name
		if kind == tar.TypeDir {
			name = strings.TrimRight(name, "/")
		}
		if seen[name] || len(seen) >= maxMembers {
			return fmt.Errorf("duplicate or oversized OCI archive inventory")
		}
		seen[name] = true
		if kind == tar.TypeDir {
			if name != "blobs" && name != "blobs/sha256" || entry.Size != 0 {
				return fmt.Errorf("unexpected OCI directory")
			}
			continue
		}
		blob := strings.TrimPrefix(name, "blobs/sha256/")
		isBlob := name != blob && digestRE.MatchString("sha256:"+blob)
		if !isBlob && name != "oci-layout" && name != "index.json" {
			return fmt.Errorf("unsafe or unexpected OCI archive path")
		}
		payload += entry.Size
		if payload > maxArchive || !isBlob && entry.Size > maxDocument {
			return fmt.Errorf("OCI payload exceeds bounds")
		}
		memberHash := sha256.New()
		var content bytes.Buffer
		var sink io.Writer = memberHash
		if entry.Size <= maxDocument {
			sink = io.MultiWriter(memberHash, &content)
		}
		if _, err := io.CopyN(sink, r, entry.Size); err != nil {
			return fmt.Errorf("truncated OCI archive payload")
		}
		key := name
		if isBlob {
			key = "sha256:" + blob
			if digest(memberHash) != key {
				return fmt.Errorf("OCI blob checksum differs from its path")
			}
			a.blobs[key] = member{offset, entry.Size}
		}
		if entry.Size <= maxDocument {
			a.docs[key] = content.Bytes()
		}
		padding := (512 - entry.Size%512) % 512
		if _, err := io.CopyN(io.Discard, r, padding); err != nil {
			return fmt.Errorf("truncated OCI archive padding")
		}
		offset += entry.Size + padding
	}
	if offset != a.initial.Size() {
		return fmt.Errorf("OCI archive changed size during verification")
	}
	a.image.ArchiveDigest = digest(h)
	a.image.BlobCount = len(a.blobs)
	return nil
}

// OpenLayer returns the raw encoded layer. It rechecks the encoded digest while
// reading; Close drains an unread remainder and reports mutation/read errors.
// Callers must propagate both read and Close errors before using derived output.
func (a *Archive) OpenLayer(index int) (io.ReadCloser, error) {
	if index < 0 || index >= len(a.image.Layers) {
		return nil, fmt.Errorf("OCI layer index out of range")
	}
	return a.openBlob(a.image.Layers[index].Digest)
}

func (a *Archive) openBlob(expected string) (io.ReadCloser, error) {
	if err := a.unchanged(); err != nil {
		return nil, err
	}
	m, ok := a.blobs[expected]
	if !ok {
		return nil, fmt.Errorf("missing verified OCI blob")
	}
	return &verifiedReader{reader: io.NewSectionReader(a.file, m.offset, m.size), hash: sha256.New(), expected: expected, archive: a}, nil
}

type verifiedReader struct {
	reader   io.Reader
	hash     hash.Hash
	expected string
	archive  *Archive
	err      error
	closed   bool
}

func (r *verifiedReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, os.ErrClosed
	}
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.reader.Read(p)
	_, _ = r.hash.Write(p[:n])
	if err == io.EOF && digest(r.hash) != r.expected {
		err = fmt.Errorf("OCI blob changed after verification")
	}
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

func (r *verifiedReader) Close() error {
	if !r.closed {
		_, err := io.Copy(io.Discard, r)
		if r.err == nil {
			r.err = err
		}
		if r.err == nil {
			r.err = r.archive.unchanged()
		}
		r.closed = true
	}
	return r.err
}
