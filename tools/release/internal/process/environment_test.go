package process

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestChildEnvironment(t *testing.T) {
	if os.Getenv("PSST_PROCESS_TEST_CHILD") == "1" {
		fmt.Print(os.Getenv("PSST_PROCESS_TEST_AMBIENT"), ":", os.Getenv("PSST_PROCESS_TEST_EXPLICIT"))
		os.Exit(0)
	}
	t.Setenv("PSST_PROCESS_TEST_AMBIENT", "ambient")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, clean := range []bool{false, true} {
		runner := (Runner{CleanEnv: clean}).With(map[string]string{
			"PSST_PROCESS_TEST_CHILD": "1", "PSST_PROCESS_TEST_EXPLICIT": "explicit",
		})
		data, err := runner.Output(context.Background(), 5*time.Second, binary, "-test.run=^TestChildEnvironment$")
		if err != nil {
			t.Fatal(err)
		}
		want := "ambient:explicit"
		if clean {
			want = ":explicit"
		}
		if strings.TrimSpace(string(data)) != want {
			t.Fatalf("CleanEnv=%v: %q", clean, data)
		}
		var streamed bytes.Buffer
		if err := runner.RunTo(context.Background(), 5*time.Second, &streamed, binary, "-test.run=^TestChildEnvironment$"); err != nil || strings.TrimSpace(streamed.String()) != want {
			t.Fatalf("streaming CleanEnv=%v: %q (%v)", clean, streamed.String(), err)
		}
	}
}
