// Package deployment implements the root-owned VPS deployment transaction. The
// command boundary is deliberately separate from the release build tooling.
package deployment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

const (
	ConfigPath        = "/etc/psst.zip/deployment.json"
	StatePath         = "/var/lib/psst.zip-deploy"
	BackupPath        = "/var/backups/psst.zip"
	maxJSON           = 16 << 20
	maxRetentionItems = 4096
)

var (
	versionRE    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	idRE         = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{12}$`)
	imageRE      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	nameRE       = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	domainRE     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)
	repositoryRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*$`)
)

// Object retains existing v1 Compose and transaction fields without renaming
// them during an installed-updater migration.
type Object = map[string]any

func object(v any) Object { m, _ := v.(map[string]any); return m }
func array(v any) []any   { m, _ := v.([]any); return m }
func text(v any) string   { s, _ := v.(string); return s }
func boolean(v any) bool  { b, _ := v.(bool); return b }
func number(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case json.Number:
		x, _ := n.Int64()
		return x
	}
	return 0
}
func integer(v any) bool {
	switch n := v.(type) {
	case float64:
		return float64(int64(n)) == n
	case int, int64:
		return true
	case json.Number:
		_, e := n.Int64()
		return e == nil
	}
	return false
}
func clone(m Object) Object {
	b, _ := json.Marshal(m)
	var out Object
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = d.Decode(&out)
	return out
}
func equal(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func keys(m Object) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func has(s []string, k string) bool {
	for _, v := range s {
		if v == k {
			return true
		}
	}
	return false
}
func stringsOf(v any) []string {
	var out []string
	for _, x := range array(v) {
		out = append(out, text(x))
	}
	return out
}
func expect(ok bool, message string) error {
	if !ok {
		return fmt.Errorf("%s", message)
	}
	return nil
}
func set(values ...string) map[string]bool {
	m := map[string]bool{}
	for _, v := range values {
		m[v] = true
	}
	return m
}
func sameSet(a, b map[string]bool) bool { return reflect.DeepEqual(a, b) }
func relative(name string) bool {
	return name != "" && !filepath.IsAbs(name) && !strings.ContainsAny(name, "\\\x00") && !has(strings.Split(name, "/"), "..")
}
func inside(path, root string) bool {
	r, err := filepath.Rel(root, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// Config is the installed deployment.json format. Defaults remain deliberately
// strict: off-host checkpoints and full verification are never silently relaxed.
type Config struct {
	Repository           string            `json:"repository"`
	SignerWorkflow       string            `json:"signer_workflow"`
	Installation         string            `json:"installation"`
	Project              string            `json:"project"`
	Environment          string            `json:"environment"`
	ComposeFiles         []string          `json:"compose_files"`
	OperatorFiles        []string          `json:"operator_files"`
	ReleaseOverrides     []string          `json:"release_overrides"`
	ExternalProxy        bool              `json:"external_proxy"`
	Domain               string            `json:"domain"`
	DBPath               string            `json:"db_path"`
	VolumeNames          map[string]string `json:"volume_names"`
	DiskReserveBytes     int64             `json:"disk_reserve_bytes"`
	ImageReserveBytes    int64             `json:"image_reserve_bytes"`
	VerificationHook     *string           `json:"verification_hook"`
	CheckpointHook       *string           `json:"checkpoint_hook"`
	GitHubTokenFile      *string           `json:"github_token_file"`
	RetentionCount       int               `json:"retention_count"`
	CheckpointProtection string            `json:"checkpoint_protection,omitempty"`
	VerificationProfile  string            `json:"verification_profile,omitempty"`
}

func (c Config) Protection() string {
	if c.CheckpointProtection == "" {
		return "off-host"
	}
	return c.CheckpointProtection
}
func (c Config) Profile() string {
	if c.VerificationProfile == "" {
		return "full"
	}
	return c.VerificationProfile
}
func (c Config) Validate() error {
	owner, repository, _ := strings.Cut(c.Repository, "/")
	if !repositoryRE.MatchString(c.Repository) || !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(owner) || len(owner) > 39 || len(repository) > 100 || repository == "." || repository == ".." || c.SignerWorkflow != c.Repository+"/.github/workflows/release.yml" {
		return fmt.Errorf("unexpected repository or signer workflow")
	}
	if !filepath.IsAbs(c.Installation) || filepath.Clean(c.Installation) != c.Installation || !nameRE.MatchString(c.Project) || !domainRE.MatchString(c.Domain) || !strings.Contains(c.Domain, ".") || strings.Contains(c.Domain, "..") {
		return fmt.Errorf("invalid installation, project or public hostname")
	}
	if !strings.HasPrefix(c.DBPath, "/app/data/") || filepath.Clean(c.DBPath) != c.DBPath || !regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`).MatchString(c.DBPath) {
		return fmt.Errorf("database must reside in the backend volume")
	}
	if c.Protection() != "off-host" && c.Protection() != "local-only" {
		return fmt.Errorf("invalid checkpoint protection policy")
	}
	if c.Profile() != "full" && c.Profile() != "runtime-v1" || c.Profile() == "runtime-v1" && c.VerificationHook != nil {
		return fmt.Errorf("invalid verification profile or hook")
	}
	if len(c.ComposeFiles) == 0 {
		return fmt.Errorf("current Compose files are required")
	}
	for _, values := range [][]string{c.ComposeFiles, c.OperatorFiles, c.ReleaseOverrides} {
		seen := set()
		for _, name := range values {
			if !relative(name) || seen[name] {
				return fmt.Errorf("invalid or duplicate deployment path")
			}
			seen[name] = true
		}
	}
	if !relative(c.Environment) {
		return fmt.Errorf("invalid environment path")
	}
	expected := set("backend:/app/data", "caddy:/data", "caddy:/config")
	if c.ExternalProxy {
		expected["external-proxy:/data"] = true
		expected["external-proxy:/config"] = true
	}
	seen := set()
	if len(c.VolumeNames) != len(expected) {
		return fmt.Errorf("explicit distinct physical volumes are required")
	}
	for key, v := range c.VolumeNames {
		if !expected[key] || !nameRE.MatchString(v) || seen[v] {
			return fmt.Errorf("invalid or repeated physical volume")
		}
		seen[v] = true
	}
	if c.DiskReserveBytes < 1<<30 || c.ImageReserveBytes < 1<<30 || c.DiskReserveBytes > 1<<50 || c.ImageReserveBytes > 1<<50 || c.RetentionCount < 2 || c.RetentionCount > 100 {
		return fmt.Errorf("invalid deployment capacity/retention policy")
	}
	for _, p := range []*string{c.VerificationHook, c.CheckpointHook, c.GitHubTokenFile} {
		if p != nil && (!filepath.IsAbs(*p) || strings.ContainsAny(*p, "\x00\r\n")) {
			return fmt.Errorf("invalid protected hook/token path")
		}
	}
	return nil
}

// SSHCommand accepts no paths, shell syntax, flags or arbitrary artifact URLs.
func SSHCommand(command string) ([]string, error) {
	if command == "status" {
		return []string{"status"}, nil
	}
	if strings.HasPrefix(command, "update ") && versionRE.MatchString(strings.TrimPrefix(command, "update ")) {
		return []string{"update", strings.TrimPrefix(command, "update ")}, nil
	}
	return nil, fmt.Errorf("restricted SSH accepts only update VERSION or status")
}
func profile(t Object) string {
	if boolean(t["restoring"]) {
		return "full"
	}
	p := text(t["verification_profile"])
	if p == "" {
		return "full"
	}
	return p
}

// DecodeConfig parses the existing installed v1 configuration. Optional policy
// fields retain their legacy strict defaults; required nullable fields must still
// be present. Parsing performs no host or production mutation.
func DecodeConfig(data []byte) (Config, error) {
	var c Config
	if err := contract.Decode(data, maxJSON, &c); err != nil {
		return c, err
	}
	var raw Object
	if err := decodeHost(data, maxJSON, &raw); err != nil {
		return c, err
	}
	required := []string{"repository", "signer_workflow", "installation", "project", "environment", "compose_files", "operator_files", "release_overrides", "external_proxy", "domain", "db_path", "volume_names", "disk_reserve_bytes", "image_reserve_bytes", "verification_hook", "checkpoint_hook", "github_token_file", "retention_count"}
	for _, key := range required {
		if _, ok := raw[key]; !ok {
			return c, fmt.Errorf("deployment configuration has missing fields")
		}
	}
	if _, ok := raw["external_proxy"].(bool); !ok {
		return c, fmt.Errorf("external_proxy must be boolean")
	}
	for _, key := range []string{"compose_files", "operator_files", "release_overrides"} {
		if _, ok := raw[key].([]any); !ok {
			return c, fmt.Errorf("deployment file lists must be arrays")
		}
	}
	if value, present := raw["checkpoint_protection"]; present && (value != "off-host" && value != "local-only") {
		return c, fmt.Errorf("invalid checkpoint protection policy")
	}
	if value, present := raw["verification_profile"]; present && (value != "full" && value != "runtime-v1") {
		return c, fmt.Errorf("invalid verification profile")
	}
	return c, c.Validate()
}

func mapOrEmpty(v any) Object {
	if m := object(v); m != nil {
		return m
	}
	return Object{}
}
func listOrEmpty(v any) []any {
	if values := array(v); values != nil {
		return values
	}
	return []any{}
}
