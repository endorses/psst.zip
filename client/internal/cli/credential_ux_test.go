package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	clientcrypto "github.com/endorses/psst.zip/client/internal/crypto"
	"github.com/endorses/psst.zip/client/internal/state"
	"github.com/endorses/psst.zip/client/protocol"
)

const credentialUXOrigin = "https://files.example"
const credentialUXOwner = "12345678-1234-1234-1234-123456789011"

// This test wallet only holds disposable identities in memory. Never reach the
// user's Secret Service, Keychain, Credential Manager or configuration directory.
type credentialUXKeyring struct {
	mu     sync.Mutex
	values map[string]string
	sets   int
}

func (k *credentialUXKeyring) Get(service, account string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	value, exists := k.values[service+"\x00"+account]
	if !exists {
		return "", state.ErrNotFound
	}
	return value, nil
}

func (k *credentialUXKeyring) Set(service, account, secret string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.values == nil {
		k.values = make(map[string]string)
	}
	k.values[service+"\x00"+account] = secret
	k.sets++
	return nil
}

func (*credentialUXKeyring) ProviderName() string { return "secret-service" }

type credentialUXTransport func(*http.Request) (*http.Response, error)

func (f credentialUXTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func credentialUXResponse(request *http.Request, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func credentialUXProfile() profile {
	return profile{
		Origin: credentialUXOrigin, Owner: credentialUXOwner, Username: "alice",
		Session: &protocol.Session{
			Token:     strings.Repeat("a", 43),
			User:      protocol.User{ID: credentialUXOwner, Username: "alice", Role: "user"},
			SessionID: "12345678-1234-1234-1234-123456789012",
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}
}

func credentialUXOpen(t *testing.T, dir string, keyring state.Keyring) *state.Store {
	t.Helper()
	vault, err := state.Open(state.Config{Dir: dir, Mode: state.ModeAuto, Keyring: keyring, LockTimeout: time.Second, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func credentialUXStateOpener(t *testing.T, dir string, keyring state.Keyring) func(state.Config) (*state.Store, error) {
	t.Helper()
	return func(config state.Config) (*state.Store, error) {
		if config.Mode != state.ModeAuto || !config.NonInteractive {
			t.Fatal("ordinary piped/JSON command did not reuse automatic, noninteractive storage")
		}
		config.Dir, config.Keyring = dir, keyring
		return state.Open(config)
	}
}

func TestCredentialUXSavedSessionReuseAndExpiryRetainsKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	keyring := new(credentialUXKeyring)
	profile := credentialUXProfile()
	private, _, err := clientcrypto.GenerateReceiveKey()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(private)
	vault := credentialUXOpen(t, dir, keyring)
	if err := vault.Put("profiles/default", profile); err != nil {
		t.Fatal(err)
	}
	owner, err := scope(vault, profile.Origin, profile.Owner)
	if err != nil {
		t.Fatal(err)
	}
	const resource = "resource/receive/12345678-1234-1234-1234-123456789013"
	if err := owner.Put(resource, map[string][]byte{"key": private}); err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	requests := 0
	transport := credentialUXTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Scheme+"://"+request.URL.Host != credentialUXOrigin || request.Header.Get("Authorization") != "Bearer "+profile.Session.Token {
			t.Fatal("saved credential was absent or used on a different origin")
		}
		switch request.URL.Path {
		case "/api/v1/auth/me":
			return credentialUXResponse(request, "application/json", `{"user":{"id":"`+credentialUXOwner+`","username":"alice","role":"user"}}`), nil
		case "/api/v1/auth/resources":
			return credentialUXResponse(request, "application/json", `{"paginated":true,"transfers":[],"slots":[],"next_cursor":null}`), nil
		default:
			t.Fatal("routine commands attempted login or an unexpected endpoint")
			return nil, nil
		}
	})
	// No server, username, storage-mode or credential flags are needed after
	// initial setup. Each invocation opens and decrypts the durable vault again.
	for _, args := range [][]string{{"auth", "status"}, {"transfers", "list"}} {
		var out, diagnostics bytes.Buffer
		app := App{In: strings.NewReader(""), Out: &out, Err: &diagnostics, HTTP: &http.Client{Transport: transport}, OpenState: credentialUXStateOpener(t, dir, keyring)}
		if exit := app.Run(context.Background(), args); exit != ExitOK {
			t.Fatalf("saved session invocation failed with exit %d: %s", exit, diagnostics.String())
		}
		if bytes.Contains(out.Bytes(), []byte(profile.Session.Token)) || bytes.Contains(out.Bytes(), []byte(base64.StdEncoding.EncodeToString(private))) {
			t.Fatal("status/list exposed an account credential or Receive private key")
		}
		if args[0] == "auth" {
			var data struct {
				Authenticated bool       `json:"authenticated"`
				Storage       state.Info `json:"storage"`
			}
			if err := json.Unmarshal(out.Bytes(), &data); err != nil || !data.Authenticated || data.Storage.Mode != state.ModeKeyring || data.Storage.Provider != "secret-service" {
				t.Fatal("successful status did not report the remembered storage provider")
			}
		}
	}
	if requests != 2 || keyring.sets != 1 {
		t.Fatal("routine reuse renewed the account or replaced its vault identity")
	}
	vault = credentialUXOpen(t, dir, keyring)
	profile.Session.ExpiresAt = time.Now().Add(-time.Hour)
	if err := vault.Put("profiles/default", profile); err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	app := App{In: strings.NewReader(""), Out: &out, Err: &diagnostics, HTTP: &http.Client{Transport: transport}, OpenState: credentialUXStateOpener(t, dir, keyring)}
	if exit := app.Run(context.Background(), []string{"auth", "status", "--json"}); exit != ExitAuth {
		t.Fatalf("expired saved session returned exit %d", exit)
	}
	var failure result
	if err := json.Unmarshal(out.Bytes(), &failure); err != nil || failure.OK || failure.Error == nil || !strings.Contains(failure.Error.Message, "psst auth login") || requests != 2 {
		t.Fatal("expired session did not fail locally with a concrete renewal command")
	}
	vault = credentialUXOpen(t, dir, keyring)
	defer vault.Close()
	owner, err = scope(vault, profile.Origin, profile.Owner)
	if err != nil {
		t.Fatal(err)
	}
	var retained struct {
		Key []byte `json:"key"`
	}
	if err := owner.Get(resource, &retained); err != nil || !bytes.Equal(retained.Key, private) {
		t.Fatal("session expiry removed a locally held Receive private key")
	}
}

func TestCredentialUXRenewalUsesRememberedLegacyAccount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	keyring := new(credentialUXKeyring)
	p := credentialUXProfile()
	p.Username = "" // Before the explicit username field was added.
	p.Session.ExpiresAt = time.Now().Add(-time.Hour)
	vault := credentialUXOpen(t, dir, keyring)
	if err := vault.Put("profiles/default", p); err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	logins := 0
	transport := credentialUXTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme+"://"+request.URL.Host != credentialUXOrigin || request.Header.Get("Authorization") != "" {
			t.Fatal("renewal forwarded stale credentials or used the wrong origin")
		}
		switch request.URL.Path {
		case "/api/v1/health":
			return credentialUXResponse(request, "application/json", `{"service":"psst.zip","api_version":1}`), nil
		case "/d/_connection_check", "/u/_connection_check":
			return credentialUXResponse(request, "text/html", `<meta name="psst-web" content="1">`), nil
		case "/api/v1/auth/login":
			logins++
			var input struct {
				Username    string `json:"username"`
				Password    string `json:"password"`
				SessionType string `json:"session_type"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.Username != "alice" || input.Password != strings.Repeat("p", 16) || input.SessionType != "device" {
				t.Fatal("remembered legacy account was not used for explicit renewal")
			}
			fresh := credentialUXProfile().Session
			fresh.Token = strings.Repeat("b", 43)
			body, err := json.Marshal(fresh)
			if err != nil {
				t.Fatal(err)
			}
			return credentialUXResponse(request, "application/json", string(body)), nil
		default:
			t.Fatal("unexpected renewal endpoint")
			return nil, nil
		}
	})
	var out, diagnostics bytes.Buffer
	app := App{In: strings.NewReader(strings.Repeat("p", 16) + "\n"), Out: &out, Err: &diagnostics, HTTP: &http.Client{Transport: transport}, OpenState: credentialUXStateOpener(t, dir, keyring)}
	if exit := app.Run(context.Background(), []string{"auth", "login", "--password-stdin", "--json"}); exit != ExitOK {
		t.Fatalf("remembered account renewal failed with exit %d: %s", exit, out.String())
	}
	var response struct {
		OK   bool `json:"ok"`
		Data struct {
			Storage state.Info `json:"storage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil || !response.OK || response.Data.Storage.Mode != state.ModeKeyring || logins != 1 {
		t.Fatal("login did not report successful renewal and retained storage mode")
	}
	vault = credentialUXOpen(t, dir, keyring)
	defer vault.Close()
	saved, err := loadProfile(vault, "default")
	if err != nil || saved.Username != "alice" || saved.Session == nil || saved.Session.Token != strings.Repeat("b", 43) {
		t.Fatal("renewed account and explicit username were not persisted")
	}
}

func TestCredentialUXLoginDefaultsStayBoundToOrigin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	vault := credentialUXOpen(t, dir, new(credentialUXKeyring))
	defer vault.Close()
	p := credentialUXProfile()
	p.Session = nil // Renewal remains usable after explicit logout too.
	if err := vault.Put("profiles/default", p); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, server, username, wantServer, wantUsername string
		denied                                           bool
	}{
		{"remembered signed-out account", "", "", credentialUXOrigin, "alice", false},
		{"canonical same origin", "https://FILES.example:443/", "", credentialUXOrigin, "alice", false},
		{"other origin cannot inherit account", "https://other.example", "", "", "", true},
		{"explicit other account", "https://other.example", "bob", "https://other.example", "bob", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, username, err := loginDefaults(vault, "default", test.server, test.username)
			if test.denied {
				if err == nil || classify(err).exit != ExitUsage || server != "" || username != "" {
					t.Fatal("different origin inherited stored account details")
				}
			} else if err != nil || server != test.wantServer || username != test.wantUsername {
				t.Fatal("saved login defaults were not recovered correctly")
			}
		})
	}
	if _, _, err := loginDefaults(vault, "new-profile", "", ""); err == nil || classify(err).exit != ExitUsage {
		t.Fatal("first login accepted missing server/account identity")
	}
}

type credentialUXNoInput struct{ t *testing.T }

func (r credentialUXNoInput) Read([]byte) (int, error) {
	r.t.Fatal("noninteractive missing-passphrase command attempted a prompt/read")
	return 0, io.EOF
}

// Construct a real passphrase envelope at a tiny public-test-only work factor.
// Production state.Config deliberately offers no way to lower age's KDF cost.
func credentialUXPassphraseVault(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := age.NewScryptRecipient(strings.Repeat("v", 16))
	if err != nil {
		t.Fatal(err)
	}
	recipient.SetWorkFactor(1)
	seal := func(plain []byte, recipient age.Recipient) []byte {
		var output bytes.Buffer
		writer, err := age.Encrypt(&output, recipient)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(plain); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	envelope := struct {
		SchemaVersion   int        `json:"schema_version"`
		Mode            state.Mode `json:"mode"`
		WrappedIdentity []byte     `json:"wrapped_identity"`
		Ciphertext      []byte     `json:"ciphertext"`
	}{state.SchemaVersion, state.ModePassphrase, seal([]byte(identity.String()), recipient), seal([]byte(`{"schema_version":1,"values":{}}`), identity.Recipient())}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vault.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// Prove this is an unlockable real vault, rather than malformed metadata
	// whose parser happens to fail before reaching the missing-passphrase branch.
	vault, err := state.Open(state.Config{Dir: dir, Mode: state.ModeAuto, Passphrase: []byte(strings.Repeat("v", 16)), NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	if vault.Info().Mode != state.ModePassphrase {
		t.Fatal("real passphrase fixture did not preserve its storage mode")
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialUXMissingPassphraseNeverPromptsForJSONOrPipes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	credentialUXPassphraseVault(t, dir)
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonTTY", true: "JSON"}[jsonMode], func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			app := App{In: credentialUXNoInput{t}, Out: &out, Err: &diagnostics, HTTP: &http.Client{Transport: forbiddenTransport{t}}, OpenState: credentialUXStateOpener(t, dir, new(credentialUXKeyring))}
			args := []string{"auth", "status"}
			if jsonMode {
				args = append(args, "--json")
			}
			if exit := app.Run(context.Background(), args); exit != ExitUsage {
				t.Fatalf("missing private descriptor returned exit %d", exit)
			}
			message := diagnostics.String()
			if jsonMode {
				var response result
				if err := json.Unmarshal(out.Bytes(), &response); err != nil || response.OK || response.Error == nil || diagnostics.Len() != 0 {
					t.Fatal("JSON missing-passphrase failure was not one clean structured result")
				}
				message = response.Error.Message
			}
			if !strings.Contains(message, "--vault-passphrase-fd") || strings.Contains(diagnostics.String(), "Vault passphrase:") {
				t.Fatal("noninteractive vault reuse omitted private-descriptor guidance or emitted a prompt")
			}
		})
	}
}
