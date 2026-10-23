// Package lanassets serves the matching, offline browser interface embedded at build time.
package lanassets

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

//go:embed all:site
var content embed.FS

// Metadata records the source and exact static asset inventory. The metadata
// itself is excluded from Files to avoid a circular digest.
type Metadata struct {
	Product       string            `json:"product"`
	Revision      string            `json:"revision"`
	Digest        string            `json:"asset_digest"`
	Source        string            `json:"source"`
	SourceArchive string            `json:"source_archive"`
	SourceDirty   bool              `json:"source_dirty"`
	InputDigest   string            `json:"input_digest"`
	Files         map[string]string `json:"files"`
}

func Info() Metadata {
	var metadata Metadata
	data, _ := content.ReadFile("site/licenses/SOURCE.json")
	_ = json.Unmarshal(data, &metadata)
	return metadata
}

// Validate refuses a binary built without its matching browser assets.
func Validate() error {
	metadata := Info()
	if metadata.Product != "psst.zip" || len(metadata.Files) == 0 || metadata.Files["index.html"] == "" {
		return errors.New("LAN browser assets are missing; rebuild with make build")
	}
	names := make([]string, 0, len(metadata.Files))
	for name := range metadata.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	inventory := sha256.New()
	for _, name := range names {
		expected := metadata.Files[name]
		if !fs.ValidPath(name) {
			return errors.New("invalid LAN browser asset inventory")
		}
		data, err := content.ReadFile("site/" + name)
		digest := sha256.Sum256(data)
		if err != nil || hex.EncodeToString(digest[:]) != expected {
			return errors.New("LAN browser assets do not match their inventory; rebuild with make build")
		}
		_, _ = inventory.Write([]byte(name + "\x00" + expected + "\n"))
	}
	if hex.EncodeToString(inventory.Sum(nil)) != metadata.Digest {
		return errors.New("LAN browser inventory digest is invalid; rebuild with make build")
	}
	return nil
}

// Handler exposes static assets and the two public transfer screens, without
// directory indexes or hosted application routes. API routing belongs to the host.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) || r.URL.RawPath != "" {
			http.NotFound(w, r)
			return
		}
		data, err := content.ReadFile("site/" + name)
		if err != nil {
			parts := strings.Split(name, "/")
			if (len(parts) == 2 && (parts[0] == "d" || parts[0] == "u")) || name == "legal" {
				name = "index.html"
				data, err = content.ReadFile("site/" + name)
			}
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}
