package source

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/notices"
)

// VerifyBackendNotices binds the distributed legal files to the authenticated
// original module ZIPs. The normalization recipe is the maintained Go code,
// rather than an executable old-generator hash pinned into another parser.
func VerifyBackendNotices(ctx context.Context, root string, source Context, modules Inventory, files map[string][]byte) (map[string]File, error) {
	return verifyBackendNotices(ctx, source, modules, files, func(name string) ([]byte, error) { return committed(ctx, root, source.Commit, name) })
}

// VerifyBackendSnapshot reuses the exact application Git snapshot for every
// original/distributed notice check, avoiding one Git process per notice.
func VerifyBackendSnapshot(ctx context.Context, source Context, application ApplicationVerification, modules Inventory, files map[string][]byte) (map[string]File, error) {
	if application.Source != source {
		return nil, fmt.Errorf("backend notices belong to another application snapshot")
	}
	return verifyBackendNotices(ctx, source, modules, files, func(name string) ([]byte, error) {
		raw, ok := application.Files[name]
		if !ok {
			return nil, fmt.Errorf("committed backend notice input missing")
		}
		return raw, nil
	})
}
func verifyBackendNotices(ctx context.Context, source Context, modules Inventory, files map[string][]byte, read func(string) ([]byte, error)) (map[string]File, error) {
	if modules.Source != source {
		return nil, fmt.Errorf("backend module collection belongs to another release")
	}
	raw, err := read("backend/licenses/dependency-inventory.json")
	if err != nil {
		return nil, err
	}
	var inventory notices.BackendInventory
	if err = contract.Decode(raw, MetadataLimit, &inventory); err != nil {
		return nil, err
	}
	mod, err := read("backend/go.mod")
	if err != nil {
		return nil, err
	}
	sums, err := read("backend/go.sum")
	if err != nil {
		return nil, err
	}
	if inventory.Inputs["go.mod"] != strings.TrimPrefix(hash(mod), "sha256:") || inventory.Inputs["go.sum"] != strings.TrimPrefix(hash(sums), "sha256:") {
		return nil, fmt.Errorf("backend legal input hashes differ from exact locks")
	}
	requirements, err := goRequirements(ctx, mod)
	if err != nil {
		return nil, err
	}
	if len(inventory.Modules) != len(requirements) {
		return nil, fmt.Errorf("backend legal module scope differs from declared inputs")
	}
	byName := map[string]Source{}
	for _, item := range modules.Sources {
		if item.Ecosystem != "go" || byName[item.Name].Name != "" {
			return nil, fmt.Errorf("invalid platform module sources")
		}
		byName[item.Name] = item
	}
	seen := map[string]bool{}
	result := map[string]File{}
	for _, module := range inventory.Modules {
		if requirements[module.Module] != module.Version || seen[module.Module] || byName[module.Module].Version != module.Version || len(module.Notices) == 0 {
			return nil, fmt.Errorf("backend legal module identity differs from original graph")
		}
		seen[module.Module] = true
		item := byName[module.Module]
		original := files[item.Original.Path]
		if fact(item.Original.Path, original) != item.Original {
			return nil, fmt.Errorf("backend legal original ZIP changed")
		}
		archive, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
		if err != nil {
			return nil, err
		}
		prefix := module.Module + "@" + module.Version + "/"
		expected := map[string]notices.BackendNotice{}
		hasLicense := false
		for _, member := range archive.File {
			if !strings.HasPrefix(member.Name, prefix) {
				return nil, fmt.Errorf("backend original legal ZIP root differs")
			}
			relative := strings.TrimPrefix(member.Name, prefix)
			lower := strings.ToLower(relative)
			if strings.Contains(relative, "/") || member.FileInfo().IsDir() || !member.Mode().IsRegular() {
				continue
			}
			legal := false
			for _, start := range []string{"license", "licence", "copying", "copyright", "notice", "sqlite-license"} {
				if strings.HasPrefix(lower, start) {
					legal = true
					if start == "license" || start == "licence" || start == "copying" {
						hasLicense = true
					}
					break
				}
			}
			if !legal {
				continue
			}
			if member.UncompressedSize64 > 8<<20 {
				return nil, fmt.Errorf("original backend notice exceeds bounds")
			}
			stream, err := member.Open()
			if err != nil {
				return nil, err
			}
			upstream, err := io.ReadAll(io.LimitReader(stream, (8<<20)+1))
			closeErr := stream.Close()
			if err != nil || closeErr != nil || !utf8.Valid(upstream) {
				return nil, fmt.Errorf("original backend legal bytes are not valid UTF-8 or failed ZIP integrity")
			}
			text := strings.ReplaceAll(strings.ReplaceAll(string(upstream), "\r\n", "\n"), "\r", "\n")
			lines := strings.Split(text, "\n")
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], " \t")
			}
			distributed := []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n")
			name := path.Join("dependencies", module.Module+"@"+module.Version, relative)
			actual, err := read("backend/licenses/" + name)
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(actual, distributed) {
				return nil, fmt.Errorf("distributed backend legal text differs from normalized original ZIP")
			}
			expected[name] = notices.BackendNotice{Path: name, SHA256: strings.TrimPrefix(hash(distributed), "sha256:"), UpstreamSHA256: strings.TrimPrefix(hash(upstream), "sha256:")}
			result[name] = fact(name, distributed)
		}
		if !hasLicense || len(expected) != len(module.Notices) {
			return nil, fmt.Errorf("backend original legal coverage differs")
		}
		for _, notice := range module.Notices {
			if expected[notice.Path] != notice {
				return nil, fmt.Errorf("backend notice inventory differs from original/distributed byte relationship")
			}
			delete(expected, notice.Path)
		}
		if len(expected) != 0 {
			return nil, fmt.Errorf("backend notice inventory duplicated or omitted original legal member")
		}
	}
	return result, nil
}
func noticeFactList(facts map[string]File) []File {
	var names []string
	for name := range facts {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]File, 0, len(names))
	for _, name := range names {
		result = append(result, facts[name])
	}
	return result
}
