package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// Host runs only fixed official host programs with a minimal environment.
// Credential material, command arguments and captured output never enter errors.
type Host struct {
	Config         Config
	State, Backups string
	boundInputs    []string
	deadlines      map[string]time.Time
	Now            func() time.Time
	command        func(context.Context, []byte, string, bool, ...string) ([]byte, error)
	protect        func(string, bool, bool, bool) error
}

func NewHost(c Config, state, backups string) *Host {
	return &Host{Config: c, State: state, Backups: backups, deadlines: map[string]time.Time{}, Now: time.Now}
}

type limitedOutput struct {
	bytes.Buffer
	limit  int64
	cancel context.CancelFunc
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if int64(b.Len()+len(p)) > b.limit {
		if b.cancel != nil {
			b.cancel()
		}
		return 0, fmt.Errorf("command output limit exceeded")
	}
	return b.Buffer.Write(p)
}
func (h *Host) run(ctx context.Context, input []byte, destination string, github bool, args ...string) ([]byte, error) {
	if h.command != nil {
		return h.command(ctx, input, destination, github, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if len(args) == 0 {
		return nil, fmt.Errorf("missing host command")
	}
	program, err := hostProgram(args[0])
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, program, args[1:]...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C.UTF-8"}
	if github {
		cmd.Env = append(cmd.Env, "GH_HOST=github.com")
		if h.Config.GitHubTokenFile != nil {
			if err := h.check(*h.Config.GitHubTokenFile, false, false, true); err != nil {
				return nil, err
			}
			b, err := os.ReadFile(*h.Config.GitHubTokenFile)
			if err != nil {
				return nil, err
			}
			token := strings.TrimSpace(string(b))
			if token == "" || strings.ContainsAny(token, "\r\n\x00") {
				return nil, fmt.Errorf("invalid protected GitHub token")
			}
			cmd.Env = append(cmd.Env, "GH_TOKEN="+token)
		}
	}
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var output limitedOutput
	output.limit = 64 << 20
	output.cancel = cancel
	var stderr limitedOutput
	stderr.limit = 1 << 20
	stderr.cancel = cancel
	cmd.Stderr = &stderr
	var f *os.File
	if destination != "" {
		f, err = os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		cmd.Stdout = f
	} else {
		cmd.Stdout = &output
	}
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("command failed (%s); output withheld", filepath.Base(args[0]))
	}
	if f != nil {
		if err = f.Sync(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return output.Bytes(), nil
}
func (h *Host) json(ctx context.Context, args ...string) (Object, error) {
	b, err := h.run(ctx, nil, "", false, args...)
	if err != nil {
		return nil, err
	}
	var m Object
	if err = decodeHost(b, 64<<20, &m); err != nil {
		return nil, fmt.Errorf("invalid host JSON response")
	}
	if m == nil {
		return nil, fmt.Errorf("host JSON object missing")
	}
	return m, nil
}
func (h *Host) jsonList(ctx context.Context, args ...string) ([]any, error) {
	b, err := h.run(ctx, nil, "", false, args...)
	if err != nil {
		return nil, err
	}
	var m []any
	if err = decodeHost(b, 64<<20, &m); err != nil {
		return nil, fmt.Errorf("invalid host JSON response")
	}
	return m, nil
}
func (h *Host) Compose(ctx context.Context, file string, args ...string) error {
	_, err := h.run(ctx, nil, "", false, append([]string{"docker", "compose", "-p", h.Config.Project, "-f", file}, args...)...)
	return err
}
func (h *Host) inspect(ctx context.Context, ids ...string) ([]any, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return h.jsonList(ctx, append([]string{"docker", "inspect"}, ids...)...)
}
func (h *Host) containers(ctx context.Context, all bool, project bool) ([]any, error) {
	args := []string{"docker", "ps", "-q"}
	if all {
		args[2] = "-aq"
	}
	if project {
		args = append(args, "--filter", "label=com.docker.compose.project="+h.Config.Project)
	}
	b, err := h.run(ctx, nil, "", false, args...)
	if err != nil {
		return nil, err
	}
	return h.inspect(ctx, strings.Fields(string(b))...)
}
func (h *Host) ProtectedInputs() ([]string, error) {
	if err := h.check(h.Config.Installation, true, false, false); err != nil {
		return nil, err
	}
	names := set(h.Config.Environment)
	for _, values := range [][]string{h.Config.ComposeFiles, h.Config.OperatorFiles, h.Config.ReleaseOverrides} {
		for _, name := range values {
			names[name] = true
		}
	}
	var paths []string
	for name := range names {
		paths = append(paths, filepath.Join(h.Config.Installation, name))
	}
	paths = append(paths, h.boundInputs...)
	sort.Strings(paths)
	for _, path := range paths {
		private := path == filepath.Join(h.Config.Installation, h.Config.Environment) || strings.HasPrefix(filepath.Base(path), ".env") || strings.HasSuffix(path, ".env")
		if err := h.check(path, false, false, private); err != nil {
			return nil, err
		}
	}
	for _, hook := range []*string{h.Config.VerificationHook, h.Config.CheckpointHook} {
		if hook != nil {
			if err := h.check(*hook, false, true, true); err != nil {
				return nil, err
			}
		}
	}
	return paths, nil
}
func (h *Host) Current(ctx context.Context) (Object, error) {
	active := filepath.Join(h.State, "active.json")
	if exists(active) {
		if err := h.check(active, false, false, true); err != nil {
			return nil, err
		}
		record, err := load(active)
		if err != nil {
			return nil, err
		}
		compose := object(record["compose"])
		approved, err := h.ProtectedInputs()
		if err != nil {
			return nil, err
		}
		for _, item := range object(compose["services"]) {
			for _, item := range array(object(item)["volumes"]) {
				mount := object(item)
				if mount["type"] != "bind" {
					continue
				}
				path := text(mount["source"])
				if !has(approved, path) && !inside(path, filepath.Join(h.State, "transactions")) && !inside(path, h.Backups) {
					return nil, fmt.Errorf("managed configuration bind escaped protected state")
				}
				if err := h.check(path, false, false, false); err != nil {
					return nil, err
				}
				if !has(approved, path) {
					h.boundInputs = append(h.boundInputs, path)
				}
			}
		}
		return compose, nil
	}
	args := []string{"docker", "compose", "-p", h.Config.Project, "--env-file", filepath.Join(h.Config.Installation, h.Config.Environment)}
	for _, name := range h.Config.ComposeFiles {
		args = append(args, "-f", filepath.Join(h.Config.Installation, name))
	}
	return h.json(ctx, append(args, "config", "--format", "json")...)
}
func (h *Host) Candidate(ctx context.Context, target string, m contract.ManifestV1) (Object, error) {
	p := filepath.Join(target, "release-images.env")
	values := map[string]string{"BACKEND_IMAGE": m.Images["backend"].Index, "WEB_IMAGE": m.Images["web"].Index, "COMPOSE_PROJECT_NAME": h.Config.Project, "DB_PATH": h.Config.DBPath, "PSST_DOMAIN": h.Config.Domain, "BACKEND_DATA_VOLUME": h.Config.VolumeNames["backend:/app/data"], "CADDY_DATA_VOLUME": h.Config.VolumeNames["caddy:/data"], "CADDY_CONFIG_VOLUME": h.Config.VolumeNames["caddy:/config"], "ADMIN_USERNAME": "", "ADMIN_PASSWORD": ""}
	if h.Config.ExternalProxy {
		values["EXTERNAL_PROXY_DATA_VOLUME"] = h.Config.VolumeNames["external-proxy:/data"]
		values["EXTERNAL_PROXY_CONFIG_VOLUME"] = h.Config.VolumeNames["external-proxy:/config"]
	}
	var names []string
	for k := range values {
		names = append(names, k)
	}
	sort.Strings(names)
	var out strings.Builder
	for _, k := range names {
		out.WriteString(k + "=" + values[k] + "\n")
	}
	if err := os.WriteFile(p, []byte(out.String()), 0o600); err != nil {
		return nil, err
	}
	args := []string{"docker", "compose", "-p", h.Config.Project, "--env-file", filepath.Join(h.Config.Installation, h.Config.Environment), "--env-file", p, "-f", filepath.Join(target, "bundle/deploy/compose.release.yml")}
	if h.Config.ExternalProxy {
		args = append(args, "-f", filepath.Join(target, "bundle/deploy/external-proxy.release.compose.yml"))
	}
	for _, name := range h.Config.ReleaseOverrides {
		args = append(args, "-f", filepath.Join(h.Config.Installation, name))
	}
	v, err := h.json(ctx, append(args, "config", "--format", "json")...)
	if err != nil {
		return nil, err
	}
	for _, item := range object(v["services"]) {
		s := object(item)
		if s == nil {
			return nil, fmt.Errorf("invalid candidate service")
		}
		delete(s, "build")
		s["pull_policy"] = "never"
	}
	return v, nil
}
func (h *Host) verifyIdentity(ctx context.Context, artifact string, m contract.ManifestV1) error {
	if err := m.Validate(h.Config.Repository); err != nil {
		return err
	}
	if h.Config.SignerWorkflow != h.Config.Repository+"/.github/workflows/release.yml" {
		return fmt.Errorf("unexpected signer workflow")
	}
	ref := "refs/tags/" + m.Version
	_, err := h.run(ctx, nil, "", true, "gh", "attestation", "verify", artifact, "--hostname", "github.com", "--repo", h.Config.Repository, "--cert-identity", "https://github.com/"+h.Config.SignerWorkflow+"@"+ref, "--signer-digest", m.Source.Commit, "--source-digest", m.Source.Commit, "--source-ref", ref, "--deny-self-hosted-runners", "--cert-oidc-issuer", "https://token.actions.githubusercontent.com", "--predicate-type", "https://slsa.dev/provenance/v1")
	return err
}
func (h *Host) downloadAsset(ctx context.Context, id int64, destination string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	args := []string{"gh", "api", "repos/" + h.Config.Repository + "/releases/assets/" + fmt.Sprint(id), "--header", "Accept: application/octet-stream"}
	// Download separately to impose a streaming size bound before allocating bytes.
	program, err := hostProgram(args[0])
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, program, args[1:]...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C.UTF-8", "GH_HOST=github.com"}
	if h.Config.GitHubTokenFile != nil {
		if err := h.check(*h.Config.GitHubTokenFile, false, false, true); err != nil {
			return err
		}
		b, err := os.ReadFile(*h.Config.GitHubTokenFile)
		if err != nil {
			return err
		}
		token := strings.TrimSpace(string(b))
		if strings.ContainsAny(token, "\r\n\x00") {
			return fmt.Errorf("invalid protected GitHub token")
		}
		cmd.Env = append(cmd.Env, "GH_TOKEN="+token)
	}
	var out limitedOutput
	out.limit = contract.MaxDeploymentBundleBytes
	out.cancel = cancel
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		return fmt.Errorf("release asset download failed or exceeded bounds; output withheld")
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(out.Bytes()); err != nil {
		return err
	}
	return f.Sync()
}
func (h *Host) Download(ctx context.Context, version, target string) (contract.ManifestV1, error) {
	var empty contract.ManifestV1
	if !versionRE.MatchString(version) {
		return empty, fmt.Errorf("invalid release version")
	}
	b, err := h.run(ctx, nil, "", true, "gh", "api", "repos/"+h.Config.Repository+"/releases/tags/"+version)
	if err != nil {
		return empty, err
	}
	var release Object
	if err = decodeHost(b, maxJSON, &release); err != nil {
		return empty, err
	}
	if release["draft"] != false || release["prerelease"] != false || release["tag_name"] != version {
		return empty, fmt.Errorf("only a ready stable release can be deployed")
	}
	bundle := "psst.zip-deployment-" + version + ".tar.gz"
	for _, name := range []string{"release-manifest.json", bundle} {
		var matches []Object
		for _, row := range array(release["assets"]) {
			if object(row)["name"] == name {
				matches = append(matches, object(row))
			}
		}
		if len(matches) != 1 || !integer(matches[0]["id"]) || !integer(matches[0]["size"]) || number(matches[0]["id"]) <= 0 || number(matches[0]["size"]) <= 0 || number(matches[0]["size"]) > contract.MaxDeploymentBundleBytes {
			return empty, fmt.Errorf("missing, duplicate or oversized release asset")
		}
		if err = h.downloadAsset(ctx, number(matches[0]["id"]), filepath.Join(target, name)); err != nil {
			return empty, err
		}
	}
	data, err := os.ReadFile(filepath.Join(target, "release-manifest.json"))
	if err != nil {
		return empty, err
	}
	m, err := contract.DecodeManifestV1(data, h.Config.Repository)
	if err != nil {
		return empty, err
	}
	if m.Version != version || m.PayloadProfile != "deployment-ready" {
		return empty, fmt.Errorf("release is not deployment-ready selected version")
	}
	for _, path := range []string{filepath.Join(target, "release-manifest.json"), filepath.Join(target, bundle)} {
		if err = h.verifyIdentity(ctx, path, m); err != nil {
			return empty, err
		}
	}
	files, err := contract.ReadBundleV1(filepath.Join(target, bundle), m)
	if err != nil {
		return empty, err
	}
	for _, component := range m.Images {
		if err = h.verifyIdentity(ctx, "oci://"+component.Index, m); err != nil {
			return empty, err
		}
		raw, e := h.json(ctx, "docker", "buildx", "imagetools", "inspect", component.Index, "--raw")
		if e != nil {
			return empty, e
		}
		observed := map[string]string{}
		for _, row := range array(raw["manifests"]) {
			item := object(row)
			p := object(item["platform"])
			name := text(p["os"]) + "/" + text(p["architecture"])
			if has(m.Platforms, name) {
				if observed[name] != "" {
					return empty, fmt.Errorf("duplicate advertised image architecture")
				}
				observed[name] = text(item["digest"])
			}
		}
		if !equal(observed, component.PlatformDigests) {
			return empty, fmt.Errorf("registry children differ from authenticated manifest")
		}
		repository, _, _ := strings.Cut(component.Index, "@")
		for _, digest := range component.PlatformDigests {
			if err = h.verifyIdentity(ctx, "oci://"+repository+"@"+digest, m); err != nil {
				return empty, err
			}
		}
	}
	root := filepath.Join(target, "bundle")
	if err = os.Mkdir(root, 0o700); err != nil {
		return empty, err
	}
	for name, data := range files {
		destination := filepath.Join(root, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return empty, err
		}
		f, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if e != nil {
			return empty, e
		}
		_, e = f.Write(data)
		closeErr := f.Close()
		if e != nil {
			return empty, e
		}
		if closeErr != nil {
			return empty, closeErr
		}
	}
	return m, nil
}
func (h *Host) Pull(ctx context.Context, m contract.ManifestV1, platform string) error {
	for _, component := range m.Images {
		if _, err := h.run(ctx, nil, "", false, "docker", "pull", "--platform", platform, component.Index); err != nil {
			return err
		}
		rows, err := h.jsonList(ctx, "docker", "image", "inspect", component.Index)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return fmt.Errorf("pulled image identity missing")
		}
		v := object(rows[0])
		c := object(v["Config"])
		labels := object(c["Labels"])
		if v["Architecture"] != strings.TrimPrefix(platform, "linux/") || v["Os"] != "linux" || has([]string{"", "0", "root"}, text(c["User"])) {
			return fmt.Errorf("pulled image has invalid architecture/user")
		}
		if labels["org.opencontainers.image.version"] != m.Version || labels["org.opencontainers.image.revision"] != m.Source.Commit || labels["org.opencontainers.image.source"] != "https://github.com/"+h.Config.Repository || labels["org.opencontainers.image.licenses"] != "AGPL-3.0-only" {
			return fmt.Errorf("pulled image metadata differs from authenticated release")
		}
	}
	return nil
}
func (h *Host) RestartPolicy(ctx context.Context) error {
	b, err := h.run(ctx, nil, "", false, "docker", "ps", "-q", "--filter", "label=com.docker.compose.project="+h.Config.Project)
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(string(b)) {
		if _, err = h.run(ctx, nil, "", false, "docker", "update", "--restart", "unless-stopped", id); err != nil {
			return err
		}
	}
	return nil
}
func (h *Host) RemoveImage(ctx context.Context, ref string) error {
	_, err := h.run(ctx, nil, "", false, "docker", "image", "rm", "--no-prune", ref)
	return err
}

// exec.Command resolves relative executables against the caller environment
// before Cmd.Env applies. Resolve against the fixed privileged PATH ourselves.
func hostProgram(name string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	if filepath.Base(name) != name {
		return "", fmt.Errorf("invalid host program")
	}
	for _, dir := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("required host program is unavailable")
}

func (h *Host) check(path string, directory, executable, private bool) error {
	if h.protect != nil {
		return h.protect(path, directory, executable, private)
	}
	return Protected(path, directory, executable, private)
}

func decodeHost(data []byte, limit int64, target any) error {
	var raw json.RawMessage
	if err := contract.Decode(data, limit, &raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(target)
}
