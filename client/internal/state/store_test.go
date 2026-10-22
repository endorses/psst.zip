package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type memoryKeyring struct {
	secret string
	err    error
	sets   int
}

type cancelledContextKeyring struct{ sets int }

func (*cancelledContextKeyring) Get(string, string) (string, error) {
	panic("context-aware provider unexpectedly used blocking Get")
}
func (*cancelledContextKeyring) Set(string, string, string) error {
	panic("context-aware provider unexpectedly used blocking Set")
}
func (*cancelledContextKeyring) GetContext(ctx context.Context, _, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (p *cancelledContextKeyring) SetContext(context.Context, string, string, string) error {
	p.sets++
	return nil
}

func TestCredentialProviderCancellationReleasesLockWithoutLateSet(t *testing.T) {
	dir := privateTestDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	provider := new(cancelledContextKeyring)
	start := time.Now()
	_, err := Open(Config{Dir: dir, Mode: ModeKeyring, Keyring: provider, Context: ctx, NonInteractive: true})
	if !errors.Is(err, ErrUnavailable) || time.Since(start) > time.Second || provider.sets != 0 {
		t.Fatal("cancelled provider continued or attempted a late credential write")
	}
	if _, err := os.Stat(filepath.Join(dir, "vault.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled credential provider wrote vault state")
	}
	// No manual lock deletion and no outstanding asynchronous Set are needed.
	s := openTest(t, passConfig(dir))
	if err := s.Put("key", "new independent fixture"); err != nil {
		t.Fatal(err)
	}
}

func TestCancelledOSCredentialCallsNeverReachPlatformProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := OSKeyring{NonInteractive: true}
	if _, err := provider.GetContext(ctx, "unused-fixture-service", "unused-account"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("cancelled OS read accepted")
	}
	if err := provider.SetContext(ctx, "unused-fixture-service", "unused-account", "unused-fixture"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("cancelled OS write accepted")
	}
}

func (m *memoryKeyring) Get(string, string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if m.secret == "" {
		return "", ErrNotFound
	}
	return m.secret, nil
}
func (m *memoryKeyring) Set(_, _, secret string) error {
	if m.err != nil {
		return m.err
	}
	m.secret = secret
	m.sets++
	return nil
}

func privateTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func passConfig(dir string) Config {
	return Config{Dir: dir, Mode: ModePassphrase, Passphrase: []byte("fixture vault passphrase"), LockTimeout: 60 * time.Millisecond, testWorkFactor: 1}
}

func openTest(t *testing.T, c Config) *Store {
	t.Helper()
	s, err := Open(c)
	if err != nil {

		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestVaultPersistenceScopesAndSessionOnlyLogout(t *testing.T) {
	c := passConfig(privateTestDir(t))
	s := openTest(t, c)
	session, err := ScopeKey("https://EXAMPLE.com:443/", "member-1", "session")
	if err != nil {
		t.Fatal(err)
	}
	alias, _ := ScopeKey("https://example.com", "member-1", "session")
	otherOwner, _ := ScopeKey("https://example.com", "member-2", "session")
	otherOrigin, _ := ScopeKey("http://example.com", "member-1", "session")
	if session != alias || session == otherOwner || session == otherOrigin {
		t.Fatal("scope collision or normalization failure")
	}
	private, _ := ScopeKey("https://example.com", "member-1", "receive/private-key")
	journal, _ := ScopeKey("https://example.com", "member-1", "journal/upload")
	for key, value := range map[string]string{session: "fixture session secret", private: "fixture private receive identity", journal: "fixture ciphertext nonce journal"} {
		if err := s.Put(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete(session); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, c)
	var value string
	if err := s.Get(session, &value); !errors.Is(err, ErrNotFound) {
		t.Fatal("logout retained session")
	}
	if err := s.Get(private, &value); err != nil || value != "fixture private receive identity" {
		t.Fatal("logout lost Receive key")
	}
	if err := s.Get(journal, &value); err != nil {
		t.Fatal("logout lost operation journal")
	}
	keys, err := s.Keys(strings.TrimSuffix(private, "receive/private-key"))
	if err != nil || len(keys) != 2 {
		t.Fatalf("unexpected retained records: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(c.Dir, "vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture session secret", "fixture private receive identity", "fixture ciphertext nonce journal", "receive/private-key", "journal/upload"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("private state leaked plaintext")
		}
	}
}

func TestNoImplicitFallbackAndMissingMasterNeverReinitializes(t *testing.T) {
	dir := privateTestDir(t)
	if _, err := Open(Config{Dir: dir}); err == nil {
		t.Fatal("implicit mode accepted")
	}
	unavailable := &memoryKeyring{err: errors.New("fixture provider unavailable")}
	if _, err := Open(Config{Dir: dir, Mode: ModeKeyring, Keyring: unavailable}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("keyring unavailability silently bypassed")
	}
	if _, err := os.Stat(filepath.Join(dir, "vault.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unavailable provider wrote a vault")
	}
	provider := &memoryKeyring{}
	c := Config{Dir: dir, Mode: ModeKeyring, Keyring: provider}
	s := openTest(t, c)
	if err := s.Put("receive/key", "private fixture"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if provider.sets != 1 {
		t.Fatal("master not created exactly once")
	}
	provider.secret = ""
	if _, err := Open(c); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing master silently replaced")
	}
	if provider.sets != 1 {
		t.Fatal("existing master was recreated")
	}
}

func TestWrongPassphraseCorruptionAndFutureSchemaFailWithoutOverwrite(t *testing.T) {
	c := passConfig(privateTestDir(t))
	s := openTest(t, c)
	if err := s.Put("receive/key", "private fixture"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Dir, "vault.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wrong := c
	wrong.Passphrase = []byte("a wrong fixture passphrase")
	if _, err = Open(wrong); !errors.Is(err, ErrUnlock) {
		t.Fatalf("wrong password: %v", err)
	}
	var e envelope
	if err = json.Unmarshal(original, &e); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{0, SchemaVersion + 1} {
		e.SchemaVersion = version
		data, _ := json.Marshal(e)
		if err = os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err = Open(c); !errors.Is(err, ErrCorrupt) {
			t.Fatal("unsupported schema accepted")
		}
		current, _ := os.ReadFile(path)
		if !bytes.Equal(current, data) {
			t.Fatal("unsupported vault overwritten")
		}
	}
	e.SchemaVersion = SchemaVersion
	e.Ciphertext[len(e.Ciphertext)-1] ^= 1
	corrupt, _ := json.Marshal(e)
	if err = os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(c); !errors.Is(err, ErrUnlock) {
		t.Fatal("corrupted ciphertext accepted")
	}
	if err = os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, c)
	var value string
	if err = s.Get("receive/key", &value); err != nil || value != "private fixture" {
		t.Fatal("failed opens lost private key")
	}
}

func TestBackupRestoreIsEncryptedBoundedAndConflictSafe(t *testing.T) {
	source := openTest(t, passConfig(privateTestDir(t)))
	if err := source.Put("receive/private-key", "fixture retained private key"); err != nil {
		t.Fatal(err)
	}
	var encrypted bytes.Buffer
	phrase := []byte("separate backup fixture passphrase")
	if err := source.Backup(&encrypted, phrase); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted.Bytes(), []byte("fixture retained private key")) {
		t.Fatal("backup exposed private key")
	}
	targetConfig := passConfig(privateTestDir(t))
	target := openTest(t, targetConfig)
	if err := target.Put("journal/new-operation", "keep newer state"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(targetConfig.Dir, "vault.json"))
	if err := target.Restore(bytes.NewReader(encrypted.Bytes()), []byte("wrong backup passphrase")); !errors.Is(err, ErrUnlock) {
		t.Fatal("wrong backup passphrase accepted")
	}
	after, _ := os.ReadFile(filepath.Join(targetConfig.Dir, "vault.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("failed backup restore changed vault")
	}
	if err := target.Restore(bytes.NewReader(encrypted.Bytes()), phrase); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := target.Get("journal/new-operation", &value); err != nil || value != "keep newer state" {
		t.Fatal("restore removed newer journal")
	}
	if err := target.Get("receive/private-key", &value); err != nil || value != "fixture retained private key" {
		t.Fatal("backup did not restore key")
	}
	if err := target.Put("receive/private-key", "newer different private key"); err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(filepath.Join(targetConfig.Dir, "vault.json"))
	if err := target.Restore(bytes.NewReader(encrypted.Bytes()), phrase); !errors.Is(err, ErrConflict) {
		t.Fatal("restore overwrote conflicting private key")
	}
	after, _ = os.ReadFile(filepath.Join(targetConfig.Dir, "vault.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("conflicting restore was not atomic")
	}
	if _, err := parsePayload([]byte(`{"schema_version":2,"values":{}}`)); !errors.Is(err, ErrCorrupt) {
		t.Fatal("future backup schema accepted")
	}
}

func TestFailedAtomicReplacementPreservesInMemoryAndDiskState(t *testing.T) {
	c := passConfig(privateTestDir(t))
	s := openTest(t, c)
	if err := s.Put("key", "old fixture"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Dir, "vault.json")
	saved := filepath.Join(c.Dir, "previous.json")
	if err := os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("key", "new fixture"); !errors.Is(err, ErrCorrupt) {
		t.Fatal("unsafe replacement accepted")
	}
	var value string
	if err := s.Get("key", &value); err != nil || value != "old fixture" {
		t.Fatal("failed write changed memory")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, path); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, c)
	if err := s.Get("key", &value); err != nil || value != "old fixture" {
		t.Fatal("failed write lost persisted key")
	}
}

func TestConcurrentCommandLockAndCancellation(t *testing.T) {
	c := passConfig(privateTestDir(t))
	s := openTest(t, c)
	if _, err := Open(c); !errors.Is(err, ErrLocked) {
		t.Fatal("second writer obtained lock")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Context = ctx
	if _, err := Open(c); !errors.Is(err, ErrLocked) {
		t.Fatal("cancelled lock wait succeeded")
	}
	c.Context = nil
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, c)
	if err := s.Put("after-recovery", true); err != nil {
		t.Fatal(err)
	}
}

func TestProcessDeathReleasesLockWithoutDeletingKeys(t *testing.T) {
	if os.Getenv("PSST_STATE_LOCK_HELPER") != "" {
		s, err := Open(passConfig(os.Getenv("PSST_STATE_LOCK_HELPER")))
		if err != nil {
			os.Exit(3)
		}
		if err = s.Put("receive/key", "survive process death"); err != nil {
			os.Exit(4)
		}
		_, _ = io.WriteString(os.Stdout, "ready\n")
		<-time.After(time.Hour)
	}
	c := passConfig(privateTestDir(t))
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessDeathReleasesLockWithoutDeletingKeys$")
	cmd.Env = append(os.Environ(), "PSST_STATE_LOCK_HELPER="+c.Dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make([]byte, 6)
	if _, err = io.ReadFull(stdout, ready); err != nil || string(ready) != "ready\n" {
		t.Fatal("helper failed to initialize")
	}
	if _, err = Open(c); !errors.Is(err, ErrLocked) {
		t.Fatal("running process did not hold lock")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	s := openTest(t, c)
	var value string
	if err = s.Get("receive/key", &value); err != nil || value != "survive process death" {
		t.Fatal("recovery lost Receive key")
	}
}

func TestPrivatePermissionsSymlinksAndFileBounds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions and symlink fixture; Windows ACL fixture requires native Windows")
	}
	c := passConfig(privateTestDir(t))
	s := openTest(t, c)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vault.json", "vault.lock"} {
		info, err := os.Stat(filepath.Join(c.Dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("state file is not private")
		}
	}
	if err := os.Chmod(c.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(c); err == nil {
		t.Fatal("unsafe directory accepted")
	}
	if err := os.Chmod(c.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(privateTestDir(t), "linked")
	if err := os.Symlink(c.Dir, alias); err != nil {
		t.Fatal(err)
	}
	linked := c
	linked.Dir = alias
	if _, err := Open(linked); err == nil {
		t.Fatal("symlink state directory accepted")
	}
	path := filepath.Join(c.Dir, "vault.json")
	if err := os.Truncate(path, maxVaultBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(c); !errors.Is(err, ErrCorrupt) {
		t.Fatal("oversized vault accepted")
	}
}
