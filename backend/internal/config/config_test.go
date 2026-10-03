package config

import "testing"

func TestLegacyDeletionRequiresExplicitOptIn(t *testing.T) {
	for _, value := range []string{"", "false", "not-a-boolean"} {
		t.Setenv("ALLOW_LEGACY_DELETION", value)
		if Load().AllowLegacyDeletion {
			t.Fatalf("legacy deletion enabled by %q", value)
		}
	}
	t.Setenv("ALLOW_LEGACY_DELETION", "true")
	if !Load().AllowLegacyDeletion {
		t.Fatal("explicit legacy deletion opt-in ignored")
	}
}
