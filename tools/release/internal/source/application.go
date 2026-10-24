package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/process"
)

const ApplicationLimit int64 = 256 << 20

// ApplicationVerification shares the exact Git snapshot bytes with notice and
// browser consumers. Files excludes link metadata; nothing is extracted or run.
type ApplicationVerification struct {
	Source                   Context           `json:"source"`
	Asset                    File              `json:"asset"`
	PrefixedGitArchiveSHA256 string            `json:"prefixed_git_archive_sha256"`
	Files                    map[string][]byte `json:"-"`
}

func applicationSnapshot(ctx context.Context, root string, source Context) ([]byte, int64, error) {
	if err := source.Validate(); err != nil {
		return nil, 0, err
	}
	runner := process.Runner{Dir: root, CleanEnv: true, Env: map[string]string{"PATH": os.Getenv("PATH")}}
	var out bytes.Buffer
	if err := runner.RunTo(ctx, 60*time.Second, &applicationBoundedWriter{Writer: &out, remaining: ApplicationLimit}, "git", "archive", "--format=tar", "--prefix=psst.zip-"+source.Version+"/", source.Commit); err != nil {
		return nil, 0, err
	}
	stamp, err := runner.Output(ctx, 20*time.Second, "git", "show", "-s", "--format=%ct", source.Commit)
	if err != nil {
		return nil, 0, err
	}
	epoch, err := strconv.ParseInt(strings.TrimSpace(string(stamp)), 10, 64)
	if err != nil || epoch < 0 {
		return nil, 0, fmt.Errorf("invalid application source commit timestamp")
	}
	if out.Len() == 0 {
		return nil, 0, fmt.Errorf("empty committed source archive")
	}
	return out.Bytes(), epoch, nil
}

type applicationBoundedWriter struct {
	io.Writer
	remaining int64
}

func (w *applicationBoundedWriter) Write(raw []byte) (int, error) {
	if int64(len(raw)) > w.remaining {
		return 0, fmt.Errorf("application source exceeds bounds")
	}
	n, err := w.Writer.Write(raw)
	w.remaining -= int64(n)
	return n, err
}
func applicationMembers(raw []byte, source Context) (map[string][]byte, error) {
	reader := tar.NewReader(bytes.NewReader(raw))
	files := map[string][]byte{}
	seen := map[string]bool{}
	prefix := "psst.zip-" + source.Version + "/"
	var total int64
	for count := 0; ; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			if header.PAXRecords["comment"] != source.Commit {
				return nil, fmt.Errorf("Git archive revision metadata differs")
			}
			continue
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safePath(name) || seen[name] || count > 100000 || header.Size < 0 || header.Size > OriginalLimit {
			return nil, fmt.Errorf("unsafe committed application source member")
		}
		seen[name] = true
		if name == strings.TrimSuffix(prefix, "/") && header.Typeflag == tar.TypeDir {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			return nil, fmt.Errorf("application source root differs")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return nil, fmt.Errorf("invalid source directory")
			}
		case tar.TypeSymlink: // Retained unchanged by exact Git byte comparison, never followed.
			if header.Size != 0 || header.Linkname == "" {
				return nil, fmt.Errorf("invalid committed source link metadata")
			}
		case tar.TypeReg, tar.TypeRegA:
			total += header.Size
			if total > ApplicationLimit {
				return nil, fmt.Errorf("application source payload exceeds bounds")
			}
			raw, err := io.ReadAll(io.LimitReader(reader, ApplicationLimit+1))
			if err != nil || int64(len(raw)) != header.Size {
				return nil, fmt.Errorf("committed application source was truncated")
			}
			files[strings.TrimPrefix(name, prefix)] = raw
		default:
			return nil, fmt.Errorf("unsupported application source entry")
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("empty committed application source")
	}
	return files, nil
}

// CreateApplication exclusively writes a new source offering from Git objects;
// untracked files, working changes and local configuration cannot enter it.
func CreateApplication(ctx context.Context, root string, source Context, output string) (ApplicationVerification, error) {
	var result ApplicationVerification
	if filepath.Base(output) != "psst.zip-source-"+source.Version+".tar.gz" {
		return result, fmt.Errorf("application source asset name differs")
	}
	archive, epoch, err := applicationSnapshot(ctx, root, source)
	if err != nil {
		return result, err
	}
	files, err := applicationMembers(archive, source)
	if err != nil {
		return result, err
	}
	var out bytes.Buffer
	writer, err := gzip.NewWriterLevel(&applicationBoundedWriter{Writer: &out, remaining: ApplicationLimit}, gzip.BestCompression)
	if err != nil {
		return result, err
	}
	writer.Header.ModTime = time.Unix(epoch, 0)
	writer.Header.OS = 255
	if _, err = writer.Write(archive); err != nil {
		return result, err
	}
	if err = writer.Close(); err != nil {
		return result, err
	}
	if err = writeNew(output, out.Bytes()); err != nil {
		return result, err
	}
	return ApplicationVerification{Source: source, Asset: fact(filepath.Base(output), out.Bytes()), PrefixedGitArchiveSHA256: hash(archive), Files: files}, nil
}

// ReadApplication binds the entire expanded tar (including Git modes, archive
// attributes, links and padding) to Git objects. Compression bytes may differ
// between implementations; the final published digest still binds exact bytes.
func ReadApplication(ctx context.Context, root string, source Context, name string) (ApplicationVerification, error) {
	var result ApplicationVerification
	if filepath.Base(name) != "psst.zip-source-"+source.Version+".tar.gz" {
		return result, fmt.Errorf("application source asset name differs")
	}
	raw, err := readRegular(name, ApplicationLimit)
	if err != nil {
		return result, err
	}
	compressed := bytes.NewReader(raw)
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		return result, err
	}
	defer reader.Close()
	reader.Multistream(false)
	archive, err := io.ReadAll(io.LimitReader(reader, ApplicationLimit+1))
	if err != nil || int64(len(archive)) > ApplicationLimit || compressed.Len() != 0 {
		return result, fmt.Errorf("application source expansion is invalid or contains trailing data")
	}
	expected, epoch, err := applicationSnapshot(ctx, root, source)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(archive, expected) || !reader.Header.ModTime.Equal(time.Unix(epoch, 0)) {
		return result, fmt.Errorf("application source differs from selected committed Git archive")
	}
	files, err := applicationMembers(archive, source)
	if err != nil {
		return result, err
	}
	return ApplicationVerification{Source: source, Asset: fact(filepath.Base(name), raw), PrefixedGitArchiveSHA256: hash(expected), Files: files}, nil
}
