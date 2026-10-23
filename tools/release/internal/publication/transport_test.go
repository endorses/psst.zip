package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	releasegithub "github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

type commandFixture func(context.Context, time.Duration, ...string) ([]byte, error)

func (f commandFixture) Output(ctx context.Context, limit time.Duration, args ...string) ([]byte, error) {
	return f(ctx, limit, args...)
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAnonymousReleaseReadbackAuthenticatesEveryByteWithoutCredentials(t *testing.T) {
	payload := []byte("disposable release bytes")
	asset := releasegithub.Asset{Name: "source.tar.gz", SHA256: strings.TrimPrefix(digest(payload), "sha256:"), Size: int64(len(payload))}
	release := releasegithub.Release{ID: 42, Tag: "v1.2.3", Immutable: true, Assets: []releasegithub.ReleaseAsset{{ID: 7, Name: asset.Name, Size: asset.Size, Digest: digest(payload), State: "uploaded"}}}
	corrupt := false
	reader := &publicReader{&http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatal("anonymous readback attached credentials")
		}
		var data []byte
		if strings.HasSuffix(request.URL.Path, "/releases/42") {
			data, _ = json.Marshal(release)
		} else if strings.HasSuffix(request.URL.Path, "/releases/assets/7") {
			data = payload
			if corrupt {
				data = []byte("substitution")
			}
		} else {
			return nil, errors.New("unexpected public path")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})}}
	if err := reader.verify(context.Background(), "endorses/psst.zip", "v1.2.3", 42, []releasegithub.Asset{asset}); err != nil {
		t.Fatal(err)
	}
	corrupt = true
	if reader.verify(context.Background(), "endorses/psst.zip", "v1.2.3", 42, []releasegithub.Asset{asset}) == nil {
		t.Fatal("corrupt anonymous asset accepted")
	}
	for _, address := range []string{"http://api.github.com/x", "https://api.github.com.evil.invalid/x", "https://user@api.github.com/x", "https://api.github.com:8443/x"} {
		if publicURL(address) {
			t.Fatal("untrusted public route accepted")
		}
	}
}
func TestRegistryPreflightRefusesExistingTagsAndUncertainAbsence(t *testing.T) {
	inputs := Inputs{Binding: releasegithub.Binding{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}, Manifest: contract.ManifestV1{Images: map[string]contract.ImageV1{}}}
	for _, component := range []string{"backend", "web"} {
		inputs.Manifest.Images[component] = contract.ImageV1{Index: "ghcr.io/endorses/psst-zip-" + component + "@sha256:" + strings.Repeat("b", 64), PlatformDigests: map[string]string{"linux/amd64": "sha256:" + strings.Repeat("c", 64), "linux/arm64": "sha256:" + strings.Repeat("d", 64)}}
	}
	exists, uncertain := false, false
	client := releasegithub.Client{Runner: commandFixture(func(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
		endpoint := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "users/") {
				endpoint = arg
			}
		}
		if endpoint == "users/endorses" {
			return []byte(`{"login":"endorses","type":"User"}`), nil
		}
		if strings.Contains(endpoint, "/versions?") {
			if exists {
				return []byte(`[{"name":"sha256:` + strings.Repeat("f", 64) + `","metadata":{"container":{"tags":["v1.2.3"]}}}]`), nil
			}
			return []byte(`[]`), nil
		}
		if uncertain {
			return nil, errors.New("private registry credential failure")
		}
		component := "backend"
		if strings.Contains(endpoint, "psst-zip-web") {
			component = "web"
		}
		return []byte("HTTP/2.0 200 OK\r\nContent-Type: application/json\r\n\r\n" + `{"name":"psst-zip-` + component + `","package_type":"container","visibility":"public","owner":{"login":"endorses"},"repository":{"full_name":"endorses/psst.zip"}}`), nil
	})}
	r := &registry{inputs: inputs, client: client}
	if err := r.preflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	exists = true
	if r.preflight(context.Background()) == nil {
		t.Fatal("existing immutable version tag adopted")
	}
	exists, uncertain = false, true
	r.initialize = true
	if r.preflight(context.Background()) == nil {
		t.Fatal("credential/network failure treated as package absence")
	}
}
func TestJournalMakesMutationUncertaintyDurableAndDiagnosticsExcludeDetails(t *testing.T) {
	state := t.TempDir()
	_ = os.Chmod(state, 0700)
	inputs := Inputs{Result: Result{Binding: "sha256:" + strings.Repeat("a", 64)}, Binding: releasegithub.Binding{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("b", 40), RunID: 12, Attempt: 2}}
	journalPath := filepath.Join(state, "v1.2.3.jsonl")
	j, err := newJournal(journalPath, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.mutate("push-backend-index", map[string]any{"private": "never export this field"}, func() error { return fmt.Errorf("private API response must stay private") }); err == nil || strings.Contains(err.Error(), "API response") {
		t.Fatal("uncertain mutation succeeded or exposed private response")
	}
	_ = j.file.Close()
	output := filepath.Join(state, "diagnostics.json")
	if err = Diagnostics(state, "endorses/psst.zip", "v1.2.3", inputs.Binding.Commit, 12, 2, "failure", output); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(output)
	if strings.Contains(string(data), "never export") || strings.Contains(string(data), "API response") || !strings.Contains(string(data), `"phase": "uncertain"`) {
		t.Fatal("diagnostic projection leaked details or lost uncertainty")
	}
	if _, err = newJournal(journalPath, inputs); err == nil {
		t.Fatal("existing uncertain transaction replayed")
	}
}
func TestRegistryCredentialFileIsPrivateAndNeverInheritedBySkopeo(t *testing.T) {
	state := t.TempDir()
	_ = os.Chmod(state, 0700)
	r, err := newRegistry(Inputs{}, releasegithub.Client{}, nil, state, map[string]string{"GITHUB_ACTOR": "endorses", "GH_TOKEN": "disposable-not-a-token"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	private := r.private
	defer r.close()
	info, err := os.Stat(r.auth)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("registry credentials not private")
	}
	runner := r.runner.(process.Runner)
	if !runner.CleanEnv || runner.Env["GH_TOKEN"] != "" || runner.Env["GITHUB_TOKEN"] != "" {
		t.Fatal("publication credential inherited by image subprocess")
	}
	r.close()
	if _, err = os.Stat(private); !os.IsNotExist(err) {
		t.Fatal("registry credential file retained")
	}
}
