package publication

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	releasegithub "github.com/endorses/psst.zip/tools/release/internal/github"
)

type publicReader struct{ client *http.Client }

func publicURL(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" && u.Port() != "443" || len(address) > 16<<10 {
		return false
	}
	switch u.Hostname() {
	case "api.github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}
func newPublicReader() *publicReader {
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConnsPerHost: 4}
	return &publicReader{&http.Client{Transport: transport, Timeout: 20 * time.Minute, CheckRedirect: func(request *http.Request, previous []*http.Request) error {
		if len(previous) > 3 || !publicURL(request.URL.String()) {
			return fmt.Errorf("public artifact redirect refused")
		}
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		return nil
	}}}
}
func (r *publicReader) close() { r.client.CloseIdleConnections() }
func (r *publicReader) get(ctx context.Context, address string, binary bool) (*http.Response, error) {
	if !publicURL(address) {
		return nil, fmt.Errorf("untrusted public artifact URL")
	}
	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, fmt.Errorf("public artifact request unavailable")
	}
	request.Header.Set("User-Agent", "psst.zip-release-readback")
	request.Header.Set("X-GitHub-Api-Version", releasegithub.APIVersion)
	accept := "application/vnd.github+json"
	if binary {
		accept = "application/octet-stream"
	}
	request.Header.Set("Accept", accept)
	response, err := r.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("anonymous artifact readback failed")
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, fmt.Errorf("published artifact is not anonymously available")
	}
	return response, nil
}
func (r *publicReader) verify(ctx context.Context, repository, tag string, releaseID int64, assets []releasegithub.Asset) error {
	base := "https://api.github.com/repos/" + repository + "/releases/"
	response, err := r.get(ctx, base+strconv.FormatInt(releaseID, 10), false)
	if err != nil {
		return err
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
	response.Body.Close()
	if readErr != nil || len(raw) > 16<<20 {
		return fmt.Errorf("public release response exceeds bounds")
	}
	var release releasegithub.Release
	if releasegithub.DecodeAPI(raw, &release) != nil || release.ID != releaseID || release.Tag != tag || release.Draft || !release.Immutable || !releasegithub.MatchAssets(release, assets) {
		return fmt.Errorf("anonymous immutable release identity or asset set differs")
	}
	for _, asset := range release.Assets {
		response, err = r.get(ctx, base+"assets/"+strconv.FormatInt(asset.ID, 10), true)
		if err != nil {
			return err
		}
		hash := sha256.New()
		size, readErr := io.Copy(hash, io.LimitReader(response.Body, asset.Size+1))
		response.Body.Close()
		if readErr != nil || size != asset.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != asset.Digest {
			return fmt.Errorf("anonymous release asset bytes differ from reviewed artifact")
		}
	}
	return nil
}
