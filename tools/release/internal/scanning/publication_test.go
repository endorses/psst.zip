package scanning

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func policyFixture(t *testing.T) (string, string, map[string]any, map[string]any) {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	native := map[string]any{"base_digest": digest, "binary_sha256": digest}
	policy, err := contract.JSON(map[string]any{
		"schema_version": 1, "kind": "container-publication-policy", "environment": "container-release", "authorized_exception_owners": []string{"fixture"},
		"temporary_caddy_exception": map[string]any{
			"schema_version": 1, "kind": "temporary-caddy-vulnerability-acceptance", "id": "disposable-fixture", "repository": "endorses/psst.zip", "authorized_by": "fixture",
			"authorized_at": "2026-10-09T07:15:59Z", "expires_at": "2026-10-23T00:00:00Z", "reason": "Disposable narrow policy for expiry and binary-scope checks.",
			"caddy_version": "v2.11.7", "source_revision": strings.Repeat("b", 40), "go_version": "go1.27.2", "base_image": "docker.io/library/caddy@" + digest,
			"platforms": map[string]any{"linux/amd64": native, "linux/arm64": native},
			"findings":  []any{map[string]any{"go_id": "GO-2026-1234", "scanner_id": "CVE-2026-1234", "module": "stdlib", "installed_version": "v1.27.2", "fixed_version": "1.27.3", "advisory_sha256": digest}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	name := filepath.Join(root, filepath.FromSlash(policyPath))
	os.MkdirAll(filepath.Dir(name), 0o700)
	os.WriteFile(name, policy, 0o600)
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git: %v", err)
		}
		return strings.TrimSpace(string(data))
	}
	git("init", "-q")
	git("add", "tools")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath="+os.DevNull, "commit", "-qm", "Disposable committed policy")
	commit := git("rev-parse", "HEAD")
	node, fact, err := committedPolicy(context.Background(), root, "endorses/psst.zip", commit)
	if err != nil {
		t.Fatal(err)
	}
	return root, commit, node, fact
}

func acceptedFixture(node, fact map[string]any) map[string]any {
	accepted := object(node["findings"].([]any)[0])
	native := object(object(node["platforms"])["linux/amd64"])
	raw := map[string]any{"VulnerabilityID": accepted["scanner_id"], "PkgName": accepted["module"], "InstalledVersion": accepted["installed_version"], "FixedVersion": accepted["fixed_version"], "DataSource": map[string]any{"ID": "govulndb"}}
	acceptance := map[string]any{"policy": fact, "exception_id": node["id"], "authorized_by": node["authorized_by"], "expires_at": node["expires_at"], "caddy_version": node["caddy_version"], "source_revision": node["source_revision"], "base_image": node["base_image"], "go_version": node["go_version"], "base_digest": native["base_digest"], "binary_sha256": native["binary_sha256"]}
	finding := map[string]any{"disposition": "temporarily-accepted", "reason": node["reason"], "class": "lang-pkgs", "target": "usr/bin/caddy", "finding": raw, "finding_sha256": hash(legacyJSON(raw)), "acceptance": acceptance, "binary_sha256": native["binary_sha256"], "source_inputs": map[string]any{"wrapper_revision": node["source_revision"]}, "graph_sha256": "sha256:" + strings.Repeat("e", 64), "official_advisory": map[string]any{"id": accepted["go_id"], "sha256": accepted["advisory_sha256"], "origin": "https://vuln.go.dev/ID/" + text(accepted["go_id"]) + ".json"}, "affected_packages": []any{"affected/package"}}
	scans := []any{}
	for _, target := range []string{"backend-amd64", "backend-arm64", "web-amd64", "web-arm64"} {
		findings := []any{}
		if target == "web-amd64" {
			findings = append(findings, finding)
		}
		scans = append(scans, map[string]any{"target": target, "status": "complete", "exit_code": 0, "findings": findings})
	}
	return map[string]any{"scans": scans, "temporary_caddy_exception_policy": fact}
}

func TestPublicationRechecksCommittedExceptionExpiryAndExactScope(t *testing.T) {
	root, commit, node, fact := policyFixture(t)
	valid := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	fixture := acceptedFixture(node, fact)
	data, _ := contract.JSON(fixture)
	if err := ValidatePublication(context.Background(), root, data, "endorses/psst.zip", "v1.2.3", commit, valid); err != nil {
		t.Fatal(err)
	}
	expired := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	if err := ValidatePublication(context.Background(), root, data, "endorses/psst.zip", "v1.2.3", commit, expired); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatal("signed exception survived its expiration boundary")
	}
	var changed map[string]any
	json.Unmarshal(data, &changed)
	scan := object(changed["scans"].([]any)[2])
	finding := object(scan["findings"].([]any)[0])
	finding["binary_sha256"] = "sha256:" + strings.Repeat("f", 64)
	altered, _ := contract.JSON(changed)
	if err := ValidatePublication(context.Background(), root, altered, "endorses/psst.zip", "v1.2.3", commit, valid); err == nil {
		t.Fatal("exception accepted a different Caddy binary")
	}
	for _, scan := range fixture["scans"].([]any) {
		object(scan)["findings"] = []any{}
	}
	clean, _ := contract.JSON(fixture)
	if err := ValidatePublication(context.Background(), root, clean, "endorses/psst.zip", "v1.2.3", commit, expired); err != nil {
		t.Fatalf("unused expired exception blocked patched clean images: %v", err)
	}
}

func TestCorrespondingPolicyFactPermitsRemovingAnUnusedException(t *testing.T) {
	root, _, _, _ := policyFixture(t)
	name := filepath.Join(root, filepath.FromSlash(policyPath))
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]any
	if err = json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	delete(policy, "temporary_caddy_exception")
	raw, err = contract.JSON(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		value, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("disposable policy commit: %v", err)
		}
		return strings.TrimSpace(string(value))
	}
	git("add", "tools")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath="+os.DevNull, "commit", "-qm", "Remove unused disposable exception")
	commit := git("rev-parse", "HEAD")
	fact, err := PublicationPolicyFact(context.Background(), root, "endorses/psst.zip", commit)
	if err != nil || fact["record_digest"] != hash(raw) {
		t.Fatalf("optional policy fact rejected: %v", err)
	}
	if _, _, err := committedPolicy(context.Background(), root, "endorses/psst.zip", commit); err == nil {
		t.Fatal("active acceptance silently used policy without exception")
	}
}

func TestLegacyScannerHashPreservesNonASCIIJSONContract(t *testing.T) {
	got := string(legacyJSON(map[string]any{"name": "é😀<"}))
	want := "{\n  \"name\": \"\\u00e9\\ud83d\\ude00<\"\n}\n"
	if got != want {
		t.Fatalf("legacy hash contract changed: %q", got)
	}
}
