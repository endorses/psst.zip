package source

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/process"
)

var legalNameRE = regexp.MustCompile(`(?i)^(?:LICEN[CS]E|COPYING|COPYRIGHT|NOTICE|PATENTS|AUTHORS)(?:[._-].*)?$`)
var nestedArchiveRE = regexp.MustCompile(`\.(?:tar\.(?:gz|bz2|xz)|tgz|zip)$`)
var fixtureHashes = map[string]string{"busybox-1.37.0/testsuite/unzip_bad_lzma_1.zip": "sha256:6917569008ed8665c736b1d5b2dbbf8d863edac2478a1f05e998d535dea5b017", "busybox-1.37.0/testsuite/unzip_bad_lzma_2.zip": "sha256:e41016555fae6a69548ace9acbafb65c060b9b2c9066724724e4082aaf3446a7"}

type legalBudget struct {
	expanded int64
	members  int
	notices  int64
}
type LegalDocuments struct {
	Documents map[string][]byte
	Fixtures  map[string]string
}

func sourceLegal(ctx context.Context, raw []byte, name string) (LegalDocuments, error) {
	result := LegalDocuments{map[string][]byte{}, map[string]string{}}
	budget := &legalBudget{2 << 30, 100000, 64 << 20}
	err := walkLegal(ctx, raw, name, "", 0, budget, &result)
	return result, err
}
func walkLegal(ctx context.Context, raw []byte, name, prefix string, depth int, budget *legalBudget, result *LegalDocuments) error {
	if depth > 4 || int64(len(raw)) > OriginalLimit {
		return fmt.Errorf("nested source archive exceeds bounds")
	}
	seen := map[string]bool{}
	account := func(name string, size int64) error {
		clean := strings.TrimSuffix(name, "/")
		for strings.HasPrefix(clean, "./") {
			clean = strings.TrimPrefix(clean, "./")
		}
		if clean == "." || clean == "" {
			return nil
		}
		if !safePath(clean) || seen[clean] || size < 0 || size > OriginalLimit {
			return fmt.Errorf("unsafe source archive member")
		}
		seen[clean] = true
		budget.expanded -= size
		budget.members--
		if budget.expanded < 0 || budget.members < 0 {
			return fmt.Errorf("source archive expansion exceeds bounds")
		}
		return nil
	}
	document := func(name string, raw []byte) error {
		budget.notices -= int64(len(raw))
		if budget.notices < 0 || len(raw) > 8<<20 {
			return fmt.Errorf("original notices exceed bounds")
		}
		result.Documents[prefix+name] = raw
		return nil
	}
	if len(raw) >= 4 && bytes.Equal(raw[:4], []byte{'P', 'K', 3, 4}) {
		archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			if depth > 1 && fixtureHashes[name] == hash(raw) {
				result.Fixtures[name] = hash(raw)
				return nil
			}
			return fmt.Errorf("original source ZIP failed to parse")
		}
		for _, entry := range archive.File {
			if err = account(entry.Name, int64(entry.UncompressedSize64)); err != nil {
				return err
			}
			if entry.FileInfo().IsDir() || !entry.Mode().IsRegular() || !legalNameRE.MatchString(path.Base(entry.Name)) {
				continue
			}
			if entry.UncompressedSize64 > 8<<20 {
				return fmt.Errorf("source notice exceeds limit")
			}
			stream, e := entry.Open()
			if e != nil {
				return e
			}
			body, e := io.ReadAll(io.LimitReader(stream, (8<<20)+1))
			closeErr := stream.Close()
			if e != nil || closeErr != nil {
				return fmt.Errorf("source ZIP notice failed integrity check")
			}
			if e = document(entry.Name, body); e != nil {
				return e
			}
		}
		return nil
	}
	var source io.Reader = bytes.NewReader(raw)
	var closeSource func() error
	switch {
	case len(raw) > 2 && bytes.Equal(raw[:2], []byte{0x1f, 0x8b}):
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		defer gz.Close()
		source = gz
	case len(raw) > 3 && string(raw[:3]) == "BZh":
		source = bzip2.NewReader(source)
	case len(raw) > 6 && bytes.Equal(raw[:6], []byte{0xfd, '7', 'z', 'X', 'Z', 0}):
		// xz is a format decoder only: no source executable/scripts are run. The
		// pipe and process share cancellation and both compressed/expanded bounds.
		decoderCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		reader, writer := io.Pipe()
		runner := process.Runner{CleanEnv: true, Env: map[string]string{"PATH": "/usr/local/bin:/usr/bin:/bin"}}
		private, err := os.MkdirTemp("", "psst-source-xz-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(private)
		input := filepath.Join(private, "original.xz")
		if err = os.WriteFile(input, raw, 0600); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() {
			e := runner.RunTo(decoderCtx, 2*time.Minute, writer, "xz", "--decompress", "--stdout", "--single-stream", input)
			writer.CloseWithError(e)
			done <- e
		}()
		source = reader
		closeSource = func() error { reader.Close(); cancel(); return <-done }
		defer func() {
			if closeSource != nil {
				closeSource()
			}
		}()
	}
	limit := &io.LimitedReader{R: source, N: budget.expanded + 1}
	archive := tar.NewReader(limit)
	count := 0
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if count == 0 && depth > 1 && fixtureHashes[name] == hash(raw) {
				result.Fixtures[name] = hash(raw)
				return nil
			}
			return fmt.Errorf("original source TAR failed to parse")
		}
		count++
		if err = account(header.Name, header.Size); err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		base := path.Base(header.Name)
		if legalNameRE.MatchString(base) {
			if header.Size > 8<<20 {
				return fmt.Errorf("source notice exceeds limit")
			}
			body, e := io.ReadAll(io.LimitReader(archive, (8<<20)+1))
			if e != nil || int64(len(body)) != header.Size {
				return fmt.Errorf("source notice read failed")
			}
			if e = document(header.Name, body); e != nil {
				return e
			}
		} else if nestedArchiveRE.MatchString(header.Name) {
			body, e := io.ReadAll(io.LimitReader(archive, OriginalLimit+1))
			if e != nil || int64(len(body)) != header.Size {
				return fmt.Errorf("nested source original read failed")
			}
			if e = walkLegal(ctx, body, header.Name, prefix+header.Name+"::", depth+1, budget, result); e != nil {
				return e
			}
		}
	}
	if count == 0 {
		return fmt.Errorf("empty original source archive")
	}
	if _, err := io.Copy(io.Discard, limit); err != nil || limit.N <= 0 {
		return fmt.Errorf("source archive hidden expansion exceeds bounds")
	}
	if closeSource != nil {
		err := closeSource()
		closeSource = nil
		if err != nil {
			return err
		}
	}
	return nil
}
func retainedDocuments(prefix string, documents map[string][]byte) ([]string, map[string]File, map[string][]byte) {
	var labels []string
	for label := range documents {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	paths := []string{}
	facts := map[string]File{}
	files := map[string][]byte{}
	for _, label := range labels {
		body := documents[label]
		name := prefix + "/" + strings.TrimPrefix(hash(body), "sha256:") + ".txt"
		if _, exists := files[name]; !exists {
			paths = append(paths, name)
		}
		facts[label] = fact(name, body)
		files[name] = body
	}
	return paths, facts, files
}
