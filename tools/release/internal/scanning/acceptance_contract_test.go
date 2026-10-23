package scanning

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func TestCommittedCaddyAcceptanceCannotBeRelocatedOrExpandedByConsumerFacts(t *testing.T) {
	root, commit, node, fact := policyFixture(t)
	valid := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	original, _ := contract.JSON(acceptedFixture(node, fact))
	// A changed working copy cannot extend the exact committed acceptance.
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(policyPath)), []byte("uncommitted replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePublication(context.Background(), root, original, "endorses/psst.zip", "v1.2.3", commit, valid); err != nil {
		t.Fatalf("working-copy policy affected the committed scope: %v", err)
	}
	for _, name := range []string{"backend", "raw finding", "policy", "advisory", "source revision", "accepted module", "fixed version"} {
		t.Run(name, func(t *testing.T) {
			var details map[string]any
			json.Unmarshal(original, &details)
			scans := details["scans"].([]any)
			web := object(scans[2])
			finding := object(web["findings"].([]any)[0])
			switch name {
			case "backend":
				object(scans[0])["findings"] = web["findings"]
				web["findings"] = []any{}
			case "raw finding":
				object(finding["finding"])["Severity"] = "CRITICAL"
			case "policy":
				object(object(finding["acceptance"])["policy"])["git_blob"] = strings.Repeat("f", 40)
			case "advisory":
				object(finding["official_advisory"])["sha256"] = "sha256:" + strings.Repeat("f", 64)
			case "source revision":
				object(finding["source_inputs"])["wrapper_revision"] = strings.Repeat("f", 40)
			case "accepted module":
				object(finding["finding"])["PkgName"] = "golang.org/x/net"
				finding["finding_sha256"] = hash(legacyJSON(finding["finding"]))
			case "fixed version":
				object(finding["finding"])["FixedVersion"] = "1.27.4"
				finding["finding_sha256"] = hash(legacyJSON(finding["finding"]))
			}
			raw, _ := contract.JSON(details)
			if err := ValidatePublication(context.Background(), root, raw, "endorses/psst.zip", "v1.2.3", commit, valid); err == nil {
				t.Fatal("altered consumer facts expanded exact committed Caddy acceptance")
			}
		})
	}
}
