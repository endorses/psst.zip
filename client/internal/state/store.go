// Package state stores account profiles and private operation journals in an
// encrypted vault. Its process lock is held until Close: callers should open one
// store per command, and must never remove the lock file to recover a stale lock.
package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
)

const (
	ModeKeyring    Mode = "keyring"
	ModePassphrase Mode = "passphrase"
	SchemaVersion       = 1
	maxVaultBytes       = 48 << 20
	maxPlainBytes       = 32 << 20
	maxValueBytes       = 8 << 20
	maxEntries          = 10000
	keyringService      = "zip.psst.cli.vault"
)

var (
	ErrNotFound    = errors.New("private state not found")
	ErrLocked      = errors.New("private state is locked by another command")
	ErrClosed      = errors.New("private state is closed")
	ErrUnavailable = errors.New("credential store unavailable; explicitly configure an encrypted passphrase vault for headless use")
	ErrUnlock      = errors.New("cannot unlock private state")
	ErrCorrupt     = errors.New("invalid or unsupported private state")
	ErrConflict    = errors.New("backup conflicts with existing private state")
)

type Mode string

// Keyring deliberately has no Delete method. Logging out must not remove the
// vault master identity or unrelated Receive private keys.
type Keyring interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
}

// ContextKeyring adapters must complete or cancel before returning; spawning an
// uncancelled asynchronous Set is unsafe when the command releases its lock.
type ContextKeyring interface {
	GetContext(context.Context, string, string) (string, error)
	SetContext(context.Context, string, string, string) error
}

type Config struct {
	Dir            string
	Mode           Mode
	Passphrase     []byte
	Keyring        Keyring
	Context        context.Context
	LockTimeout    time.Duration
	NonInteractive bool
	// Tests exercise the actual age format with a deliberately small KDF cost.
	// Production callers cannot lower age's default work factor.
	testWorkFactor int
}

type envelope struct {
	SchemaVersion   int    `json:"schema_version"`
	Mode            Mode   `json:"mode"`
	WrappedIdentity []byte `json:"wrapped_identity,omitempty"`
	Ciphertext      []byte `json:"ciphertext"`
}

type payload struct {
	SchemaVersion int                        `json:"schema_version"`
	Values        map[string]json.RawMessage `json:"values"`
}

type Store struct {
	mu       sync.Mutex
	dir      string
	lock     *os.File
	identity *age.X25519Identity
	envelope envelope
	values   map[string]json.RawMessage
	work     int
}

// Open requires an explicit storage mode. It never falls back from an unavailable
// OS credential store to a file containing plaintext secrets. Passphrase bytes
// belong to the caller and can be cleared immediately after Open returns.
func Open(c Config) (_ *Store, err error) {
	if c.Mode != ModeKeyring && c.Mode != ModePassphrase {
		return nil, errors.New("select keyring or passphrase private-state mode explicitly")
	}
	if c.Mode == ModeKeyring && len(c.Passphrase) != 0 {
		return nil, errors.New("passphrase supplied for keyring private-state mode")
	}
	if c.Mode == ModePassphrase && len(c.Passphrase) == 0 {
		return nil, ErrUnlock
	}
	dir, err := filepath.Abs(c.Dir)
	if err != nil || c.Dir == "" {
		return nil, errors.New("private-state directory is required")
	}
	if err = prepareDirectory(dir); err != nil {
		return nil, err
	}
	lock, err := acquireLock(c, filepath.Join(dir, "vault.lock"))
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, lock: lock, work: c.testWorkFactor}
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()
	path := filepath.Join(dir, "vault.json")
	data, err := readPrivate(path, maxVaultBytes)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if exists {
		if err = decode(data, &s.envelope); err != nil || s.envelope.SchemaVersion != SchemaVersion || s.envelope.Mode != c.Mode || len(s.envelope.Ciphertext) == 0 {
			return nil, ErrCorrupt
		}
	} else {
		s.envelope = envelope{SchemaVersion: SchemaVersion, Mode: c.Mode}
	}
	if c.Mode == ModeKeyring {
		if len(s.envelope.WrappedIdentity) != 0 {
			return nil, ErrCorrupt
		}
		provider := c.Keyring
		if provider == nil {
			provider = OSKeyring{NonInteractive: c.NonInteractive}
		}
		ctx := c.Context
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		get := func() (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if contextual, ok := provider.(ContextKeyring); ok {
				return contextual.GetContext(ctx, keyringService, accountForDirectory(dir))
			}
			value, err := provider.Get(keyringService, accountForDirectory(dir))
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return value, err
		}
		set := func(secret string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if contextual, ok := provider.(ContextKeyring); ok {
				return contextual.SetContext(ctx, keyringService, accountForDirectory(dir), secret)
			}
			err := provider.Set(keyringService, accountForDirectory(dir), secret)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		secret, getErr := get()
		if errors.Is(getErr, ErrNotFound) && !exists {
			identity, makeErr := age.GenerateX25519Identity()
			if makeErr != nil {
				return nil, ErrUnavailable
			}
			secret = identity.String()
			if setErr := set(secret); setErr != nil {
				return nil, ErrUnavailable
			}
			// A rejected/locked keyring must not appear to have persisted a key.
			if persisted, verifyErr := get(); verifyErr != nil || persisted != secret {
				return nil, ErrUnavailable
			}
		} else if getErr != nil {
			return nil, ErrUnavailable
		}
		s.identity, err = age.ParseX25519Identity(secret)
		if err != nil {
			return nil, ErrUnlock
		}
	} else if exists {
		if len(s.envelope.WrappedIdentity) == 0 || len(s.envelope.WrappedIdentity) > 8192 {
			return nil, ErrCorrupt
		}
		var secret []byte
		secret, err = decryptPassphrase(s.envelope.WrappedIdentity, c.Passphrase, 1024)
		if err != nil {
			return nil, ErrUnlock
		}
		s.identity, err = age.ParseX25519Identity(string(secret))
		clear(secret)
		if err != nil {
			return nil, ErrUnlock
		}
	} else {
		s.identity, err = age.GenerateX25519Identity()
		if err != nil {
			return nil, ErrUnlock
		}
		s.envelope.WrappedIdentity, err = encryptPassphrase([]byte(s.identity.String()), c.Passphrase, s.work)
		if err != nil {
			return nil, ErrUnlock
		}
	}
	if exists {
		plaintext, decryptErr := decrypt(s.envelope.Ciphertext, s.identity, maxPlainBytes)
		if decryptErr != nil {
			return nil, ErrUnlock
		}
		defer clear(plaintext)
		s.values, err = parsePayload(plaintext)
		if err != nil {
			return nil, err
		}
	} else {
		s.values = make(map[string]json.RawMessage)
		if err = s.persist(s.values); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func accountForDirectory(dir string) string {
	id := sha256.Sum256([]byte(dir))
	return hex.EncodeToString(id[:])
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	for _, value := range s.values {
		clear(value)
	}
	s.values, s.identity = nil, nil
	lock := s.lock
	s.lock = nil
	unlockErr := unlockFile(lock)
	return errors.Join(unlockErr, lock.Close())
}

func validKey(key string) bool {
	return key != "" && len(key) <= 1024 && !strings.ContainsAny(key, "\x00\r\n")
}

func (s *Store) Get(key string, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return ErrClosed
	}
	value, ok := s.values[key]
	if !ok {
		return ErrNotFound
	}
	if err := json.Unmarshal(value, out); err != nil {
		return ErrCorrupt
	}
	return nil
}

func (s *Store) Put(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return ErrClosed
	}
	if !validKey(key) {
		return ErrCorrupt
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxValueBytes {
		return ErrCorrupt
	}
	next := copyValues(s.values)
	next[key] = encoded
	if err = s.persist(next); err != nil {
		return err
	}
	s.values = next
	return nil
}

// Delete removes only the requested entry. Callers must remove a session entry
// on logout, not the profile namespace containing locally created private keys.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return ErrClosed
	}
	if _, exists := s.values[key]; !exists {
		return ErrNotFound
	}
	next := copyValues(s.values)
	delete(next, key)
	if err := s.persist(next); err != nil {
		return err
	}
	s.values = next
	return nil
}

func (s *Store) Keys(prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil, ErrClosed
	}
	keys := make([]string, 0)
	for key := range s.values {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// ScopeKey isolates a normalized origin and owner. HTTP origins are retained for
// explicitly selected LAN servers; transport policy belongs to the API client.
func ScopeKey(origin, owner, key string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(owner) != owner || owner == "" || !validKey(key) {
		return "", errors.New("state scope requires an origin, owner and key")
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	u.Path = ""
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	id := sha256.Sum256([]byte(u.String() + "\x00" + owner))
	return "profiles/" + hex.EncodeToString(id[:]) + "/" + key, nil
}

func copyValues(values map[string]json.RawMessage) map[string]json.RawMessage {
	next := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		next[key] = value
	}
	return next
}

func parsePayload(data []byte) (map[string]json.RawMessage, error) {
	var p payload
	if len(data) > maxPlainBytes || decode(data, &p) != nil || p.SchemaVersion != SchemaVersion || p.Values == nil || len(p.Values) > maxEntries {
		return nil, ErrCorrupt
	}
	for key, value := range p.Values {
		if !validKey(key) || len(value) > maxValueBytes || !json.Valid(value) {
			return nil, ErrCorrupt
		}
	}
	return p.Values, nil
}

func decode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return ErrCorrupt
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrCorrupt
	}
	return nil
}

func (s *Store) persist(values map[string]json.RawMessage) error {
	if len(values) > maxEntries {
		return ErrCorrupt
	}
	plaintext, err := json.Marshal(payload{SchemaVersion: SchemaVersion, Values: values})
	if err != nil || len(plaintext) > maxPlainBytes {
		return ErrCorrupt
	}
	defer clear(plaintext)
	var ciphertext bytes.Buffer
	w, err := age.Encrypt(&ciphertext, s.identity.Recipient())
	if err != nil {
		return ErrUnlock
	}
	if _, err = w.Write(plaintext); err != nil {
		return ErrUnlock
	}
	if err = w.Close(); err != nil {
		return ErrUnlock
	}
	env := s.envelope
	env.Ciphertext = ciphertext.Bytes()
	encoded, err := json.Marshal(env)
	if err != nil || len(encoded) > maxVaultBytes {
		return ErrCorrupt
	}
	if err = atomicWrite(filepath.Join(s.dir, "vault.json"), encoded); err != nil {
		return err
	}
	s.envelope = env
	return nil
}

func decrypt(data []byte, identity age.Identity, limit int64) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(data), identity)
	if err != nil {
		return nil, ErrUnlock
	}
	plaintext, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(plaintext)) > limit {
		clear(plaintext)
		return nil, ErrCorrupt
	}
	return plaintext, nil
}

func encryptPassphrase(data, passphrase []byte, work int) ([]byte, error) {
	r, err := age.NewScryptRecipient(string(passphrase))
	if err != nil {
		return nil, ErrUnlock
	}
	if work != 0 {
		r.SetWorkFactor(work)
	}
	var ciphertext bytes.Buffer
	w, err := age.Encrypt(&ciphertext, r)
	if err != nil {
		return nil, ErrUnlock
	}
	if _, err = w.Write(data); err != nil {
		return nil, ErrUnlock
	}
	if err = w.Close(); err != nil {
		return nil, ErrUnlock
	}
	return ciphertext.Bytes(), nil
}

func decryptPassphrase(data, passphrase []byte, limit int64) ([]byte, error) {
	i, err := age.NewScryptIdentity(string(passphrase))
	if err != nil {
		return nil, ErrUnlock
	}
	// A hostile backup may not request unbounded KDF memory or CPU.
	i.SetMaxWorkFactor(18)
	return decrypt(data, i, limit)
}

// Backup writes a separately passphrase-encrypted snapshot, including all local
// Receive keys and journals. Callers own the destination and must not print it.
func (s *Store) Backup(w io.Writer, backupPassphrase []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return ErrClosed
	}
	plaintext, err := json.Marshal(payload{SchemaVersion: SchemaVersion, Values: s.values})
	if err != nil {
		return ErrCorrupt
	}
	defer clear(plaintext)
	ciphertext, err := encryptPassphrase(plaintext, backupPassphrase, s.work)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, bytes.NewReader(ciphertext))
	return err
}

// Restore merges a backup without overwriting existing private keys. Conflicting
// entries fail atomically; restoring never deletes newer local journals or keys.
func (s *Store) Restore(r io.Reader, backupPassphrase []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return ErrClosed
	}
	ciphertext, err := io.ReadAll(io.LimitReader(r, maxVaultBytes+1))
	if err != nil || len(ciphertext) > maxVaultBytes {
		return ErrCorrupt
	}
	plaintext, err := decryptPassphrase(ciphertext, backupPassphrase, maxPlainBytes)
	if err != nil {
		return ErrUnlock
	}
	defer clear(plaintext)
	values, err := parsePayload(plaintext)
	if err != nil {
		return err
	}
	next := copyValues(s.values)
	for key, value := range values {
		if current, exists := next[key]; exists && !bytes.Equal(current, value) {
			return ErrConflict
		}
		next[key] = value
	}
	if err = s.persist(next); err != nil {
		return err
	}
	s.values = next
	return nil
}

func acquireLock(c Config, path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, ErrCorrupt
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.New("cannot open private-state lock")
	}
	if err = protectFile(path); err != nil {
		_ = f.Close()
		return nil, err
	}
	ctx := c.Context
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := c.LockTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ErrLocked
		default:
		}
		locked, lockErr := tryLockFile(f)
		if lockErr != nil {
			_ = f.Close()
			return nil, errors.New("cannot acquire private-state lock")
		}
		if locked {
			return f, nil
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ErrLocked
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func prepareDirectory(dir string) error {
	// Reject symlinks throughout the selected path before creating private files.
	for current := dir; ; current = filepath.Dir(current) {
		if info, err := os.Lstat(current); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
			return errors.New("private-state directory must not contain symlinks")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot inspect private-state directory")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create private-state directory")
	}
	return protectDirectory(dir)
}

func readPrivate(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrCorrupt
	}
	if err = protectFile(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read private state")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrCorrupt
	}
	return data, nil
}

func atomicWrite(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return ErrCorrupt
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".vault-pending-*")
	if err != nil {
		return errors.New("cannot create encrypted-state replacement")
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = protectFile(name); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return errors.New("cannot write encrypted-state replacement")
	}
	if err = f.Sync(); err != nil {
		return errors.New("cannot synchronize encrypted-state replacement")
	}
	if err = f.Close(); err != nil {
		return errors.New("cannot close encrypted-state replacement")
	}
	if err = os.Rename(name, path); err != nil {
		return errors.New("cannot replace encrypted private state")
	}
	if err = syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("cannot synchronize private-state directory: %w", err)
	}
	return nil
}
