package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

type Member struct {
	Path           string `json:"path"`
	Kind           string `json:"kind"`
	Mode           int64  `json:"mode"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256,omitempty"`
	LinkTarget     string `json:"link_target,omitempty"`
	ResolvedTarget string `json:"resolved_target,omitempty"`
	MetadataOnly   bool   `json:"source_fixture_metadata_only,omitempty"`
}
type archivePolicy struct {
	Expanded, Member int64
	Members          int
	Prefix, Commit   string
	Links            map[string]string
	Selected         map[string]bool
	Capture          func(string) bool
	Original         bool
}

// inspectArchive never extracts or follows original source links. It bounds the
// entire decompressed stream, including metadata/header/padding bytes.
func inspectArchive(raw []byte, p archivePolicy) ([]Member, map[string][]byte, error) {
	compressed := bytes.NewReader(raw)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, nil, err
	}
	defer gz.Close()
	gz.Multistream(false)
	expanded := &io.LimitedReader{R: gz, N: p.Expanded + 1}
	reader := tar.NewReader(expanded)
	var members []Member
	selected := map[string][]byte{}
	seen := map[string]Member{}
	var lastPadding int64
	for {
		before := expanded.N
		h, err := reader.Next()
		if err == io.EOF {
			if before-expanded.N < lastPadding+1024 {
				return nil, nil, fmt.Errorf("incomplete source tar terminator")
			}
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			if !p.Original || h.Size > 128 || len(h.PAXRecords) != 1 || h.PAXRecords["comment"] != p.Commit {
				return nil, nil, fmt.Errorf("unsupported original global metadata")
			}
			continue
		}
		name := h.Name
		if h.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if !safePath(name) || h.Size < 0 || h.Size > p.Member || h.Mode&07000 != 0 || len(members) >= p.Members {
			return nil, nil, fmt.Errorf("unsafe or oversized source archive member")
		}
		if _, ok := seen[name]; ok {
			return nil, nil, fmt.Errorf("duplicate source archive member")
		}
		for key, value := range h.PAXRecords {
			if !p.Original || key != "comment" || value != p.Commit {
				return nil, nil, fmt.Errorf("unreviewed source archive metadata rewrite")
			}
		}
		if p.Prefix != "" && name != p.Prefix && !strings.HasPrefix(name, p.Prefix+"/") {
			return nil, nil, fmt.Errorf("source archive root differs from full commit")
		}
		if h.Typeflag != tar.TypeSymlink && h.Linkname != "" {
			return nil, nil, fmt.Errorf("unexpected source link name")
		}
		lastPadding = (512 - h.Size%512) % 512
		member := Member{Path: name, Mode: h.Mode, Size: h.Size}
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			member.Kind = "file"
			data, err := io.ReadAll(io.LimitReader(reader, p.Member+1))
			if err != nil {
				return nil, nil, err
			}
			if int64(len(data)) != h.Size {
				return nil, nil, fmt.Errorf("source member size differs")
			}
			member.SHA256 = hash(data)
			relative := strings.TrimPrefix(name, p.Prefix+"/")
			if p.Selected[relative] || p.Selected[name] || p.Capture != nil && p.Capture(relative) {
				selected[relative] = data
			}
		case tar.TypeDir:
			if !p.Original || h.Size != 0 {
				return nil, nil, fmt.Errorf("unexpected source offering directory")
			}
			member.Kind = "directory"
		case tar.TypeSymlink:
			if !p.Original || h.Size != 0 || h.Linkname == "" || path.IsAbs(h.Linkname) || strings.ContainsAny(h.Linkname, "\\") || strings.IndexFunc(h.Linkname, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
				return nil, nil, fmt.Errorf("unsupported original source link")
			}
			target := path.Clean(path.Join(path.Dir(name), h.Linkname))
			if target != p.Prefix && !strings.HasPrefix(target, p.Prefix+"/") {
				return nil, nil, fmt.Errorf("original source link escapes commit root")
			}
			member.Kind = "symlink"
			member.LinkTarget = h.Linkname
			member.ResolvedTarget = target
		default:
			return nil, nil, fmt.Errorf("unsupported source archive entry type")
		}
		seen[name] = member
		members = append(members, member)
	}
	// Tar stops at its terminator; consume and validate any remaining padding,
	// gzip checksum and compressed end, rejecting concatenation or hidden data.
	tail, err := io.ReadAll(expanded)
	if err != nil {
		return nil, nil, err
	}
	if expanded.N <= 0 || compressed.Len() != 0 {
		return nil, nil, fmt.Errorf("source expansion exceeds limit or contains concatenated payload")
	}
	for _, b := range tail {
		if b != 0 {
			return nil, nil, fmt.Errorf("hidden source tar payload")
		}
	}
	if len(members) == 0 {
		return nil, nil, fmt.Errorf("empty source archive")
	}
	links := map[string]bool{}
	for i, member := range members {
		for parent := path.Dir(member.Path); parent != "."; parent = path.Dir(parent) {
			if item, ok := seen[parent]; ok && item.Kind != "directory" {
				return nil, nil, fmt.Errorf("source member has a nondirectory ancestor")
			}
		}
		if member.Kind != "symlink" {
			continue
		}
		relative := strings.TrimPrefix(member.Path, p.Prefix+"/")
		target, ok := seen[member.ResolvedTarget]
		if pinned, reviewed := p.Links[relative]; reviewed {
			if pinned != member.LinkTarget || !ok || target.Kind != "directory" || member.ResolvedTarget != p.Prefix+"/"+strings.Split(relative, "/test/")[0] {
				return nil, nil, fmt.Errorf("reviewed package-self source fixture differs")
			}
			members[i].MetadataOnly = true
			links[relative] = true
		} else if !ok || target.Kind == "symlink" || member.Path == member.ResolvedTarget || strings.HasPrefix(member.Path, member.ResolvedTarget+"/") {
			return nil, nil, fmt.Errorf("original source link target is absent, recursive or linked")
		}
	}
	if len(links) != len(p.Links) {
		return nil, nil, fmt.Errorf("reviewed source fixture link missing")
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Path < members[j].Path })
	return members, selected, nil
}

func offering(files map[string][]byte) ([]byte, error) {
	var output bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	gzipWriter.Header.OS = 255
	writer := tar.NewWriter(gzipWriter)
	names := make([]string, 0, len(files))
	var total int64
	for name, raw := range files {
		if !safePath(name) {
			return nil, fmt.Errorf("unsafe source offering path")
		}
		names = append(names, name)
		total += int64(len(raw))
		if total > OfferingLimit {
			return nil, fmt.Errorf("source offering exceeds limit")
		}
	}
	sort.Strings(names)
	for _, name := range names {
		raw := files[name]
		if err = writer.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(raw)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return nil, err
		}
		if _, err = writer.Write(raw); err != nil {
			return nil, err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	if err = gzipWriter.Close(); err != nil {
		return nil, err
	}
	if int64(output.Len()) > OfferingLimit {
		return nil, fmt.Errorf("compressed source offering exceeds limit")
	}
	return output.Bytes(), nil
}
