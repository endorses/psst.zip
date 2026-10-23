package lan

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endorses/psst.zip/client"
	clientcrypto "github.com/endorses/psst.zip/client/internal/crypto"
	"github.com/endorses/psst.zip/client/protocol"
)

func smallConfig(kind string) Config {
	return Config{Kind: kind, Listen: "127.0.0.1:0", Expires: time.Hour, MaxBytes: 1 << 20, MaxFiles: 100, MaxDownloads: 1, MaxStreams: 2}
}
func testAssets() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "browser") })
}
func input(text string) client.InputFile {
	return client.InputFile{Name: "note.txt", Size: int64(len(text)), Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(text)), nil }}
}
func startTest(t *testing.T, cfg Config) *Host {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	h, err := Start(ctx, cfg, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}

func TestSendSnapshotGuestIsolationAndCleanup(t *testing.T) {
	original := filepath.Join(t.TempDir(), "original.txt")
	if err := os.WriteFile(original, []byte("original plaintext"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := client.FileInputs([]string{original})
	if err != nil {
		t.Fatal(err)
	}
	cfg := smallConfig("send")
	cfg.Files = files
	host := startTest(t, cfg)
	staging := host.staging.path
	if host.Startup.Hosting.Listen != host.listener.Addr().String() || host.Startup.Origin != "http://"+host.listener.Addr().String() {
		t.Fatal("startup did not advertise actual listener")
	}
	if err = os.WriteFile(original, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	guest, err := client.New(host.Startup.Origin, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/transfers", "/api/v1/admin/users", "/api/v1/slots"} {
		response, err := http.Get(host.Startup.Origin + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("public route %s status %d", path, response.StatusCode)
		}
	}
	request, _ := http.NewRequest("GET", host.Startup.Origin+"/d/"+host.Startup.ID, nil)
	request.Host = "attacker.example"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode < 400 {
		t.Fatal("untrusted host accepted")
	}
	link, err := protocol.ParseLink(host.Startup.Link)
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	downloaded, err := guest.Fetch(context.Background(), link, output, newMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(downloaded.Files[0])
	if err != nil || string(b) != "original plaintext" {
		t.Fatalf("snapshot changed: %q, %v", b, err)
	}
	if err = host.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = host.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("session staging retained", err)
	}
	if _, err = os.Stat(downloaded.Files[0]); err != nil {
		t.Fatal("received plaintext removed", err)
	}
}

func TestReceiveCollectsAuthenticatedPlaintextWithoutVault(t *testing.T) {
	cfg := smallConfig("receive")
	cfg.MaxFiles = 1
	cfg.OutputDir = t.TempDir()
	host := startTest(t, cfg)
	guest, err := client.New(host.Startup.Origin, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	link, err := protocol.ParseLink(host.Startup.Link)
	if err != nil {
		t.Fatal(err)
	}
	upload, err := guest.UploadWithOptions(context.Background(), link, []client.InputFile{input("Receive-v2 plaintext")}, client.SendOptions{SpoolDir: t.TempDir()}, newMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	if err = host.Wait(); err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(cfg.OutputDir, upload.ID, "note.txt")
	b, err := os.ReadFile(expected)
	if err != nil || string(b) != "Receive-v2 plaintext" {
		t.Fatalf("received %q, %v", b, err)
	}
	if err = host.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(expected); err != nil {
		t.Fatal("cleanup removed received file", err)
	}
}

func TestHostDistinguishesExplicitDeadlineAndSessionExpiry(t *testing.T) {
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	ticks := make(chan time.Time)
	for _, tc := range []struct {
		name      string
		parent    context.Context
		completed int
		want      error
	}{
		{"explicit command deadline", expired, 0, context.DeadlineExceeded},
		{"session expiry without work", context.Background(), 0, ErrExpired},
		{"session expiry after work", context.Background(), 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &Host{ctx: expired, parent: tc.parent, completions: tc.completed}
			if got := host.wait(ticks); !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidationDoesNotBindOrPrepareInvalidSessions(t *testing.T) {
	cfg := smallConfig("send")
	cfg.Files = []client.InputFile{input("valid")}
	cases := []func(*Config){func(c *Config) { c.MaxBytes = 0 }, func(c *Config) { c.MaxStreams = 0 }, func(c *Config) { c.Expires = 0 }, func(c *Config) { c.TLSCert = "missing" }, func(c *Config) { c.Listen = "0.0.0.0:0" }, func(c *Config) { c.Files[0].Size = 2 << 20 }}
	for i, change := range cases {
		copy := cfg
		copy.Files = append([]client.InputFile(nil), cfg.Files...)
		change(&copy)
		if _, err := Start(context.Background(), copy, testAssets()); err == nil {
			t.Errorf("invalid case %d started", i)
		}
	}
	cfg.TLSCert = "missing-cert"
	cfg.TLSKey = "missing-key"
	if _, err := Start(context.Background(), cfg, testAssets()); err == nil {
		t.Fatal("missing TLS keypair accepted")
	}
}

func TestCancellationAndCrashCleanupLock(t *testing.T) {
	staging, err := newStaging()
	if err != nil {
		t.Fatal(err)
	}
	if err = Cleanup(staging.path); err == nil {
		t.Fatal("cleanup removed active staging")
	}
	if err = staging.lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = Cleanup(staging.path); err != nil {
		t.Fatal(err)
	}
	cfg := smallConfig("send")
	cfg.Files = []client.InputFile{input("hello")}
	ctx, cancel := context.WithCancel(context.Background())
	host, err := Start(ctx, cfg, testAssets())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err = host.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = host.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSuppliedTLSIsVerifiedAndMismatchedKeysFail(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "LAN test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})
	if err = os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := smallConfig("send")
	cfg.Files = []client.InputFile{input("TLS")}
	cfg.TLSCert = certPath
	cfg.TLSKey = keyPath
	host := startTest(t, cfg)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Get(host.Startup.Origin + "/api/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	if !host.Startup.Hosting.TLS || !strings.HasPrefix(host.Startup.Link, "https://") {
		t.Fatal("TLS metadata incorrect")
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrong, _ := x509.MarshalPKCS8PrivateKey(other)
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: wrong}), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Start(context.Background(), cfg, testAssets()); err == nil {
		t.Fatal("mismatched TLS key accepted")
	}
}

func TestOwnerTransportStreamsAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	payload := bytes.Repeat([]byte("x"), 2<<20)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1/", nil)
	response, err := (handlerTransport{handler}).RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 128)
	if _, err = io.ReadFull(response.Body, buffer); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = io.Copy(io.Discard, response.Body); err == nil {
		t.Fatal("cancellation did not terminate owner stream")
	}
	_ = response.Body.Close()
}

func TestReceiveRejectsSymlinkSubmissionDirectory(t *testing.T) {
	cfg := smallConfig("receive")
	cfg.MaxFiles = 1
	cfg.OutputDir = t.TempDir()
	host := startTest(t, cfg)
	guest, err := client.New(host.Startup.Origin, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	link, err := protocol.ParseLink(host.Startup.Link)
	if err != nil {
		t.Fatal(err)
	}
	upload, err := guest.UploadWithOptions(context.Background(), link, []client.InputFile{input("private")}, client.SendOptions{SpoolDir: t.TempDir()}, newMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(cfg.OutputDir, upload.ID)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err = host.Wait(); err == nil {
		t.Fatal("symlink output accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("symlink escape wrote plaintext", err)
	}
}

func TestReceiveCapacityRetryPreservesSessionAndJournal(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cfg := smallConfig("receive")
			cfg.MaxFiles = 1
			cfg.OutputDir = t.TempDir()
			host := startTest(t, cfg)
			guest, err := client.New(host.Startup.Origin, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			link, err := protocol.ParseLink(host.Startup.Link)
			if err != nil {
				t.Fatal(err)
			}
			upload, err := guest.UploadWithOptions(context.Background(), link, []client.InputFile{input("retry plaintext")}, client.SendOptions{SpoolDir: t.TempDir()}, newMemoryStore())
			if err != nil {
				t.Fatal(err)
			}
			private := host.backend.OwnerHandler()
			attempted := make(chan struct{}, 4)
			busy := true
			wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && strings.Contains(r.URL.Path, "/files/") {
					attempted <- struct{}{}
					if busy {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Retry-After", "2")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, `{"error":"stream_busy"}`)
						return
					}
				}
				private.ServeHTTP(w, r)
			})
			host.owner, err = client.New(host.Startup.Origin, &http.Client{Transport: handlerTransport{handler: wrapper}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			ticks := make(chan time.Time)
			finished := make(chan error, 1)
			go func() { finished <- host.wait(ticks) }()
			now := time.Now()
			select {
			case ticks <- now:
			case err := <-finished:
				t.Fatalf("stopped before capacity retry: %v", err)
			}
			select {
			case <-attempted:
			case err := <-finished:
				t.Fatalf("stopped before collecting a file: %v", err)
			}
			// Each unbuffered tick acknowledges the preceding iteration. These timestamps
			// advance retry scheduling without waiting two seconds or sleeping at all.
			for _, offset := range []time.Duration{250 * time.Millisecond, time.Second} {
				select {
				case ticks <- now.Add(offset):
				case err := <-finished:
					t.Fatalf("transient capacity error ended the session: %v", err)
				}
			}
			select {
			case <-attempted:
				t.Fatal("ignored Retry-After and retried a busy stream")
			default:
			}
			if host.ctx.Err() != nil {
				t.Fatal("transient error canceled the session")
			}
			keys, err := host.store.Keys("operation/download/")
			if err != nil || len(keys) != 1 {
				t.Fatalf("download journal lost: %v %v", keys, err)
			}
			busy = false
			select {
			case ticks <- now.Add(2 * time.Second):
			case err := <-finished:
				t.Fatalf("stopped before admission released: %v", err)
			}
			if err = <-finished; err != nil {
				t.Fatal(err)
			}
			<-attempted
			content, err := os.ReadFile(filepath.Join(cfg.OutputDir, upload.ID, "note.txt"))
			if err != nil || string(content) != "retry plaintext" {
				t.Fatalf("retry output %q: %v", content, err)
			}
		})
	}
}

func TestCollectionRetryIsBoundedAndRejectsPermanentFailures(t *testing.T) {
	now := time.Now()
	deadline := now.Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	next, retry := collectionRetry(&client.APIError{Status: 503, RetryAfter: 24 * time.Hour}, now, ctx)
	if !retry || !next.Equal(deadline) {
		t.Fatal("retry outlives the session", next, retry)
	}
	for _, err := range []error{clientcrypto.ErrInvalidData, &client.APIError{Status: 400}, os.ErrPermission} {
		if _, retry := collectionRetry(err, now, ctx); retry {
			t.Fatalf("permanent failure retried: %v", err)
		}
	}
}

func TestTLSRejectsWrongIPExpiredAndNonServerCertificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := smallConfig("send")
	cfg.Files = []client.InputFile{input("TLS")}
	cfg.TLSCert = certPath
	cfg.TLSKey = keyPath
	now := time.Now()
	for _, test := range []struct {
		name     string
		ip       string
		from, to time.Time
		usage    []x509.ExtKeyUsage
	}{
		{"wrong IP", "192.168.1.42", now.Add(-time.Minute), now.Add(time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{"expired", "127.0.0.1", now.Add(-time.Hour), now.Add(-time.Minute), []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{"not yet valid", "127.0.0.1", now.Add(time.Hour), now.Add(2 * time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{"client only", "127.0.0.1", now.Add(-time.Minute), now.Add(time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
	} {
		t.Run(test.name, func(t *testing.T) {
			template := &x509.Certificate{SerialNumber: big.NewInt(1), IPAddresses: []net.IP{net.ParseIP(test.ip)}, NotBefore: test.from, NotAfter: test.to, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: test.usage}
			der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
				t.Fatal(err)
			}
			host, err := Start(context.Background(), cfg, testAssets())
			if host != nil {
				_ = host.Close()
				t.Fatal("unusable HTTPS session published")
			}
			if !errors.Is(err, ErrTLS) {
				t.Fatalf("certificate rejected for wrong reason: %v", err)
			}
		})
	}
}
