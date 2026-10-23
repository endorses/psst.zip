package source

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type npmInput struct {
	Path, Name, Version, Resolved, Integrity string
	Dev, Optional                            bool
}
type npmResolved struct {
	raw     []byte
	sources []Source
}

func npmID(input npmInput) string {
	id := sha256.Sum256([]byte(input.Path + "@" + input.Version))
	return "npm-" + hex.EncodeToString(id[:])
}

func lockedNpm(lock []byte) ([]npmInput, error) {
	object, err := jsonObject(lock)
	if err != nil {
		return nil, err
	}
	var version int
	if json.Unmarshal(object["lockfileVersion"], &version) != nil || version != 3 {
		return nil, fmt.Errorf("unsupported npm lock schema")
	}
	var packages map[string]struct {
		Version, Resolved, Integrity string
		Link, Dev, Optional          bool
	}
	if json.Unmarshal(object["packages"], &packages) != nil || len(packages) < 2 || len(packages) > 4096 {
		return nil, fmt.Errorf("invalid npm lock graph")
	}
	var names []string
	for name := range packages {
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var inputs []npmInput
	for _, name := range names {
		p := packages[name]
		index := strings.LastIndex(name, "node_modules/")
		if !safePath(name) || index < 0 || p.Link || p.Version == "" {
			return nil, fmt.Errorf("unresolved or linked npm input")
		}
		packageName := name[index+len("node_modules/"):]
		if !regexp.MustCompile(`^(?:@[a-z0-9_.-]+/)?[a-z0-9_.-]+$`).MatchString(packageName) {
			return nil, fmt.Errorf("invalid npm package name")
		}
		u, err := url.Parse(p.Resolved)
		if err != nil || u.Scheme != "https" || u.Host != "registry.npmjs.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, ".tgz") {
			return nil, fmt.Errorf("unsupported npm original URL")
		}
		if !strings.HasPrefix(p.Integrity, "sha512-") {
			return nil, fmt.Errorf("npm input lacks exact SHA512 integrity")
		}
		sum, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(p.Integrity, "sha512-"))
		if err != nil || len(sum) != 64 {
			return nil, fmt.Errorf("invalid npm SHA512 integrity")
		}
		inputs = append(inputs, npmInput{name, packageName, p.Version, p.Resolved, p.Integrity, p.Dev, p.Optional})
	}
	return inputs, nil
}
func inspectNpm(raw []byte, input npmInput) (Source, error) {
	sum := sha512.Sum512(raw)
	if input.Integrity != "sha512-"+base64.StdEncoding.EncodeToString(sum[:]) || int64(len(raw)) > OriginalLimit {
		return Source{}, fmt.Errorf("npm original integrity differs")
	}
	members, selected, err := inspectArchive(raw, archivePolicy{Expanded: OriginalLimit, Member: OriginalLimit, Members: 100000, Original: true, Capture: func(name string) bool { return path.Base(name) == "package.json" && strings.Count(name, "/") == 1 }})
	if err != nil {
		return Source{}, err
	}
	prefix := ""
	for _, m := range members {
		if prefix == "" {
			prefix = strings.Split(m.Path, "/")[0]
		}
		if m.Kind == "symlink" || !(m.Path == prefix || strings.HasPrefix(m.Path, prefix+"/")) {
			return Source{}, fmt.Errorf("npm original contains a link or multiple roots")
		}
	}
	metadata := selected[prefix+"/package.json"]
	value, err := jsonObject(metadata)
	if err != nil {
		return Source{}, err
	}
	var name, version string
	json.Unmarshal(value["name"], &name)
	json.Unmarshal(value["version"], &version)
	if name != input.Name || version != input.Version {
		return Source{}, fmt.Errorf("npm original identity differs from lock")
	}
	archive := "npm/" + strings.TrimPrefix(hash(raw), "sha256:") + ".tgz"
	item := Source{ID: npmID(input), Ecosystem: "npm", Name: name, Version: version, Origin: "npm", URL: input.Resolved, Original: fact(archive, raw), LicensePaths: []string{}, BuildInputPaths: []string{"package.json"}, PreferredSourcePaths: []string{}, ObservedInputs: map[string]File{"package.json": fact("package.json", metadata)}, LockPath: input.Path, Integrity: input.Integrity, Development: input.Dev, Optional: input.Optional}
	for _, member := range members {
		if member.Kind != "file" {
			continue
		}
		relative := strings.TrimPrefix(member.Path, prefix+"/")
		base := strings.ToLower(path.Base(relative))
		for _, legal := range []string{"license", "licence", "copying", "copyright", "notice", "patents"} {
			if strings.HasPrefix(base, legal) {
				item.LicensePaths = append(item.LicensePaths, relative)
				item.ObservedInputs[relative] = File{relative, member.SHA256, member.Size}
				break
			}
		}
	}
	return item, nil
}

// collectNpm acquires each content identity once, even when multiple locked paths
// resolve to it. Four bounded workers reduce network latency without a second
// dependency graph resolution or package-script execution.
func collectNpm(ctx context.Context, lock []byte, fetch Fetch) ([]Source, map[string][]byte, error) {
	inputs, err := lockedNpm(lock)
	if err != nil {
		return nil, nil, err
	}
	groups := map[string][]npmInput{}
	var keys []string
	for _, input := range inputs {
		key := input.Resolved + "\n" + input.Integrity
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], input)
	}
	sort.Strings(keys)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	results := make([]npmResolved, len(keys))
	var workers sync.WaitGroup
	var first error
	var once sync.Once
	var total atomic.Int64
	for range 4 {
		workers.Go(func() {
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				list := groups[keys[index]]
				raw, err := fetch(ctx, list[0].Resolved, OriginalLimit)
				if err == nil && total.Add(int64(len(raw))) > OfferingLimit {
					err = fmt.Errorf("common npm originals exceed offering limit")
				}
				resolved := npmResolved{raw: raw}
				if err == nil {
					for _, input := range list {
						var item Source
						item, err = inspectNpm(raw, input)
						if err != nil {
							break
						}
						resolved.sources = append(resolved.sources, item)
					}
				}
				if err != nil {
					once.Do(func() { first = err; cancel() })
					return
				}
				results[index] = resolved
			}
		})
	}
enqueue:
	for index := range keys {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()
	if first != nil {
		return nil, nil, first
	}
	if err = ctx.Err(); err != nil {
		return nil, nil, err
	}
	var sources []Source
	files := map[string][]byte{}
	for _, resolved := range results {
		for _, item := range resolved.sources {
			sources = append(sources, item)
			files[item.Original.Path] = resolved.raw
		}
	}
	return sources, files, nil
}
