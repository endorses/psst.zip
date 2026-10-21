package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

type namedMemoryKeyring struct {
	memoryKeyring
	name string
}

func (m *namedMemoryKeyring) ProviderName() string { return m.name }

func TestAutomaticModeRemembersProtectedStorage(t *testing.T) {
	for _, mode := range []Mode{ModeKeyring, ModePassphrase} {
		t.Run(string(mode), func(t *testing.T) {
			provider := &memoryKeyring{}
			config := passConfig(privateTestDir(t))
			config.Mode, config.Keyring = mode, provider
			if mode == ModeKeyring {
				config.Passphrase = nil
			}
			store := openTest(t, config)
			if err := store.Put("receive/key", "retained fixture identity"); err != nil {
				t.Fatal(err)
			}
			store.Close()
			path := filepath.Join(config.Dir, "vault.json")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			automatic := config
			automatic.Mode = ModeAuto
			if mode == ModePassphrase {
				automatic.Passphrase = nil
				if _, err := Open(automatic); !errors.Is(err, ErrPassphraseRequired) {
					t.Fatalf("remembered passphrase mode lost: %v", err)
				}
				unchanged, _ := os.ReadFile(path)
				if !bytes.Equal(original, unchanged) {
					t.Fatal("asking for a passphrase modified the vault")
				}
				automatic.Passphrase = config.Passphrase
			}
			store = openTest(t, automatic)
			var value string
			if store.Info().Mode != mode || store.Get("receive/key", &value) != nil || value != "retained fixture identity" {
				t.Fatal("automatic reopen lost mode or private records")
			}
			store.Close()
			conflict := automatic
			conflict.Mode = ModePassphrase
			if mode == ModePassphrase {
				conflict.Mode, conflict.Passphrase = ModeKeyring, nil
			}
			if _, err := Open(conflict); !errors.Is(err, ErrModeMismatch) {
				t.Fatalf("explicit conflict was not explained: %v", err)
			}
			unchanged, _ := os.ReadFile(path)
			if !bytes.Equal(original, unchanged) {
				t.Fatal("mode conflict modified private state")
			}
		})
	}
}

func TestAutomaticNewPassphraseVaultAndProviderMigration(t *testing.T) {
	pass := passConfig(privateTestDir(t))
	pass.Mode = ModeAuto
	store := openTest(t, pass)
	if store.Info().Mode != ModePassphrase {
		t.Fatal("explicit private passphrase input did not select encrypted storage")
	}
	store.Close()

	// Older vaults had no provider hint. Remember it only after successful
	// authenticated decryption, without replacing the existing master.
	legacy := &memoryKeyring{}
	config := Config{Dir: privateTestDir(t), Mode: ModeKeyring, Keyring: legacy}
	store = openTest(t, config)
	if err := store.Put("receive/key", "retained legacy fixture"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	path := filepath.Join(config.Dir, "vault.json")
	original, _ := os.ReadFile(path)
	wrong, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	provider := &namedMemoryKeyring{memoryKeyring: memoryKeyring{secret: wrong.String()}, name: "secret-service"}
	config.Mode, config.Keyring = ModeAuto, provider
	if _, err := Open(config); !errors.Is(err, ErrUnlock) {
		t.Fatalf("wrong master was accepted: %v", err)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(original, unchanged) || provider.sets != 0 {
		t.Fatal("provider hint was written before authenticated decryption")
	}
	provider.secret = legacy.secret
	store = openTest(t, config)
	var value string
	if store.Get("receive/key", &value) != nil || value != "retained legacy fixture" || store.Info().Provider != "secret-service" {
		t.Fatal("provider migration lost existing records")
	}
	store.Close()
	current, _ := os.ReadFile(path)
	var record envelope
	if json.Unmarshal(current, &record) != nil || record.KeyringProvider != "secret-service" || provider.sets != 0 {
		t.Fatal("successful provider selection was not durably remembered")
	}
	provider.name = "keychain"
	if _, err := Open(config); !errors.Is(err, ErrUnavailable) {
		t.Fatal("remembered provider silently changed")
	}
	provider.name, provider.secret = "secret-service", ""
	if _, err := Open(config); !errors.Is(err, ErrUnavailable) || provider.sets != 0 {
		t.Fatal("missing remembered master was recreated")
	}
	unchanged, _ = os.ReadFile(path)
	if !bytes.Equal(current, unchanged) {
		t.Fatal("provider failures modified existing keys")
	}
}
