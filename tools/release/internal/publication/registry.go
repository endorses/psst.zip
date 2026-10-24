package publication

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	releasegithub "github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

type registry struct {
	inputs                           Inputs
	client                           releasegithub.Client
	runner                           releasegithub.Executor
	journal                          *journal
	auth, anonymous, private, prefix string
	initialize, setup                bool
	out                              io.Writer
}
type packageRecord struct {
	Name       string                 `json:"name"`
	Type       string                 `json:"package_type"`
	Visibility string                 `json:"visibility"`
	Owner      struct{ Login string } `json:"owner"`
	Repository struct {
		Name string `json:"full_name"`
	} `json:"repository"`
}
type packageVersion struct {
	Name     string `json:"name"`
	Metadata struct {
		Container struct {
			Tags []string `json:"tags"`
		} `json:"container"`
	} `json:"metadata"`
}

func newRegistry(inputs Inputs, client releasegithub.Client, j *journal, state string, environment map[string]string, out io.Writer) (*registry, error) {
	actor, token := environment["GITHUB_ACTOR"], environment["GH_TOKEN"]
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}(\[bot\])?$`).MatchString(actor) || token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, fmt.Errorf("missing valid hosted registry credential")
	}
	initialize := environment["PSST_INITIALIZE_GHCR_PACKAGES"]
	if initialize != "" && initialize != "false" && initialize != "true" {
		return nil, fmt.Errorf("invalid first-package initialization setting")
	}
	private, err := os.MkdirTemp(state, "registry-credentials-")
	if err != nil {
		return nil, fmt.Errorf("private registry credentials unavailable")
	}
	r := &registry{inputs: inputs, client: client, journal: j, private: private, initialize: initialize == "true", out: out}
	r.auth, r.anonymous = filepath.Join(private, "auth.json"), filepath.Join(private, "anonymous.json")
	raw, _ := contract.JSON(map[string]any{"auths": map[string]any{"ghcr.io": map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(actor + ":" + token))}}})
	if os.WriteFile(r.auth, raw, 0600) != nil || os.WriteFile(r.anonymous, []byte(`{"auths":{}}`), 0600) != nil {
		_ = os.RemoveAll(private)
		return nil, fmt.Errorf("private registry credentials could not be written")
	}
	env := map[string]string{"PATH": os.Getenv("PATH"), "HOME": private, "LANG": "C", "REGISTRY_AUTH_FILE": r.anonymous}
	for _, name := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(name); value != "" {
			env[name] = value
		}
	}
	r.runner = process.Runner{Dir: state, CleanEnv: true, Env: env}
	return r, nil
}
func (r *registry) close() { _ = os.RemoveAll(r.private) }
func (r *registry) repository(component string) string {
	owner, _, _ := strings.Cut(r.inputs.Binding.Repository, "/")
	return "ghcr.io/" + owner + "/psst-zip-" + component
}
func (r *registry) packageEndpoint(component string) string {
	return r.prefix + "/packages/container/psst-zip-" + component
}
func (r *registry) packages(ctx context.Context, allowMissing, allowPrivate bool) (map[string]*packageRecord, error) {
	owner, _, _ := strings.Cut(r.inputs.Binding.Repository, "/")
	if r.prefix == "" {
		var user struct{ Login, Type string }
		if err := r.client.API(ctx, "GET", "users/"+owner, &user); err != nil {
			return nil, err
		}
		if user.Login != owner || user.Type != "User" && user.Type != "Organization" {
			return nil, fmt.Errorf("unknown container package owner")
		}
		r.prefix = "users/" + owner
		if user.Type == "Organization" {
			r.prefix = "orgs/" + owner
		}
	}
	records := map[string]*packageRecord{}
	for _, component := range []string{"backend", "web"} {
		var record packageRecord
		present, err := r.client.OptionalGET(ctx, r.packageEndpoint(component), &record)
		if err != nil {
			return nil, err
		}
		if !present {
			if !allowMissing {
				return nil, fmt.Errorf("both linked container packages must already exist")
			}
			records[component] = nil
			continue
		}
		if record.Name != "psst-zip-"+component || record.Type != "container" || record.Owner.Login != owner || record.Repository.Name != r.inputs.Binding.Repository || record.Visibility != "public" && !(allowPrivate && record.Visibility == "private") {
			return nil, fmt.Errorf("container package must be public and linked to the reviewed repository")
		}
		records[component] = &record
	}
	return records, nil
}
func (r *registry) versions(ctx context.Context, component string) ([]packageVersion, error) {
	result := []packageVersion{}
	for page := 1; page <= 100; page++ {
		var versions []packageVersion
		if err := r.client.API(ctx, "GET", fmt.Sprintf("%s/versions?state=active&per_page=100&page=%d", r.packageEndpoint(component), page), &versions); err != nil {
			return nil, err
		}
		if len(versions) > 100 {
			return nil, fmt.Errorf("container package listing exceeds inspection bound")
		}
		result = append(result, versions...)
		if len(versions) < 100 {
			return result, nil
		}
	}
	return nil, fmt.Errorf("container package listing exceeds inspection bound; absence is unknown")
}
func (r *registry) unused(ctx context.Context, component string, checkDigests bool) error {
	versions, err := r.versions(ctx, component)
	if err != nil {
		return err
	}
	image := r.inputs.Manifest.Images[component]
	_, index, _ := strings.Cut(image.Index, "@")
	for _, version := range versions {
		for _, tag := range version.Metadata.Container.Tags {
			if tag == r.inputs.Binding.Version {
				return fmt.Errorf("container version tag already exists; never overwrite or adopt it")
			}
		}
		if checkDigests && (version.Name == index || version.Name == image.PlatformDigests["linux/amd64"] || version.Name == image.PlatformDigests["linux/arm64"]) {
			return fmt.Errorf("reviewed registry digest already exists; reconcile instead of adopting it")
		}
	}
	return nil
}
func (r *registry) preflight(ctx context.Context) error {
	records, err := r.packages(ctx, r.initialize, false)
	if err != nil {
		return err
	}
	missing := 0
	for _, component := range []string{"backend", "web"} {
		if records[component] == nil {
			missing++
			continue
		}
		if err = r.unused(ctx, component, true); err != nil {
			return err
		}
	}
	if missing != 0 && missing != 2 {
		return fmt.Errorf("mixed existing and missing packages require separate operator setup")
	}
	r.setup = missing == 2
	return nil
}
func (r *registry) command(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	data, err := r.runner.Output(ctx, timeout, append([]string{"skopeo"}, args...)...)
	if err != nil || len(data) > 16<<20 {
		return nil, fmt.Errorf("registry operation failed; private diagnostics withheld")
	}
	return data, nil
}
func (r *registry) inspect(ctx context.Context, reference, expected string, anonymous bool) error {
	auth := r.auth
	args := []string{"inspect", "--raw", "--retry-times", "0"}
	if anonymous {
		auth = r.anonymous
		args = append(args, "--no-creds")
	}
	args = append(args, "--authfile", auth, "docker://"+reference)
	raw, err := r.command(ctx, 180*time.Second, args...)
	if err != nil || digest(raw) != expected {
		return fmt.Errorf("registry manifest readback differs from exact reviewed bytes")
	}
	return nil
}
func (r *registry) inspectPair(ctx context.Context, anonymous bool, tagged bool) error {
	for _, component := range []string{"backend", "web"} {
		image := r.inputs.Manifest.Images[component]
		_, index, _ := strings.Cut(image.Index, "@")
		if err := r.inspect(ctx, image.Index, index, anonymous); err != nil {
			return err
		}
		if tagged {
			if err := r.inspect(ctx, r.repository(component)+":"+r.inputs.Binding.Version, index, anonymous); err != nil {
				return err
			}
		}
		for _, arch := range []string{"amd64", "arm64"} {
			child := image.PlatformDigests["linux/"+arch]
			if err := r.inspect(ctx, r.repository(component)+"@"+child, child, anonymous); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *registry) waitPublic(ctx context.Context) error {
	if !r.setup {
		_, err := r.packages(ctx, false, false)
		return err
	}
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	fmt.Fprintln(r.out, "Make both newly created linked GHCR packages public in their GitHub package settings; this attempt waits up to ten minutes. Do not change image bytes or tags.")
	for {
		records, err := r.packages(ctx, false, true)
		if err != nil {
			return err
		}
		if records["backend"].Visibility == "public" && records["web"].Visibility == "public" {
			r.setup = false
			return nil
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("package visibility wait cancelled")
		case <-deadline.C:
			timer.Stop()
			return fmt.Errorf("package visibility setup timed out; reconcile the retained draft")
		case <-timer.C:
		}
	}
}
func (r *registry) push(ctx context.Context, layouts map[string]string) error {
	for _, component := range []string{"backend", "web"} {
		image := r.inputs.Manifest.Images[component]
		if !r.setup {
			if err := r.unused(ctx, component, true); err != nil {
				return err
			}
		}
		if err := r.journal.mutate("push-"+component+"-index", map[string]any{"subject": image.Index}, func() error {
			_, err := r.command(ctx, 20*time.Minute, "copy", "--all", "--preserve-digests", "--dest-authfile", r.auth, "--retry-times", "0", "oci:"+layouts[component], "docker://"+image.Index)
			return err
		}); err != nil {
			return err
		}
	}
	if err := r.waitPublic(ctx); err != nil {
		return err
	}
	return r.inspectPair(ctx, true, false)
}

func (r *registry) anonymousPull(ctx context.Context, state string) error {
	private, err := os.MkdirTemp(state, "anonymous-pull-")
	if err != nil {
		return fmt.Errorf("private anonymous-pull staging unavailable")
	}
	defer os.RemoveAll(private)
	for _, component := range []string{"backend", "web"} {
		for _, arch := range []string{"amd64", "arm64"} {
			child := r.inputs.Manifest.Images[component].PlatformDigests["linux/"+arch]
			destination := filepath.Join(private, component+"-"+arch)
			if _, err = r.command(ctx, 20*time.Minute, "copy", "--preserve-digests", "--src-no-creds", "--authfile", r.anonymous, "--retry-times", "0", "docker://"+r.repository(component)+"@"+child, "oci:"+destination); err != nil {
				return err
			}
			raw, err := r.command(ctx, 180*time.Second, "inspect", "--raw", "--authfile", r.anonymous, "oci:"+destination)
			if err != nil || digest(raw) != child {
				return fmt.Errorf("complete anonymous child pull differs from reviewed image")
			}
		}
	}
	return nil
}
func (r *registry) tagVersions(ctx context.Context) error {
	for _, component := range []string{"backend", "web"} {
		if err := r.unused(ctx, component, false); err != nil {
			return err
		}
		commit, err := r.client.ResolveTag(ctx, r.inputs.Binding.Repository, r.inputs.Binding.Version)
		if err != nil || commit != r.inputs.Binding.Commit {
			return fmt.Errorf("source tag moved before container version tagging")
		}
		image := r.inputs.Manifest.Images[component]
		if err = r.journal.mutate("tag-"+component, map[string]any{"version": r.inputs.Binding.Version}, func() error {
			_, err := r.command(ctx, 20*time.Minute, "copy", "--all", "--preserve-digests", "--src-authfile", r.auth, "--dest-authfile", r.auth, "--retry-times", "0", "docker://"+image.Index, "docker://"+r.repository(component)+":"+r.inputs.Binding.Version)
			return err
		}); err != nil {
			return err
		}
	}
	return r.inspectPair(ctx, true, true)
}
