package deployment

import "testing"

func TestCutoverRequiresResolvedStateAndStaysOutsideSSH(t *testing.T) {
	for _, record := range []Object{nil, {"phase": "completed"}, {"phase": "failed-safe", "mutation_started": false}, {"phase": "abandoned"}} {
		if err := resolvedCutoverState(record); err != nil {
			t.Fatal(record, err)
		}
	}
	for _, phase := range []string{"", "preparing", "failed-closed", "awaiting-verification", "restored-awaiting-verification", "verified", "activating", "unknown"} {
		if err := resolvedCutoverState(Object{"phase": phase}); err == nil {
			t.Fatal("accepted unresolved transaction", phase)
		}
	}
	if err := resolvedCutoverState(Object{"phase": "failed-safe", "mutation_started": true}); err == nil {
		t.Fatal("accepted mutated failed-safe transaction")
	}
	if _, err := SSHCommand("cutover-check"); err == nil {
		t.Fatal("SSH user gained installer command")
	}
}
