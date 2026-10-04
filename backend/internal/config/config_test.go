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

func TestAuthenticationIsSecureByDefault(t *testing.T) {
	for _, value := range []string{"", "false", "garbage"} {
		t.Setenv("AUTH_ALLOW_INSECURE_HTTP", value)
		if Load().AuthAllowInsecureHTTP {
			t.Fatalf("insecure auth enabled by %q", value)
		}
	}
	cfg := Load()
	if cfg.MaxSlotTransfers != 20 || cfg.MaxSlotSize != 5*1024*1024*1024 || cfg.MaxSlotExpiry.Hours() != 168 {
		t.Fatalf("unexpected receive limits: %+v", cfg)
	}
}

func TestManifestDefaultMatchesClientCeiling(t *testing.T) {
	t.Setenv("MAX_MANIFEST_SIZE", "")
	if got := Load().MaxManifestSize; got != 1024*1024 {
		t.Fatalf("default manifest size = %d", got)
	}
}
