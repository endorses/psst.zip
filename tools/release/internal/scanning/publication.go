package scanning

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/process"
)

const policyPath = "tools/container-distribution-policy.json"

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func text(value any) string           { result, _ := value.(string); return result }

func exactFields(value map[string]any, names ...string) bool {
	if len(value) != len(names) {
		return false
	}
	for _, name := range names {
		if _, present := value[name]; !present {
			return false
		}
	}
	return true
}

// legacyJSON preserves the established Python JSON hash contract used in signed
// scanner facts, including non-ASCII escaping. New source inventories use JSON.
func legacyJSON(value any) []byte {
	raw, _ := contract.JSON(value)
	var escaped strings.Builder
	for _, r := range string(raw) {
		if r < 128 {
			escaped.WriteRune(r)
		} else if r <= 0xffff {
			fmt.Fprintf(&escaped, "\\u%04x", r)
		} else {
			first, second := utf16.EncodeRune(r)
			fmt.Fprintf(&escaped, "\\u%04x\\u%04x", first, second)
		}
	}
	return []byte(escaped.String())
}

func committedPolicy(ctx context.Context, root, repository, commit string) (map[string]any, map[string]any, error) {
	return readPublicationPolicy(ctx, root, repository, commit, true)
}

// PublicationPolicyFact binds corresponding-source coverage to the exact
// committed publication policy. Expiry is checked only when an exception is
// actually used; a repaired release may remove the optional exception entirely.
func PublicationPolicyFact(ctx context.Context, root, repository, commit string) (map[string]any, error) {
	if !repositoryPattern.MatchString(repository) || !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("invalid publication policy source")
	}
	_, fact, err := readPublicationPolicy(ctx, root, repository, commit, false)
	return fact, err
}

func readPublicationPolicy(ctx context.Context, root, repository, commit string, requireException bool) (map[string]any, map[string]any, error) {
	runner := process.Runner{Dir: root}
	listing, err := runner.Output(ctx, 15*time.Second, "git", "ls-tree", commit, "--", policyPath)
	if err != nil {
		return nil, nil, err
	}
	match := regexp.MustCompile(`^100644 blob ([a-f0-9]{40})\t` + regexp.QuoteMeta(policyPath) + `\n$`).FindSubmatch(listing)
	if match == nil {
		return nil, nil, fmt.Errorf("publication policy is not a regular exact committed file")
	}
	raw, err := runner.Output(ctx, 15*time.Second, "git", "cat-file", "blob", commit+":"+policyPath)
	if err != nil {
		return nil, nil, err
	}
	var policy map[string]any
	if err := contract.Decode(raw, 16384, &policy); err != nil {
		return nil, nil, err
	}
	complete := exactFields(policy, "schema_version", "kind", "environment", "authorized_exception_owners", "temporary_caddy_exception") || !requireException && exactFields(policy, "schema_version", "kind", "environment", "authorized_exception_owners")
	if !complete || policy["schema_version"] != float64(1) || text(policy["kind"]) != "container-publication-policy" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`).MatchString(text(policy["environment"])) {
		return nil, nil, fmt.Errorf("invalid committed container publication policy")
	}
	owners, ok := policy["authorized_exception_owners"].([]any)
	seen := map[string]bool{}
	if !ok || len(owners) == 0 || len(owners) > 6 {
		return nil, nil, fmt.Errorf("invalid exception owners")
	}
	for _, owner := range owners {
		name := text(owner)
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}$`).MatchString(name) || seen[name] {
			return nil, nil, fmt.Errorf("invalid exception owner")
		}
		seen[name] = true
	}
	fact := map[string]any{"path": policyPath, "source_commit": commit, "record_digest": hash(raw), "git_blob": string(match[1])}
	if _, exists := policy["temporary_caddy_exception"]; !exists && !requireException {
		return nil, fact, nil
	}
	node := object(policy["temporary_caddy_exception"])
	if !exactFields(node, "schema_version", "kind", "id", "repository", "authorized_by", "authorized_at", "expires_at", "reason", "caddy_version", "source_revision", "go_version", "base_image", "platforms", "findings") || node["schema_version"] != float64(1) || text(node["kind"]) != "temporary-caddy-vulnerability-acceptance" || text(node["repository"]) != repository || !seen[text(node["authorized_by"])] || len(text(node["reason"])) < 20 || len(text(node["reason"])) > 2048 || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`).MatchString(text(node["id"])) || !commitPattern.MatchString(text(node["source_revision"])) || !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(text(node["caddy_version"])) || !regexp.MustCompile(`^go\d+\.\d+\.\d+$`).MatchString(text(node["go_version"])) || !strings.HasPrefix(text(node["base_image"]), "docker.io/library/caddy@") || !digestPattern.MatchString(strings.TrimPrefix(text(node["base_image"]), "docker.io/library/caddy@")) {
		return nil, nil, fmt.Errorf("invalid committed temporary Caddy exception scope")
	}
	start, err := policyTime(node["authorized_at"])
	end, endErr := policyTime(node["expires_at"])
	if err != nil || endErr != nil || !start.Before(end) || end.Sub(start) > 14*24*time.Hour {
		return nil, nil, fmt.Errorf("Caddy exception must expire within fourteen days")
	}
	platforms := object(node["platforms"])
	if !exactFields(platforms, "linux/amd64", "linux/arm64") {
		return nil, nil, fmt.Errorf("Caddy exception requires both exact platforms")
	}
	for _, platform := range platforms {
		p := object(platform)
		if !exactFields(p, "base_digest", "binary_sha256") || !digestPattern.MatchString(text(p["base_digest"])) || !digestPattern.MatchString(text(p["binary_sha256"])) {
			return nil, nil, fmt.Errorf("invalid Caddy exception platform identity")
		}
	}
	findings, ok := node["findings"].([]any)
	if !ok || len(findings) == 0 || len(findings) > 32 {
		return nil, nil, fmt.Errorf("missing bounded Caddy exception findings")
	}
	seen = map[string]bool{}
	for _, finding := range findings {
		row := object(finding)
		key := text(row["module"]) + "@" + text(row["go_id"])
		if !exactFields(row, "go_id", "scanner_id", "module", "installed_version", "fixed_version", "advisory_sha256") || !regexp.MustCompile(`^GO-\d{4}-\d+$`).MatchString(text(row["go_id"])) || !regexp.MustCompile(`^(GO|CVE)-\d{4}-\d+$`).MatchString(text(row["scanner_id"])) || !slices.Contains([]string{"stdlib", "golang.org/x/net"}, text(row["module"])) || !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(text(row["installed_version"])) || text(row["fixed_version"]) == "" || len(text(row["fixed_version"])) > 64 || !digestPattern.MatchString(text(row["advisory_sha256"])) || seen[key] || text(row["module"]) == "stdlib" && text(row["installed_version"]) != "v"+strings.TrimPrefix(text(node["go_version"]), "go") {
			return nil, nil, fmt.Errorf("invalid exact Caddy accepted finding")
		}
		seen[key] = true
	}
	return node, fact, nil
}

func policyTime(value any) (time.Time, error) {
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(text(value)) {
		return time.Time{}, fmt.Errorf("exception requires an exact UTC timestamp")
	}
	return time.Parse(time.RFC3339, text(value))
}

// ValidatePublication rechecks scanner dispositions at the mutation boundary.
// Signed temporary findings cannot outlive their exact committed exception.
// It does not replace scanner replay or final-subject attestation verification.
func ValidatePublication(ctx context.Context, root string, details json.RawMessage, repository, version, commit string, now time.Time) error {
	if !repositoryPattern.MatchString(repository) || !versionPattern.MatchString(version) || !commitPattern.MatchString(commit) {
		return fmt.Errorf("invalid scanner publication source")
	}
	var record map[string]any
	if err := contract.Decode(details, 16<<20, &record); err != nil {
		return err
	}
	scans, ok := record["scans"].([]any)
	if !ok || len(scans) != 4 {
		return fmt.Errorf("final-image scanner coverage is incomplete")
	}
	seen := map[string]bool{}
	var node, fact map[string]any
	for _, value := range scans {
		scan := object(value)
		target := text(scan["target"])
		if !slices.Contains([]string{"backend-amd64", "backend-arm64", "web-amd64", "web-arm64"}, target) || seen[target] || scan["status"] != "complete" || scan["exit_code"] != float64(0) {
			return fmt.Errorf("final-image scanner target or completion differs")
		}
		seen[target] = true
		findings, ok := scan["findings"].([]any)
		if !ok {
			return fmt.Errorf("missing final-image findings")
		}
		for _, value := range findings {
			finding := object(value)
			disposition := text(finding["disposition"])
			if disposition != "temporarily-accepted" {
				if !slices.Contains([]string{"fixed", "not-applicable"}, disposition) || strings.TrimSpace(text(finding["reason"])) == "" {
					return fmt.Errorf("unresolved final-image finding")
				}
				continue
			}
			if node == nil {
				var err error
				node, fact, err = committedPolicy(ctx, root, repository, commit)
				if err != nil {
					return err
				}
			}
			start, _ := policyTime(node["authorized_at"])
			end, _ := policyTime(node["expires_at"])
			if now.Before(start) || !now.Before(end) {
				return fmt.Errorf("temporary Caddy exception is not active or has expired")
			}
			if !reflect.DeepEqual(record["temporary_caddy_exception_policy"], fact) || !strings.HasPrefix(target, "web-") {
				return fmt.Errorf("temporary acceptance differs from committed Caddy policy")
			}
			platform := "linux/" + strings.TrimPrefix(target, "web-")
			native := object(object(node["platforms"])[platform])
			expected := map[string]any{"policy": fact, "exception_id": node["id"], "authorized_by": node["authorized_by"], "expires_at": node["expires_at"], "caddy_version": node["caddy_version"], "source_revision": node["source_revision"], "base_image": node["base_image"], "go_version": node["go_version"], "base_digest": native["base_digest"], "binary_sha256": native["binary_sha256"]}
			raw := object(finding["finding"])
			official := object(finding["official_advisory"])
			affected, _ := finding["affected_packages"].([]any)
			if finding["reason"] != node["reason"] || finding["class"] != "lang-pkgs" || finding["target"] != "usr/bin/caddy" || object(raw["DataSource"])["ID"] != "govulndb" || finding["finding_sha256"] != hash(legacyJSON(raw)) || !reflect.DeepEqual(finding["acceptance"], expected) || finding["binary_sha256"] != native["binary_sha256"] || object(finding["source_inputs"])["wrapper_revision"] != node["source_revision"] || !digestPattern.MatchString(text(finding["graph_sha256"])) || official["origin"] != "https://vuln.go.dev/ID/"+text(official["id"])+".json" || len(affected) == 0 {
				return fmt.Errorf("temporary finding exceeds exact committed Caddy scope")
			}
			matches := 0
			for _, accepted := range node["findings"].([]any) {
				row := object(accepted)
				if row["go_id"] == official["id"] && row["scanner_id"] == raw["VulnerabilityID"] && row["module"] == raw["PkgName"] && row["installed_version"] == raw["InstalledVersion"] && row["fixed_version"] == raw["FixedVersion"] && row["advisory_sha256"] == official["sha256"] {
					matches++
				}
			}
			if matches != 1 {
				return fmt.Errorf("Caddy finding is outside the temporary acceptance")
			}
		}
	}
	return nil
}
