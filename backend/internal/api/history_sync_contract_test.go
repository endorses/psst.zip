package api

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Shared wire fixtures must include every required field emitted by the actual
// snapshot response models; native/web parsers must not relax for test data.
func TestHistorySyncFixtureMatchesAPIResourceModels(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/testing/fixtures/history-sync-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Snapshot struct {
			Transfers []json.RawMessage `json:"transfers"`
			Slots     []json.RawMessage `json:"slots"`
		} `json:"snapshot"`
		Upsert struct {
			Changes []struct {
				Kind     string          `json:"kind"`
				Resource json.RawMessage `json:"resource"`
			} `json:"changes"`
		} `json:"upsert"`
		Unknown struct {
			Changes []struct {
				Kind     string          `json:"kind"`
				Resource json.RawMessage `json:"resource"`
			} `json:"changes"`
		} `json:"unknown_summary"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	check := func(raw json.RawMessage, kind string) {
		t.Helper()
		var model any
		if kind == "transfer" {
			model = &historyTransferResponse{}
		} else {
			model = &historySlotResponse{}
		}
		if err := json.Unmarshal(raw, model); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		var want, got map[string]any
		if err = json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s fixture differs from API model\nfixture: %s\nmodel: %s", kind, raw, encoded)
		}
	}
	for _, row := range fixture.Snapshot.Transfers {
		check(row, "transfer")
	}
	for _, row := range fixture.Snapshot.Slots {
		check(row, "slot")
	}
	for _, row := range fixture.Upsert.Changes {
		check(row.Resource, row.Kind)
	}
	for _, row := range fixture.Unknown.Changes {
		check(row.Resource, row.Kind)
	}
}
