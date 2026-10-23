package publication

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func fixture(t *testing.T) (Options, map[string]json.RawMessage) {
	t.Helper()
	root := t.TempDir()
	o := Options{Prepared: filepath.Join(root, "prepared"), SourceDir: filepath.Join(root, "source"), ChecksDir: filepath.Join(root, "checks"), Output: filepath.Join(root, "source", "release-result.json"), Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40), RunID: 123, Attempt: 2}
	for _, dir := range []string{o.Prepared, o.SourceDir, o.ChecksDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	subjects := map[string]string{}
	for _, key := range []string{"manifest", "bundle", "backend-index", "backend-amd64", "backend-arm64", "web-index", "web-amd64", "web-arm64"} {
		subjects[key] = "file:" + key + "@sha256:" + strings.Repeat("b", 64)
		if strings.HasPrefix(key, "backend") || strings.HasPrefix(key, "web") {
			subjects[key] = "oci://ghcr.io/endorses/psst-zip-" + key + "@sha256:" + strings.Repeat("b", 64)
		}
	}
	bindingRaw, _ := contract.JSON(map[string]any{"repository": o.Repository, "ref": "refs/tags/" + o.Version, "commit": o.Commit, "workflow": ".github/workflows/release.yml", "subjects": subjects})
	binding := digest(bindingRaw)
	record := map[string]any{"schema_version": 1, "kind": "prepared-release-inputs", "source_kind": "version-tag", "tagged_source_ci_gate_verified": false, "signer_identity_verified": false, "publication_authorized": false, "measurement_authentication_required": true, "assets": map[string]string{}, "dependency_replays": map[string]any{}, "upstream_replay": map[string]any{}, "repository": o.Repository, "version": o.Version, "source_commit": o.Commit, "binding_sha256": binding, "subjects": subjects}
	writeJSON(t, filepath.Join(o.Prepared, "release-inputs.json"), record)
	details := map[string]json.RawMessage{}
	for _, name := range baseChecks {
		details[name] = json.RawMessage(`{"actual":"checked"}`)
		if name == "final-image-smoke" {
			details[name] = json.RawMessage(`{"execution":{"linux/amd64":"native","linux/arm64":"native"}}`)
		}
		if name == "source-ci" {
			details[name] = json.RawMessage(`{"run_id":123,"run_attempt":2}`)
		}
		path := filepath.Join(o.ChecksDir, name+".json")
		if name == "corresponding-source" {
			path = filepath.Join(o.SourceDir, name+".json")
		}
		writeJSON(t, path, report{1, name, binding, true, details[name]})
	}
	writeJSON(t, filepath.Join(o.SourceDir, "corresponding-source-evidence.json"), map[string]string{"raw": "source replay evidence"})
	return o, details
}
func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := contract.JSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestCollapsePreservesFactsAndRawInputsAndExtendsExactAttempt(t *testing.T) {
	o, details := fixture(t)
	evidencePath := filepath.Join(o.SourceDir, "corresponding-source-evidence.json")
	evidence, _ := os.ReadFile(evidencePath)
	if err := Create(o); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(o.Output)
	result, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range details {
		var got, want any
		json.Unmarshal(result.Checks[name], &got)
		json.Unmarshal(value, &want)
		gotRaw, _ := json.Marshal(got)
		wantRaw, _ := json.Marshal(want)
		if string(gotRaw) != string(wantRaw) {
			t.Fatal("substantive check changed")
		}
	}
	if result.Inputs["corresponding-source-replay"].SHA256 != digest(evidence) {
		t.Fatal("raw replay not inventoried")
	}
	if _, err = os.Stat(evidencePath); err != nil {
		t.Fatal("raw replay removed")
	}
	for _, name := range baseChecks {
		path := filepath.Join(o.ChecksDir, name+".json")
		if name == "corresponding-source" {
			path = filepath.Join(o.SourceDir, name+".json")
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("intermediate gate envelope retained")
		}
	}
	previous := o.Output
	o.Previous = previous
	o.Recovery = filepath.Join(filepath.Dir(o.SourceDir), "upgrade-recovery.json")
	o.Output = filepath.Join(filepath.Dir(o.SourceDir), "release-result.json")
	writeJSON(t, o.Recovery, report{1, "upgrade-recovery", result.Binding, true, json.RawMessage(`{"execution":{"linux/amd64":"native","linux/arm64":"native"}}`)})
	wrong := o
	wrong.Attempt++
	if Create(wrong) == nil {
		t.Fatal("cross-attempt extension accepted")
	}
	if err = Create(o); err != nil {
		t.Fatal(err)
	}
	completeRaw, _ := os.ReadFile(o.Output)
	complete, err := Decode(completeRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(complete.Checks) != 7 || complete.Inputs["source-result"].SHA256 != digest(raw) {
		t.Fatal("completed result lost prior facts")
	}
	if _, err = os.Stat(o.Recovery); !os.IsNotExist(err) {
		t.Fatal("intermediate recovery envelope retained")
	}
	if err = Create(o); err == nil {
		t.Fatal("result overwrite accepted")
	}
}
func TestAmbiguousOrSubstitutedInputsNeverRemoveReports(t *testing.T) {
	for _, bad := range []string{"duplicate-json", "wrong-gate", "wrong-binding", "emulated-platform"} {
		t.Run(bad, func(t *testing.T) {
			o, _ := fixture(t)
			path := filepath.Join(o.ChecksDir, "final-image-smoke.json")
			raw, _ := os.ReadFile(path)
			switch bad {
			case "duplicate-json":
				raw = []byte(`{"schema_version":1,"schema_version":1}`)
			case "wrong-gate":
				raw = []byte(strings.Replace(string(raw), "final-image-smoke", "source-ci", 1))
			case "wrong-binding":
				raw = []byte(strings.Replace(string(raw), "sha256:", "bad:", 1))
			case "emulated-platform":
				raw = []byte(strings.Replace(string(raw), "native", "emulated", 1))
			}
			os.WriteFile(path, raw, 0600)
			if Create(o) == nil {
				t.Fatal("bad result input accepted")
			}
			if _, err := os.Stat(o.Output); !os.IsNotExist(err) {
				t.Fatal("failed consolidation emitted output")
			}
			if _, err := os.Stat(filepath.Join(o.SourceDir, "corresponding-source.json")); err != nil {
				t.Fatal("failed consolidation deleted inputs")
			}
		})
	}
}
