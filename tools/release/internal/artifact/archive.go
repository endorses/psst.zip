// Package artifact provides bounded, deterministic release archives.
package artifact

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type File struct {
	Source string
	Mode   fs.FileMode
}

func HashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ValidPath(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "\\:\x00") && path.Clean(name) == name
}

// Archive exclusively creates a new archive and removes partial output on error.
func Archive(destination, prefix string, files map[string]File, epoch int64) (err error) {
	if !ValidPath(prefix) {
		return fmt.Errorf("invalid archive prefix")
	}
	var names []string
	for name := range files {
		if !ValidPath(name) {
			return fmt.Errorf("unsafe archive member")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	if strings.HasSuffix(destination, ".zip") {
		writer := zip.NewWriter(out)
		for _, name := range names {
			file := files[name]
			info, openErr := os.Lstat(file.Source)
			if openErr != nil {
				return openErr
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("archive source must be regular")
			}
			header := &zip.FileHeader{Name: prefix + "/" + name, Method: zip.Deflate}
			header.SetMode(file.Mode)
			header.SetModTime(time.Unix(max(epoch, 315532800), 0).UTC())
			sink, err := writer.CreateHeader(header)
			if err != nil {
				return err
			}
			if err := copyRegular(sink, file.Source, info.Size()); err != nil {
				return err
			}
		}
		return writer.Close()
	}
	zipped, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		return err
	}
	zipped.Header.OS = 255
	writer := tar.NewWriter(zipped)
	for _, name := range names {
		file := files[name]
		info, err := os.Lstat(file.Source)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("archive source must be regular")
		}
		if err := writer.WriteHeader(&tar.Header{Name: prefix + "/" + name, Mode: int64(file.Mode.Perm()), Size: info.Size(), ModTime: time.Unix(epoch, 0).UTC(), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return err
		}
		if err := copyRegular(writer, file.Source, info.Size()); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return zipped.Close()
}

func copyRegular(out io.Writer, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return fmt.Errorf("archive source changed")
	}
	count, err := io.CopyN(out, f, size)
	if err != nil {
		return err
	}
	var extra [1]byte
	n, end := f.Read(extra[:])
	if count != size || n != 0 || end != io.EOF {
		return fmt.Errorf("archive source changed")
	}
	return nil
}

type Limits struct {
	Compressed, Expanded, Member int64
	Members                      int
}

// ExtractTar requires an empty private destination and regular files only. It
// bounds decompression including tar headers/PAX metadata, not just file sizes.
func ExtractTar(reader io.Reader, destination string, limits Limits) error {
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("archive destination must be empty")
	}
	compressed := &io.LimitedReader{R: reader, N: limits.Compressed + 1}
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return err
	}
	defer gz.Close()
	expanded := &io.LimitedReader{R: gz, N: limits.Expanded + 1}
	archive := tar.NewReader(expanded)
	seen := map[string]bool{}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	for count := 0; ; count++ {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if count >= limits.Members || !ValidPath(header.Name) || seen[header.Name] || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > limits.Member || header.Mode&0o7000 != 0 || len(header.PAXRecords) != 0 {
			return fmt.Errorf("unsafe or oversized archive member")
		}
		seen[header.Name] = true
		name := filepath.FromSlash(header.Name)
		if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return err
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, archive, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	// Read through the gzip trailer to validate CRC and detect unbounded padding.
	if _, err := io.Copy(io.Discard, expanded); err != nil {
		return err
	}
	if compressed.N <= 0 || expanded.N <= 0 {
		return fmt.Errorf("archive exceeds byte limit")
	}
	return nil
}
