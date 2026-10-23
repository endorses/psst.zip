package publication

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func TestNewSourceResultIncorporatesDirectChecksWithoutGateFiles(t *testing.T) {
	o, checks := fixture(t)
	raw, err := os.ReadFile(filepath.Join(o.Prepared, "release-inputs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var foundation struct {
		Binding  string            `json:"binding_sha256"`
		Subjects map[string]string `json:"subjects"`
	}
	if err = json.Unmarshal(raw, &foundation); err != nil {
		t.Fatal(err)
	}
	options := SourceResultOptions{Repository: o.Repository, Version: o.Version, Commit: o.Commit, RunID: o.RunID, Attempt: o.Attempt, Binding: foundation.Binding, Subjects: foundation.Subjects, Checks: checks, Inputs: map[string]Input{"corresponding-source-replay": {digest([]byte("raw replay"))}}}
	result, err := NewSourceResult(options)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := contract.JSON(result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(encoded); err != nil {
		t.Fatal(err)
	}
	checks["source-ci"] = json.RawMessage(`{"run_id":0,"run_attempt":0}`)
	if _, err := NewSourceResult(options); err == nil {
		t.Fatal("accepted another CI invocation")
	}
	delete(checks, "source-ci")
	if _, err := NewSourceResult(options); err == nil {
		t.Fatal("accepted missing completed check")
	}
	if _, ok := result.Checks["source-ci"]; !ok {
		t.Fatal("constructor retained caller map by reference")
	}
}
