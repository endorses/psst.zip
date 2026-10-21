package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/endorses/psst.zip/client"
)

func TestAPIErrorExitCodesPreservePolicyAndAuthentication(t *testing.T) {
	for _, test := range []struct {
		name, serverCode string
		status, exit     int
	}{
		{"receive capacity changed", "receive_file_limit", 403, ExitUnavailable},
		{"receive batch exhausted", "receive_batch_limit", 403, ExitUnavailable},
		{"unsupported legacy invitation", "legacy_receive_disabled", 403, ExitUnavailable},
		{"traffic budget", "traffic_budget_exhausted", 429, ExitUnavailable},
		{"expired session", "", 401, ExitAuth},
		{"password renewal", "password_change_required", 403, ExitAuth},
		{"administrator account", "admin_transfer_forbidden", 403, ExitAuth},
		{"missing capability", "", 403, ExitAuth},
	} {
		t.Run(test.name, func(t *testing.T) {
			const operation = "12345678-1234-1234-1234-123456789012"
			err := &client.OperationError{OperationID: operation, Cause: &client.APIError{
				Status: test.status, Code: test.serverCode, RetryAfter: 17 * time.Second,
			}}
			failure := classify(err)
			if failure.exit != test.exit || failure.ServerCode != test.serverCode || failure.OperationID != operation || failure.RetryAfterSeconds != 17 {
				t.Fatalf("scripting error contract changed: %+v (exit %d)", failure, failure.exit)
			}
			if (failure.Code == "authentication_required") != (test.exit == ExitAuth) {
				t.Fatalf("policy error was presented as authentication: %+v", failure)
			}
			var out bytes.Buffer
			if err := writeJSON(&out, nil, failure); err != nil {
				t.Fatal(err)
			}
			var envelope result
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope.OK || envelope.SchemaVersion != 1 || envelope.Error == nil || envelope.Error.ServerCode != test.serverCode || envelope.Error.OperationID != operation {
				t.Fatalf("policy/authentication result lost structured evidence: %s", out.String())
			}
		})
	}
}
