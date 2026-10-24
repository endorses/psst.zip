package publication

import (
	"encoding/json"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func TestNewSourceResultIncorporatesDirectChecksWithoutGateFiles(t *testing.T) {
	fixture := resultFixture(t)
	checks := fixture.Checks
	options := SourceResultOptions{Repository: fixture.Repository, Version: fixture.Version, Commit: fixture.Commit, RunID: fixture.RunID, Attempt: fixture.Attempt, Binding: fixture.Binding, Subjects: fixture.Subjects, Checks: checks, Inputs: fixture.Inputs}
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
