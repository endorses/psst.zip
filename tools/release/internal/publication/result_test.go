package publication

import (
	"encoding/json"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"strings"
	"testing"
)

func resultFixture(t *testing.T) Result {
	t.Helper()
	r := Result{SchemaVersion: 1, Kind: "container-release-result", Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40), RunID: 123, Attempt: 2, Subjects: map[string]string{}, Checks: map[string]json.RawMessage{}, Inputs: map[string]Input{"corresponding-source-replay": {SHA256: "sha256:" + strings.Repeat("b", 64)}}, Platforms: map[string]Platform{}}
	for _, name := range []string{"manifest", "bundle", "backend-index", "backend-amd64", "backend-arm64", "web-index", "web-amd64", "web-arm64"} {
		r.Subjects[name] = "file:" + name + "@sha256:" + strings.Repeat("b", 64)
		if strings.HasPrefix(name, "backend") || strings.HasPrefix(name, "web") {
			r.Subjects[name] = "oci://ghcr.io/endorses/psst-zip-" + name + "@sha256:" + strings.Repeat("b", 64)
		}
	}
	for _, name := range baseChecks {
		r.Checks[name] = json.RawMessage(`{"actual":"checked"}`)
	}
	r.Checks["source-ci"] = json.RawMessage(`{"run_id":123,"run_attempt":2}`)
	r.Checks["final-image-smoke"] = json.RawMessage(`{"execution":{"linux/amd64":"native","linux/arm64":"native"}}`)
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		r.Platforms[platform] = Platform{Execution: "native", Checks: []string{"final-image-smoke"}}
	}
	raw, _ := contract.JSON(map[string]any{"repository": r.Repository, "ref": "refs/tags/" + r.Version, "commit": r.Commit, "workflow": ".github/workflows/release.yml", "subjects": r.Subjects})
	r.Binding = digest(raw)
	return r
}

func TestResultDecoderRejectsAmbiguousScopeAndSubstitution(t *testing.T) {
	valid := resultFixture(t)
	raw, _ := contract.JSON(valid)
	decoded, err := Decode(raw)
	if err != nil || len(decoded.Checks) != 6 || decoded.Inputs["corresponding-source-replay"] != valid.Inputs["corresponding-source-replay"] {
		t.Fatalf("direct result contract changed: %v", err)
	}
	for name, mutate := range map[string]func(*Result){
		"subject-substitution": func(r *Result) { r.Subjects["bundle"] = "file:other@sha256:" + strings.Repeat("c", 64) },
		"emulated-platform": func(r *Result) {
			p := r.Platforms["linux/arm64"]
			p.Execution = "emulated"
			r.Platforms["linux/arm64"] = p
		},
		"cross-attempt-ci":   func(r *Result) { r.Checks["source-ci"] = json.RawMessage(`{"run_id":123,"run_attempt":3}`) },
		"missing-check":      func(r *Result) { delete(r.Checks, "runtime-notices") },
		"unknown-check":      func(r *Result) { r.Checks["unreviewed-check"] = json.RawMessage(`{}`) },
		"missing-raw-replay": func(r *Result) { delete(r.Inputs, "corresponding-source-replay") },
		"missing-parent-result": func(r *Result) {
			r.Checks["upgrade-recovery"] = json.RawMessage(`{"execution":{"linux/amd64":"native","linux/arm64":"native"}}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := resultFixture(t)
			mutate(&r)
			data, _ := contract.JSON(r)
			if _, err := Decode(data); err == nil {
				t.Fatal("ambiguous or substituted result accepted")
			}
		})
	}
	duplicate := append([]byte(`{"schema_version":1,`), raw[1:]...)
	if _, err := Decode(duplicate); err == nil {
		t.Fatal("duplicate result JSON field accepted")
	}
}
