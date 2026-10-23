package browser

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
)

// ReadCapture accepts only the regular USTAR members emitted by the Node
// capture. Check physical headers before archive/tar can consume extension data.
// Files stay in memory for the Git/npm/image comparisons; no extraction occurs.
func ReadCapture(name string) (map[string][]byte, File, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, File{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumTotal+64<<20 {
		return nil, File{}, fmt.Errorf("browser capture must be a bounded regular archive")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, File{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, File{}, fmt.Errorf("browser capture changed")
	}
	h := sha256.New()
	r := io.TeeReader(f, h)
	files := map[string][]byte{}
	var total, consumed int64
	for {
		var block [512]byte
		if _, err := io.ReadFull(r, block[:]); err != nil {
			return nil, File{}, fmt.Errorf("truncated browser capture")
		}
		consumed += 512
		if bytes.Equal(block[:], make([]byte, 512)) {
			tail, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
			consumed += int64(len(tail))
			if err != nil || len(tail) < 512 || len(tail) > 1<<20 || len(bytes.Trim(tail, "\x00")) != 0 {
				return nil, File{}, fmt.Errorf("browser capture has invalid trailing data")
			}
			break
		}
		if block[156] != tar.TypeReg && block[156] != tar.TypeRegA {
			return nil, File{}, fmt.Errorf("browser capture contains links or extension headers")
		}
		header, err := tar.NewReader(bytes.NewReader(block[:])).Next()
		if err != nil || !safe(header.Name) || header.Size < 0 || header.Size > maximumFile || len(header.PAXRecords) != 0 || len(files) >= 50000 {
			return nil, File{}, fmt.Errorf("invalid browser capture member")
		}
		if _, exists := files[header.Name]; exists {
			return nil, File{}, fmt.Errorf("duplicate browser capture member")
		}
		total += header.Size
		if total > maximumTotal {
			return nil, File{}, fmt.Errorf("browser capture exceeds expanded bound")
		}
		data := make([]byte, header.Size)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, File{}, fmt.Errorf("truncated browser capture member")
		}
		padding := (512 - header.Size%512) % 512
		if _, err := io.CopyN(io.Discard, r, padding); err != nil {
			return nil, File{}, fmt.Errorf("truncated browser capture padding")
		}
		consumed += header.Size + padding
		files[header.Name] = data
	}
	after, err := f.Stat()
	if err != nil || consumed != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, File{}, fmt.Errorf("browser capture changed during verification")
	}
	return files, File{"sha256:" + hex.EncodeToString(h.Sum(nil)), consumed}, nil
}

// WriteCapture keeps the selected actual builder bytes, not a second extraction
// tree or a second JSON representation of the same payload.
func WriteCapture(name string, files map[string][]byte) (result File, err error) {
	var total int64
	var names []string
	for name, body := range files {
		total += int64(len(body))
		if !safe(name) || len(body) > maximumFile || len(files) > 50000 || total > maximumTotal {
			return result, fmt.Errorf("invalid browser capture payload")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	h := sha256.New()
	w := tar.NewWriter(io.MultiWriter(f, h))
	for _, name := range names {
		body := files[name]
		if err = w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			_ = f.Close()
			return result, err
		}
		if _, err = w.Write(body); err != nil {
			_ = f.Close()
			return result, err
		}
	}
	if err = w.Close(); err == nil {
		err = f.Sync()
	}
	info, statErr := f.Stat()
	closeErr := f.Close()
	if err != nil {
		return result, err
	}
	if statErr != nil {
		return result, statErr
	}
	if closeErr != nil {
		return result, closeErr
	}
	return File{"sha256:" + hex.EncodeToString(h.Sum(nil)), info.Size()}, nil
}
