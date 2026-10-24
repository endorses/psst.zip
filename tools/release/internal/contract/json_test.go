package contract

import "testing"

func TestDecodeRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	for _, input := range []string{`{"id":1,"id":2}`, `{"id":{"nested":1,"nested":2}}`, `{"unknown":1}`, `{"id":1} {"id":2}`, `[1,]`} {
		t.Run(input, func(t *testing.T) {
			var target struct {
				ID any `json:"id"`
			}
			if Decode([]byte(input), 1024, &target) == nil {
				t.Fatal("ambiguous contract accepted")
			}
		})
	}
	var target struct {
		ID int `json:"id"`
	}
	if err := Decode([]byte(`{"id":2}`), 1024, &target); err != nil || target.ID != 2 {
		t.Fatalf("valid input: %v", err)
	}
	if Decode([]byte(`{"id":2}`), 1, &target) == nil {
		t.Fatal("size limit ignored")
	}
}
