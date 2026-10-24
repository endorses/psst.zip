package source

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

var publicHTTP = http.Client{Timeout: 180 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConnsPerHost: 4}, CheckRedirect: func(req *http.Request, previous []*http.Request) error {
	if len(previous) > 3 || req.URL.Scheme != "https" || req.URL.User != nil {
		return fmt.Errorf("unsafe public source redirect")
	}
	switch req.URL.Host {
	case "github.com", "release-assets.githubusercontent.com", "codeload.github.com":
	default:
		return fmt.Errorf("untrusted public source redirect")
	}
	req.Header.Del("Authorization")
	return nil
}}

func publicFetch(ctx context.Context, address string, limit int64) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid public source URL")
	}
	api := false
	switch u.Host {
	case "api.github.com":
		api = true
		if !strings.HasPrefix(u.Path, "/repos/") {
			return nil, fmt.Errorf("untrusted source API route")
		}
	case "raw.githubusercontent.com", "github.com", "codeload.github.com":
		if u.RawQuery != "" {
			return nil, fmt.Errorf("untrusted public source query")
		}
	default:
		return nil, fmt.Errorf("untrusted public source host")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "psst.zip-source-collector")
	req.Header.Set("Accept", "application/vnd.github+json")
	if api {
		token := os.Getenv("GH_TOKEN")
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	// A dedicated no-redirect client protects the API token. Other requests carry
	// no credentials and only public release CDN redirects are permitted.
	client := &publicHTTP
	if api {
		copy := publicHTTP
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return fmt.Errorf("source API redirect refused") }
		client = &copy
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("public source request failed")
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return nil, errSourceNotFound
	}
	if res.StatusCode != 200 || res.ContentLength > limit {
		return nil, fmt.Errorf("public source request failed or exceeds bounds")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || int64(len(raw)) > limit || len(raw) == 0 {
		return nil, fmt.Errorf("public source body failed or exceeds bounds")
	}
	return raw, nil
}

var errSourceNotFound = fmt.Errorf("exact source route not found")

func gitBlob(raw []byte) string {
	sum := sha1.New()
	fmt.Fprintf(sum, "blob %d\x00", len(raw))
	sum.Write(raw)
	return hex.EncodeToString(sum.Sum(nil))
}

type recipeEntry struct{ Name, Path, Type, SHA string }

func recipeFiles(ctx context.Context, origin, commit string, fetch Fetch) (string, map[string][]byte, error) {
	if !apkNameRE.MatchString(origin) || !commitRE.MatchString(commit) {
		return "", nil, fmt.Errorf("invalid exact APK recipe identity")
	}
	if fetch == nil {
		fetch = publicFetch
	}
	budget, remaining := int64(32<<20), 2048
	var descend func(string, []byte, int, map[string][]byte) error
	descend = func(relative string, raw []byte, depth int, files map[string][]byte) error {
		if depth > 6 {
			return fmt.Errorf("recipe directory depth exceeds limit")
		}
		var entries []recipeEntry
		if json.Unmarshal(raw, &entries) != nil || len(entries) > 512 {
			return fmt.Errorf("invalid recipe directory listing")
		}
		seen := map[string]bool{}
		for _, entry := range entries {
			if !safePath(entry.Name) || path.Base(entry.Name) != entry.Name || seen[entry.Name] || entry.Path != relative+"/"+entry.Name {
				return fmt.Errorf("unsafe or duplicate recipe tree entry")
			}
			seen[entry.Name] = true
			remaining--
			if remaining < 0 {
				return fmt.Errorf("recipe file count exceeds limit")
			}
			if entry.Type == "dir" {
				child, e := fetch(ctx, "https://api.github.com/repos/alpinelinux/aports/contents/"+entry.Path+"?ref="+commit, 8<<20)
				if e != nil {
					return e
				}
				if e = descend(entry.Path, child, depth+1, files); e != nil {
					return e
				}
			} else {
				if entry.Type != "file" || !commitRE.MatchString(entry.SHA) {
					return fmt.Errorf("unsupported recipe tree entry")
				}
				body, e := fetch(ctx, "https://raw.githubusercontent.com/alpinelinux/aports/"+commit+"/"+entry.Path, 8<<20)
				if e != nil {
					return e
				}
				budget -= int64(len(body))
				if budget < 0 || gitBlob(body) != entry.SHA {
					return fmt.Errorf("recipe original Git blob differs or exceeds bounds")
				}
				key := strings.TrimPrefix(entry.Path, strings.Split(relative, "/")[0]+"/"+origin+"/")
				if !safePath(key) {
					return fmt.Errorf("recipe relative path differs")
				}
				files[key] = body
			}
		}
		return nil
	}
	for _, repository := range []string{"main", "community"} {
		relative := repository + "/" + origin
		raw, e := fetch(ctx, "https://api.github.com/repos/alpinelinux/aports/contents/"+relative+"?ref="+commit, 8<<20)
		if e == errSourceNotFound {
			continue
		}
		if e != nil {
			return "", nil, e
		}
		files := map[string][]byte{}
		if e = descend(relative, raw, 0, files); e != nil {
			return "", nil, e
		}
		if len(files["APKBUILD"]) == 0 {
			return "", nil, fmt.Errorf("exact recipe lacks APKBUILD")
		}
		return repository, files, nil
	}
	return "", nil, fmt.Errorf("exact APK recipe not found")
}
