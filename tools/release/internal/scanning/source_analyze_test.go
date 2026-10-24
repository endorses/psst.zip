package scanning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/source"
)

func sourceFixtureJSON(value any) []byte {
	raw, err := contract.JSON(value)
	if err != nil {
		panic(err)
	}
	return raw
}
func sourceFixtureStream(values ...any) []byte {
	var out bytes.Buffer
	for _, value := range values {
		out.Write(sourceFixtureJSON(value))
	}
	return out.Bytes()
}
func sourceGoFixture(platform string) (map[string][]byte, []any) {
	arch := strings.TrimPrefix(platform, "linux/")
	sum := "h1:" + strings.Repeat("a", 43) + "="
	root := map[string]any{"ImportPath": "example/cmd/server", "Module": map[string]any{"Path": "example", "Main": true}}
	dep := map[string]any{"ImportPath": "dependency/safe", "Module": map[string]any{"Path": "dependency", "Version": "v1.0.0", "Sum": sum}}
	config := map[string]any{"protocol_version": "v1.0.0", "scanner_name": "govulncheck", "scanner_version": GovulnVersion, "db": "https://vuln.go.dev", "db_last_modified": "2026-10-24T00:00:00Z", "go_version": CompilerVersion, "scan_level": "symbol", "scan_mode": "source"}
	advisory := map[string]any{"id": "GO-2026-1234", "modified": "2026-10-24T00:00:00Z", "affected": []any{map[string]any{"package": map[string]any{"name": "dependency", "ecosystem": "Go"}, "ecosystem_specific": map[string]any{"imports": []any{map[string]any{"path": "dependency/ssh"}}}}}}
	finding := map[string]any{"osv": "GO-2026-1234", "trace": []any{map[string]any{"module": "dependency", "version": "v1.0.0"}}}
	messages := []any{map[string]any{"config": config}, map[string]any{"SBOM": map[string]any{"go_version": CompilerVersion, "roots": []string{"example/cmd/server"}, "modules": []any{map[string]any{"path": "example"}, map[string]any{"path": "dependency", "version": "v1.0.0"}}}}, map[string]any{"osv": advisory}, map[string]any{"finding": finding}}
	variant, key := "v1", "GOAMD64"
	if arch == "arm64" {
		variant, key = "v8.0", "GOARM64"
	}
	env := map[string]any{"GOOS": "linux", "GOARCH": arch, "CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=readonly", "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org", key: variant}
	raw := map[string][]byte{
		"go-execution.complete": []byte("complete\n"), "govulncheck.exit": []byte("0\n"), "govulncheck-convert.exit": []byte("0\n"), "govulncheck.txt": []byte("No vulnerable symbols\n"),
		"go-version.txt": []byte("go version " + CompilerVersion + " " + platform + "\n"), "go-env.json": sourceFixtureJSON(env),
		"scanner-module.json":       sourceFixtureJSON(map[string]any{"Path": "golang.org/x/vuln", "Version": GovulnVersion, "Sum": GovulnSum}),
		"scanner-build.json":        sourceFixtureJSON(map[string]any{"GoVersion": CompilerVersion, "Main": map[string]any{"Path": "golang.org/x/vuln", "Version": GovulnVersion, "Sum": GovulnSum}}),
		"scanner-binary-sha256.txt": []byte(strings.Repeat("a", 64) + "  /tmp/bin/govulncheck\n"),
		"go-roots.json":             sourceFixtureStream(root), "go-all-graph.json": sourceFixtureStream(root, dep), "go-server-graph.json": sourceFixtureStream(root, dep),
		"go-modules.json": sourceFixtureStream(map[string]any{"Path": "example", "Main": true}, map[string]any{"Path": "dependency", "Version": "v1.0.0", "Sum": sum}),
	}
	raw["govulncheck.json"] = sourceFixtureStream(messages...)
	return raw, messages
}
func sourceNPMFixture(finding bool) map[string][]byte {
	code, total := 0, 0
	vulnerabilities := map[string]any{}
	if finding {
		code, total = 1, 1
		vulnerabilities["fixture"] = map[string]any{"severity": "low", "isDirect": false, "via": []any{map[string]any{"source": 123, "name": "fixture"}}}
	}
	return map[string][]byte{"node-execution.complete": []byte("complete\n"), "node-version.txt": []byte("v26.12.1\n"), "npm-version.txt": []byte("11.20.0\n"), "npm-audit.exit": []byte(fmt.Sprintf("%d\n", code)), "npm-audit.json": sourceFixtureJSON(map[string]any{"auditReportVersion": 2, "vulnerabilities": vulnerabilities, "metadata": map[string]any{"vulnerabilities": map[string]any{"total": total}}}), "npm-lock-graph.json": sourceFixtureJSON(map[string]any{"name": "web"}), "npm-audit.stderr": {}, "npm-graph.stderr": {}, "execution.stderr": {}}
}
func TestSourceAnalysisRetainsFindingsAndRejectsPartialToolEvidence(t *testing.T) {
	raw, messages := sourceGoFixture("linux/amd64")
	result, err := AnalyzeGo(raw, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	findings := result["findings"].([]map[string]any)
	if len(findings) != 1 || findings[0]["disposition"] != "not-applicable" {
		t.Fatal("module-only official affected paths lost")
	}
	sum := []byte("dependency v1.0.0 h1:" + strings.Repeat("a", 43) + "=\n")
	if _, err = sourceGoFacts(raw, "example", sum, "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	advisory := messages[2].(map[string]any)["osv"].(map[string]any)
	advisory["withdrawn"] = "2026-10-24T01:00:00Z"
	raw["govulncheck.json"] = sourceFixtureStream(messages...)
	if _, err = sourceGoFacts(raw, "example", sum, "linux/amd64"); err == nil {
		t.Fatal("withdrawn advisory established an absence disposition")
	}
	delete(advisory, "withdrawn")
	imported := advisory["affected"].([]any)[0].(map[string]any)["ecosystem_specific"].(map[string]any)["imports"].([]any)[0].(map[string]any)
	imported["path"] = "dependency/safe"
	raw["govulncheck.json"] = sourceFixtureStream(messages...)
	if _, err = sourceGoFacts(raw, "example", sum, "linux/amd64"); err == nil {
		t.Fatal("actually imported affected package was suppressed")
	}
	raw["go-version.txt"] = []byte("go version go1.27.2 linux/amd64\n")
	if _, err = AnalyzeGo(raw, "linux/amd64"); err == nil {
		t.Fatal("unexpected source compiler accepted")
	}
	lock := sourceFixtureJSON(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{}}})
	npm, err := AnalyzeNPM(sourceNPMFixture(true), lock)
	if err != nil {
		t.Fatal(err)
	}
	if rows := npm["findings"].([]map[string]any); len(rows) != 1 || rows[0]["disposition"] != "unresolved" {
		t.Fatal("low-severity indirect npm advisory was suppressed")
	}
	incomplete := sourceNPMFixture(false)
	incomplete["npm-audit.exit"] = []byte("1\n")
	if _, err = AnalyzeNPM(incomplete, lock); err == nil {
		t.Fatal("inconsistent npm exit/findings accounting accepted")
	}
}

type sourceAggregationFixture struct {
	o       SourceAggregationOptions
	trusted map[string]bool
}

func newSourceAggregationFixture(t *testing.T, npmFinding bool) sourceAggregationFixture {
	t.Helper()
	folder := t.TempDir()
	identity := source.Context{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}
	goImage := "docker.io/library/golang@sha256:" + strings.Repeat("b", 64)
	nodeImage := "docker.io/library/node@sha256:" + strings.Repeat("c", 64)
	files := map[string][]byte{"backend/go.mod": []byte("module example\n\ngo 1.23.0\nrequire dependency v1.0.0\n"), "backend/go.sum": []byte("dependency v1.0.0 h1:" + strings.Repeat("a", 43) + "=\n"), "backend/cmd/server/main.go": []byte("package main\nfunc main(){}\n"), "web/package.json": []byte("{\"name\":\"web\"}\n"), "web/package-lock.json": sourceFixtureJSON(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{"name": "web"}}})}
	inputs := map[string]string{}
	for name, raw := range files {
		inputs[name] = hash(raw)
	}
	rawNpm := sourceNPMFixture(npmFinding)
	rawFacts := map[string]string{}
	for name, raw := range rawNpm {
		rawFacts[name] = hash(raw)
	}
	common := NPMMeasurement{SchemaVersion: 1, Kind: "common-npm-lock-scanner-measurement", Source: Source{identity.Repository, identity.Version, identity.Commit}, Execution: "common-lock", Platform: "linux/amd64", SourceInputs: map[string]string{"web/package.json": inputs["web/package.json"], "web/package-lock.json": inputs["web/package-lock.json"]}, Builder: Builder{Reference: nodeImage, Config: "sha256:" + strings.Repeat("d", 64), Architecture: "amd64", Layers: []string{"sha256:" + strings.Repeat("e", 64)}, Daemon: "amd64"}, Started: "2026-10-24T00:00:00Z", Completed: "2026-10-24T00:01:00Z", RawFiles: rawFacts}
	rawNpm["common-npm-audit.json"] = sourceFixtureJSON(common)
	fixture := sourceAggregationFixture{trusted: map[string]bool{}}
	fixture.o = SourceAggregationOptions{Binding: github.Binding{Repository: identity.Repository, Version: identity.Version, Commit: identity.Commit, RunID: 123, Attempt: 1}, Application: source.ApplicationVerification{Source: identity, Files: files}, GoImage: goImage, NodeImage: nodeImage, Sources: map[string]SourceEvidence{}, Subjects: map[string]string{"manifest": "fixture"}}
	fixture.o.ResolvedBases = sourceFixtureJSON(map[string]any{"schema_version": 1, "kind": "release-candidate", "candidate_only": true, "version": identity.Version, "source_commit": identity.Commit, "base_images": map[string]string{"golang": goImage, "node": nodeImage}})
	fixture.trusted[hash(fixture.o.ResolvedBases)] = true
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		directory := filepath.Join(folder, strings.TrimPrefix(platform, "linux/"))
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		rawGo, _ := sourceGoFixture(platform)
		record := SourceMeasurement{Schema: 2, Kind: "native-source-scanner-measurement", Source: NativeIdentity{Source{identity.Repository, identity.Version, identity.Commit}, platform}, Execution: "native-go-common-lock", ArchiveSHA256: hash([]byte("fixture Git archive")), Inputs: inputs, Completed: "2026-10-24T01:00:00Z", Pending: true, Review: true, Scans: []map[string]any{}}
		for _, target := range []string{"backend-source", "web-source"} {
			raw := rawGo
			builder := Builder{Reference: goImage, Config: "sha256:" + strings.Repeat("f", 64), Architecture: strings.TrimPrefix(platform, "linux/"), Layers: []string{"sha256:" + strings.Repeat("e", 64)}, Daemon: strings.TrimPrefix(platform, "linux/")}
			var calculated map[string]any
			var err error
			if target == "backend-source" {
				calculated, err = AnalyzeGo(raw, platform)
			} else {
				raw = rawNpm
				builder = common.Builder
				calculated, err = AnalyzeNPM(raw, files["web/package-lock.json"])
			}
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(directory, target)
			if err = os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			inventory := map[string]string{}
			for name, body := range raw {
				if err = os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
					t.Fatal(err)
				}
				inventory[name] = hash(body)
			}
			calculated["target"] = target
			calculated["subject"] = "git:" + identity.Repository + "@" + identity.Commit
			calculated["started_at"] = common.Started
			calculated["scanned_at"] = common.Completed
			calculated["builder"] = builder
			calculated["raw_files"] = inventory
			if target == "web-source" {
				calculated["common_lock"] = map[string]string{"platform": common.Platform, "measurement_sha256": hash(raw["common-npm-audit.json"])}
			}
			record.Scans = append(record.Scans, calculated)
		}
		raw := sourceFixtureJSON(record)
		fixture.o.Sources[platform] = SourceEvidence{Measurement: raw, Directory: directory}
		fixture.trusted[hash(raw)] = true
	}
	fixture.o.Authenticate = func(_ context.Context, raw []byte) error {
		if !fixture.trusted[hash(raw)] {
			return fmt.Errorf("fixture rejected unsigned substituted bytes")
		}
		return nil
	}
	return fixture
}
func TestBothNativeSourceGraphsShareOneAuditAndDerivePolicyFromRawEvidence(t *testing.T) {
	fixture := newSourceAggregationFixture(t, false)
	details, err := AggregateSources(context.Background(), fixture.o)
	if err != nil {
		t.Fatal(err)
	}
	scans := details["scans"].([]any)
	if len(scans) != 2 || len(scans[0].(map[string]any)["findings"].([]map[string]any)) != 2 || details["derivation_policy"] != "authenticated-native-Go-graphs-and-common-lock-npm-v2" {
		t.Fatal("native findings or shared npm identity lost")
	}
	missing := fixture.o
	missing.Sources = map[string]SourceEvidence{"linux/amd64": fixture.o.Sources["linux/amd64"]}
	if _, err = AggregateSources(context.Background(), missing); err == nil {
		t.Fatal("missing ARM64 evidence accepted")
	}
	evidence := fixture.o.Sources["linux/arm64"]
	if err = os.WriteFile(filepath.Join(evidence.Directory, "backend-source/go-server-graph.json"), []byte("substitution"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = AggregateSources(context.Background(), fixture.o); err == nil {
		t.Fatal("changed raw native graph accepted")
	}
	unsafe := newSourceAggregationFixture(t, false)
	evidence = unsafe.o.Sources["linux/amd64"]
	var measurement map[string]any
	if err = json.Unmarshal(evidence.Measurement, &measurement); err != nil {
		t.Fatal(err)
	}
	goScan := measurement["scans"].([]any)[0].(map[string]any)
	goScan["findings"] = []any{}
	evidence.Measurement = sourceFixtureJSON(measurement)
	unsafe.o.Sources["linux/amd64"] = evidence
	unsafe.trusted[hash(evidence.Measurement)] = true
	if _, err = AggregateSources(context.Background(), unsafe.o); err == nil {
		t.Fatal("authenticated wrapper omitted an actual finding")
	}
	advisory := newSourceAggregationFixture(t, true)
	if _, err = AggregateSources(context.Background(), advisory.o); err == nil {
		t.Fatal("npm advisory accepted without repair/rescan")
	}
}
