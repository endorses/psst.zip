package publication

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/scanning"
)

func validateScans(ctx context.Context, root string, inputs Inputs, now time.Time) error {
	for _, gate := range []string{"source-scanners", "final-image-scanners"} {
		var record struct {
			Scans []struct {
				Target    string                                 `json:"target"`
				Subject   string                                 `json:"subject"`
				Status    string                                 `json:"status"`
				Exit      *int                                   `json:"exit_code"`
				Scanner   string                                 `json:"scanner"`
				Version   string                                 `json:"version"`
				Database  string                                 `json:"database"`
				ScannedAt string                                 `json:"scanned_at"`
				Findings  []struct{ Disposition, Reason string } `json:"findings"`
			} `json:"scans"`
		}
		if json.Unmarshal(inputs.Result.Checks[gate], &record) != nil {
			return fmt.Errorf("scanner evidence is malformed")
		}
		expected := map[string]string{}
		if gate == "source-scanners" {
			subject := "git:" + inputs.Binding.Repository + "@" + inputs.Binding.Commit
			expected["backend-source"], expected["web-source"] = subject, subject
		} else {
			for _, name := range []string{"backend-amd64", "backend-arm64", "web-amd64", "web-arm64"} {
				expected[name] = inputs.Result.Subjects[name]
			}
		}
		if len(record.Scans) != len(expected) {
			return fmt.Errorf("scanner evidence lacks complete exact source/image coverage")
		}
		for _, scan := range record.Scans {
			if expected[scan.Target] == "" || scan.Subject != expected[scan.Target] || scan.Status != "complete" || scan.Exit == nil || *scan.Exit != 0 || strings.TrimSpace(scan.Scanner) == "" || strings.TrimSpace(scan.Version) == "" || strings.TrimSpace(scan.Database) == "" || strings.TrimSpace(scan.ScannedAt) == "" || scan.Findings == nil {
				return fmt.Errorf("scanner target, tool/database/date or completion differs from reviewed release")
			}
			delete(expected, scan.Target)
			for _, finding := range scan.Findings {
				if finding.Disposition == "temporarily-accepted" && gate == "final-image-scanners" {
					continue
				}
				if finding.Disposition != "fixed" && finding.Disposition != "not-applicable" || strings.TrimSpace(finding.Reason) == "" {
					return fmt.Errorf("unresolved or out-of-scope source/image scanner finding")
				}
			}
		}
	}
	return scanning.ValidatePublication(ctx, root, inputs.Result.Checks["final-image-scanners"], inputs.Binding.Repository, inputs.Binding.Version, inputs.Binding.Commit, now)
}
