package publication

import (
	"encoding/json"
	"fmt"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// SourceResultOptions carries completed checks directly from evaluation. No
// separately serialized six-gate envelopes or schema1 preparation wrapper are
// needed for a newly assembled schema2 artifact foundation.
type SourceResultOptions struct {
	Repository, Version, Commit, Binding string
	RunID, Attempt                       int64
	Subjects                             map[string]string
	Checks                               map[string]json.RawMessage
	Inputs                               map[string]Input
}

func NewSourceResult(o SourceResultOptions) (Result, error) {
	result := Result{SchemaVersion: 1, Kind: "container-release-result", Repository: o.Repository, Version: o.Version, Commit: o.Commit, RunID: o.RunID, Attempt: o.Attempt, Binding: o.Binding, Subjects: map[string]string{}, Checks: map[string]json.RawMessage{}, Inputs: map[string]Input{}, Platforms: map[string]Platform{}}
	if len(o.Checks) != len(baseChecks) {
		return result, fmt.Errorf("source result requires exactly six completed source/native checks")
	}
	for name, value := range o.Subjects {
		result.Subjects[name] = value
	}
	for name, value := range o.Checks {
		result.Checks[name] = append(json.RawMessage{}, value...)
	}
	for name, value := range o.Inputs {
		result.Inputs[name] = value
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		result.Platforms[platform] = Platform{Execution: "native", Checks: []string{"final-image-smoke"}}
	}
	raw, err := contract.JSON(result)
	if err != nil {
		return result, err
	}
	return Decode(raw)
}
