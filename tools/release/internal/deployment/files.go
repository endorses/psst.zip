package deployment

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/artifact"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// Protected rejects symbolic links at every component, and group/other writers.
// Installed private records are root-owned mode 0600; directory roots use 0700.
func Protected(path string, directory, executable, private bool) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("protected path must be absolute")
	}
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return fmt.Errorf("protected path is unavailable")
		}
		if info.Mode()&os.ModeSymlink != 0 || !ownedByRoot(info) || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("protected path contains a symlink or untrusted writer")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if directory != info.IsDir() || !directory && !info.Mode().IsRegular() {
		return fmt.Errorf("invalid protected file type")
	}
	if !directory && private && !executable && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("protected configuration must have mode 0600")
	}
	if executable && info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("verification hook is not executable")
	}
	return nil
}
func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := Protected(path, true, false, false); err != nil {
		return err
	}
	i, err := os.Stat(path)
	if err != nil {
		return err
	}
	return expect(i.Mode().Perm()&0o077 == 0, "deployment state directory must have mode 0700")
}
func nonce() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func atomicJSON(path string, v any) (err error) {
	data, err := contract.JSON(v)
	if err != nil {
		return err
	}
	n, err := nonce()
	if err != nil {
		return err
	}
	tmp := path + ".new-" + n
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxJSON {
		return fmt.Errorf("invalid or oversized JSON file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxJSON+1))
	if err != nil {
		return err
	}
	if target, ok := v.(*map[string]any); ok {
		var raw json.RawMessage
		if err := contract.Decode(data, maxJSON, &raw); err != nil {
			return err
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		return d.Decode(target)
	}
	return contract.Decode(data, maxJSON, v)
}
func load(path string) (Object, error) {
	var v Object
	err := readJSON(path, &v)
	if err == nil && v == nil {
		err = fmt.Errorf("expected JSON object")
	}
	return v, err
}
func exists(path string) bool                 { _, err := os.Lstat(path); return err == nil }
func fingerprint(path string) (string, error) { return artifact.HashFile(path) }

// VerifyArchive reads every byte before restore. Docker image archives and local
// Caddy state may contain ordinary directories but never links or devices.
func VerifyArchive(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := tar.NewReader(f)
	seen := set()
	count := 0
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("truncated checkpoint archive")
		}
		n := strings.TrimPrefix(h.Name, "./")
		if h.Name == "." || h.Name == "./" {
			n = "."
		}
		if strings.HasPrefix(h.Name, "/") || strings.ContainsAny(h.Name, "\\\x00") || has(strings.Split(h.Name, "/"), "..") || seen[h.Name] || n == "" {
			return fmt.Errorf("unsafe checkpoint archive path")
		}
		seen[h.Name] = true
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("unsupported checkpoint entry; preserve it for operator review")
		}
		mask := int64(0o7000)
		if h.Typeflag == tar.TypeDir {
			mask = 0o4000
		}
		if h.Mode&mask != 0 {
			return fmt.Errorf("unsafe checkpoint entry permissions")
		}
		if _, err := io.CopyN(io.Discard, r, h.Size); err != nil {
			return fmt.Errorf("truncated checkpoint archive")
		}
		count++
	}
	return expect(count > 0, "empty checkpoint archive")
}
func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("storage has unsupported symlink")
		}
		if !d.IsDir() {
			i, e := d.Info()
			if e != nil {
				return e
			}
			if !i.Mode().IsRegular() {
				return fmt.Errorf("storage has unsupported file type")
			}
			total += i.Size()
		}
		return nil
	})
	return total, err
}
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	e := out.Close()
	if err != nil {
		return err
	}
	return e
}

func boundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid or oversized deployment input")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("deployment input exceeds size limit")
	}
	return data, nil
}
