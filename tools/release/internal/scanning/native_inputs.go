package scanning

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// nativeScanInputs reads only the already-produced local native descriptor.
// Receiving jobs must separately authenticate its exact bytes before using any
// claims. The scanner still inspects and binds the actual OCI/config/source data.
func nativeScanInputs(name string, identity NativeIdentity, component string) (string, string, error) {
	raw, err := bounded(name, scannerRawLimit)
	if err != nil {
		return "", "", err
	}
	var value map[string]any
	if err = contract.Decode(raw, scannerRawLimit, &value); err != nil {
		return "", "", err
	}
	var expected map[string]any
	body, _ := json.Marshal(identity)
	json.Unmarshal(body, &expected)
	if value["schema_version"] != float64(2) || value["kind"] != "native-release-artifacts" || !reflect.DeepEqual(value["source"], expected) || value["publication_authorized"] != false || value["measurement_authentication_required"] != true || compilerTarget(component) == "" {
		return "", "", fmt.Errorf("native scanner descriptor source/completion differs")
	}
	config := text(object(value["tested_configs"])[component])
	sourceSHA := text(object(object(value["artifacts"])["runtime_source"])["sha256"])
	if !digestPattern.MatchString(config) || !digestPattern.MatchString(sourceSHA) {
		return "", "", fmt.Errorf("native scanner descriptor configuration/source digest missing")
	}
	return config, sourceSHA, nil
}
