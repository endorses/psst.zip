package scanning

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func TestNativeScannerInputsBindExactSourceWithoutGrantingPublication(t *testing.T) {
	identity := NativeIdentity{Source: Source{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}, Platform: "linux/amd64"}
	config, offering := "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64)
	value := map[string]any{"schema_version": 2, "kind": "native-release-artifacts", "source": identity, "tested_configs": map[string]string{"backend": config}, "artifacts": map[string]any{"runtime_source": map[string]any{"sha256": offering}}, "publication_authorized": false, "measurement_authentication_required": true}
	name := filepath.Join(t.TempDir(), "native-artifacts.json")
	write := func() {
		t.Helper()
		raw, err := contract.JSON(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	gotConfig, gotOffering, err := nativeScanInputs(name, identity, "backend")
	if err != nil || gotConfig != config || gotOffering != offering {
		t.Fatalf("exact local scanner inputs differ: %v", err)
	}
	other := identity
	other.Platform = "linux/arm64"
	if _, _, err := nativeScanInputs(name, other, "backend"); err == nil {
		t.Fatal("accepted descriptor from a different native architecture")
	}
	value["publication_authorized"] = true
	write()
	if _, _, err := nativeScanInputs(name, identity, "backend"); err == nil {
		t.Fatal("accepted local descriptor claiming publication authority")
	}
	value["publication_authorized"] = false
	write()
	for _, component := range []string{"web", "unrelated"} {
		if _, _, err := nativeScanInputs(name, identity, component); err == nil {
			t.Fatalf("accepted missing or unsupported component %q", component)
		}
	}
}

func TestNativeScannerDescriptorCannotBePartiallyOverridden(t *testing.T) {
	for name, run := range map[string]func(context.Context, string, []string, io.Writer) error{"compiler": compilerCommand, "image": imageCommand} {
		t.Run(name, func(t *testing.T) {
			args := []string{"--archive", "unused.tar", "--output", "unused-output", "--native-artifacts", "must-not-be-read.json", "--tested-config", "sha256:" + strings.Repeat("a", 64)}
			if name == "compiler" {
				args = append(args, "--collection", "unused-collection")
			} else {
				args = append(args, "--tool-archive", "unused-tool", "--tool-bundle", "unused-bundle", "--cosign", "unused-verifier")
			}
			if err := run(context.Background(), t.TempDir(), args, io.Discard); err == nil || !strings.Contains(err.Error(), "cannot be overridden") {
				t.Fatalf("partial descriptor override was not refused before I/O: %v", err)
			}
		})
	}
}
